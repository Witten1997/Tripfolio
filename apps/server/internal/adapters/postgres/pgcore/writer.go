package pgcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
)

// Writer 实现总览 4.1 的统一写事务：账号锁 → 收据去重 → 业务写入 → 变更日志 → 收据 → River 入队 → 提交。
//
// 数据库通常在远端，每次往返都是可感知的延迟，因此固定阶段尽量合并：
//   - BEGIN、账号锁与收据查询放进同一条流水线，一次往返；
//   - 变更日志、序号推进与收据写入合成一条语句，没有后台任务时与 COMMIT 同批发送，一次往返。
//
// 业务函数与 reload 各自的查询仍按需往返，由各模块自行合并。
type Writer struct {
	pool   *pgxpool.Pool
	queue  *river.Client[pgx.Tx]
	clock  clock.Clock
	logger *slog.Logger
}

// NewWriter 创建写事务设施。queue 可为 nil（不支持后台任务入队，测试用）。
func NewWriter(pool *pgxpool.Pool, queue *river.Client[pgx.Tx], clk clock.Clock, logger *slog.Logger) *Writer {
	return &Writer{pool: pool, queue: queue, clock: clk, logger: logger}
}

// TxScope 是一次写事务内的记录器与仓储入口。
type TxScope struct {
	Tx        pgx.Tx
	Queries   *dbgen.Queries
	AccountID uuid.UUID
	Now       time.Time

	changes  []write.Change
	warnings []string
	primary  *write.EntityRef
	affected []write.EntityRef
	jobs     []write.JobArgs
}

var _ write.Scope = (*TxScope)(nil)

// Record 登记一条同步变更。
func (s *TxScope) Record(c write.Change) { s.changes = append(s.changes, c) }

// Warn 登记警告代码（去重）。
func (s *TxScope) Warn(code string) {
	for _, w := range s.warnings {
		if w == code {
			return
		}
	}
	s.warnings = append(s.warnings, code)
}

// SetPrimary 设置主资源引用。
func (s *TxScope) SetPrimary(ref write.EntityRef) { s.primary = &ref }

// AddAffected 登记附加的受影响资源引用。
func (s *TxScope) AddAffected(ref write.EntityRef) { s.affected = append(s.affected, ref) }

// Enqueue 登记一个与事务一起提交的后台任务。
func (s *TxScope) Enqueue(args write.JobArgs) error {
	s.jobs = append(s.jobs, args)
	return nil
}

// ChangedFieldsSince 实现 write.MergeSource：读取 (base, current] 各版本的 changed_fields 并集。
func (s *TxScope) ChangedFieldsSince(ctx context.Context, accountID uuid.UUID, entityType string, entityID uuid.UUID, base, current int64) ([]string, bool, error) {
	rows, err := s.Queries.ChangedFieldsBetweenVersions(ctx, dbgen.ChangedFieldsBetweenVersionsParams{
		AccountID: accountID, EntityType: entityType, EntityID: entityID, EntityVersion: base, EntityVersion_2: current,
	})
	if err != nil {
		return nil, false, err
	}
	if int64(len(rows)) != current-base {
		return nil, false, nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, r := range rows {
		for _, f := range r.ChangedFields {
			if _, ok := seen[f]; !ok {
				seen[f] = struct{}{}
				out = append(out, f)
			}
		}
	}
	return out, true, nil
}

const lockAndReceiptQuery = `SELECT s.last_seq, a.status FROM account_sync_state s
JOIN accounts a ON a.id = s.account_id WHERE s.account_id = $1 FOR UPDATE OF s`

const receiptLookupQuery = `SELECT request_hash, result FROM mutation_receipts WHERE account_id = $1 AND operation_id = $2`

// finalizeQuery 一次写入本次事务的全部变更日志、推进账号序号并保存收据。
// 变更以 JSON 数组传入，按顺序分配 last_seq+1 起的连续序列；没有变更时只写收据。
const finalizeQuery = `
WITH input AS (
    SELECT value, ordinality FROM jsonb_array_elements($6::jsonb) WITH ORDINALITY AS t(value, ordinality)
), changes AS (
    INSERT INTO sync_changes (
        account_id, seq, batch_id, batch_end_seq, entity_type, entity_id, trip_id,
        entity_version, change_kind, schema_version, snapshot, changed_fields, requires_snapshot, created_at
    )
    SELECT $1, $2 + input.ordinality, $3, $2 + $4, input.value->>'entity_type',
           (input.value->>'entity_id')::uuid, (input.value->>'trip_id')::uuid,
           (input.value->>'version')::bigint, input.value->>'kind', 1,
           CASE WHEN input.value->>'kind' = 'upsert' THEN input.value->'snapshot' ELSE NULL END,
           CASE WHEN input.value->>'kind' = 'upsert' THEN
               ARRAY(SELECT jsonb_array_elements_text(input.value->'changed_fields'))
           ELSE NULL END,
           (input.value->>'requires_snapshot')::boolean, $5
    FROM input
    RETURNING seq
), advanced AS (
    UPDATE account_sync_state SET last_seq = $2 + $4, updated_at = $5
    WHERE account_id = $1 AND $4 > 0 AND (SELECT count(*) FROM changes) = $4
    RETURNING account_id
)
INSERT INTO mutation_receipts (account_id, operation_id, operation_type, request_hash, result, created_at)
SELECT $1, $3, $7, $8, $9, $5
WHERE $4 = 0 OR EXISTS (SELECT 1 FROM advanced)
RETURNING operation_id`

type changeEntry struct {
	EntityType       string           `json:"entity_type"`
	EntityID         uuid.UUID        `json:"entity_id"`
	TripID           *uuid.UUID       `json:"trip_id"`
	Version          int64            `json:"version"`
	Kind             write.ChangeKind `json:"kind"`
	Snapshot         any              `json:"snapshot"`
	ChangedFields    []string         `json:"changed_fields"`
	RequiresSnapshot bool             `json:"requires_snapshot"`
}

func encodeChanges(changes []write.Change) ([]byte, error) {
	entries := make([]changeEntry, 0, len(changes))
	for _, c := range changes {
		e := changeEntry{
			EntityType: c.EntityType, EntityID: c.EntityID, TripID: c.TripID,
			Version: c.Version, Kind: c.Kind, RequiresSnapshot: c.RequiresSnapshot,
		}
		if c.Kind == write.ChangeUpsert {
			e.Snapshot = c.Snapshot
			e.ChangedFields = c.ChangedFields
			if e.ChangedFields == nil {
				e.ChangedFields = []string{}
			}
		}
		entries = append(entries, e)
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("序列化变更日志: %w", err)
	}
	return payload, nil
}

// Run 执行一次写事务。fn 在账号锁内执行业务写入；reload 读取 primary 的当前资源填充 Data。
func (w *Writer) Run(ctx context.Context, req write.Request, fn func(ctx context.Context, scope *TxScope) error, reload func(ctx context.Context, scope *TxScope) (any, error)) (write.Result, error) {
	started := time.Now()
	conn, err := w.pool.Acquire(ctx)
	write.RecordTiming(ctx, "pool_acquire", time.Since(started))
	if err != nil {
		return write.Result{}, apperr.Dependency(err)
	}
	defer conn.Release()
	tx := &connTx{conn: conn}
	defer func() {
		if !tx.closed {
			_ = tx.Rollback(ctx)
		}
	}()

	// BEGIN、账号锁与收据查询一次往返；账号锁必须是事务第一条语句（总览 4.1）。
	started = time.Now()
	var lock dbgen.LockAccountForWriteRow
	var receipt dbgen.MutationReceipt
	var receiptFound bool
	batch := &pgx.Batch{}
	batch.Queue("BEGIN ISOLATION LEVEL READ COMMITTED")
	batch.Queue(lockAndReceiptQuery, req.AccountID)
	batch.Queue(receiptLookupQuery, req.AccountID, req.OperationID)
	results := conn.SendBatch(ctx, batch)
	_, err = results.Exec()
	if err == nil {
		err = results.QueryRow().Scan(&lock.LastSeq, &lock.Status)
	}
	if err == nil {
		lookupErr := results.QueryRow().Scan(&receipt.RequestHash, &receipt.Result)
		if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
			err = lookupErr
		} else {
			receiptFound = lookupErr == nil
		}
	}
	if closeErr := results.Close(); err == nil {
		err = closeErr
	}
	write.RecordTiming(ctx, "begin_lock_receipt", time.Since(started))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return write.Result{}, apperr.Unauthorized("SESSION_EXPIRED", "")
		}
		return write.Result{}, apperr.Internal(err)
	}
	if lock.Status != "active" {
		return write.Result{}, apperr.Forbidden("ACCOUNT_DELETING", "账号正在注销")
	}
	now := w.clock.Now()
	scope := &TxScope{Tx: tx, Queries: dbgen.New(tx), AccountID: req.AccountID, Now: now}

	// 幂等：已成功的相同操作直接重放
	if receiptFound {
		if string(receipt.RequestHash) != string(req.Fingerprint[:]) {
			return write.Result{}, apperr.Conflicted("IDEMPOTENCY_CONFLICT", "同一操作编号携带了不同的内容")
		}
		var result write.Result
		if err := json.Unmarshal(receipt.Result, &result); err != nil {
			return write.Result{}, apperr.Internal(err)
		}
		result.Replayed = true
		if reload != nil {
			started = time.Now()
			data, reloadErr := reload(ctx, scope)
			write.RecordTiming(ctx, "reload", time.Since(started))
			if reloadErr == nil {
				result.Data = data
			} else if e, ok := apperr.As(reloadErr); !ok || e.Status != 404 && e.Status != 410 {
				return write.Result{}, reloadErr
			}
		}
		started = time.Now()
		err = tx.Commit(ctx)
		write.RecordTiming(ctx, "commit", time.Since(started))
		if err != nil {
			return write.Result{}, apperr.Dependency(err)
		}
		return result, nil
	}

	started = time.Now()
	fnErr := fn(ctx, scope)
	write.RecordTiming(ctx, "business", time.Since(started))
	if err := fnErr; err != nil {
		if _, ok := apperr.As(err); ok {
			return write.Result{}, err
		}
		return write.Result{}, apperr.Internal(err)
	}

	result := write.Result{
		OperationID: req.OperationID,
		Primary:     scope.primary,
		Affected:    write.AffectedRefs(scope.primary, scope.changes, scope.affected),
		Warnings:    scope.warnings,
		Replayed:    false,
	}
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	stored, err := json.Marshal(result)
	if err != nil {
		return write.Result{}, apperr.Internal(err)
	}
	payload, err := encodeChanges(scope.changes)
	if err != nil {
		return write.Result{}, apperr.Internal(err)
	}

	if len(scope.jobs) > 0 {
		if w.queue == nil {
			return write.Result{}, apperr.Internal(errors.New("未配置任务队列，无法入队"))
		}
		params := make([]river.InsertManyParams, 0, len(scope.jobs))
		for _, j := range scope.jobs {
			args, ok := j.(river.JobArgs)
			if !ok {
				return write.Result{}, apperr.Internal(fmt.Errorf("任务 %s 未实现 river.JobArgs", j.Kind()))
			}
			params = append(params, river.InsertManyParams{Args: args})
		}
		started = time.Now()
		_, err := w.queue.InsertManyTx(ctx, tx, params)
		write.RecordTiming(ctx, "enqueue", time.Since(started))
		if err != nil {
			return write.Result{}, apperr.Internal(err)
		}
	}

	// reload 在提交前读取，结果与提交后一致：同一事务内没有其他写入者。
	if reload != nil {
		started = time.Now()
		data, err := reload(ctx, scope)
		write.RecordTiming(ctx, "reload", time.Since(started))
		if err != nil {
			return write.Result{}, err
		}
		result.Data = data
	}

	// 变更日志、序号、收据与 COMMIT 同批发送：语句失败时 COMMIT 在出错事务里会被服务端当作 ROLLBACK 处理。
	started = time.Now()
	final := &pgx.Batch{}
	final.Queue(finalizeQuery, req.AccountID, lock.LastSeq, req.OperationID, len(scope.changes), now,
		payload, req.OperationType, req.Fingerprint[:], stored)
	final.Queue("COMMIT")
	results = conn.SendBatch(ctx, final)
	var operationID uuid.UUID
	err = results.QueryRow().Scan(&operationID)
	if err == nil && operationID != req.OperationID {
		err = fmt.Errorf("操作收据编号不匹配: %s", operationID)
	}
	if err == nil {
		var tag pgconn.CommandTag
		tag, err = results.Exec()
		if err == nil && tag.String() == "ROLLBACK" {
			err = pgx.ErrTxCommitRollback
		}
	}
	closeErr := results.Close()
	if err == nil {
		err = closeErr
	}
	// 批次已把事务带到终态（提交或服务端回滚），后续不再发送回滚语句。
	if err == nil || errors.Is(err, pgx.ErrTxCommitRollback) || conn.Conn().PgConn().TxStatus() == 'I' {
		tx.markClosed()
	}
	write.RecordTiming(ctx, "finalize_commit", time.Since(started))
	if err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return write.Result{}, apperr.Dependency(err)
		}
		return write.Result{}, apperr.Internal(err)
	}
	return result, nil
}

// RecordChanges 为 changes 分配从 lastSeq+1 起的连续序列并写入 sync_changes；返回新的最后序列。
// 注册等不经过 Writer 的事务也用它写账号级变更。
func RecordChanges(ctx context.Context, q *dbgen.Queries, accountID, batchID uuid.UUID, lastSeq int64, changes []write.Change, now time.Time) (int64, error) {
	if len(changes) == 0 {
		return lastSeq, nil
	}
	endSeq := lastSeq + int64(len(changes))
	for i, c := range changes {
		var snapshot []byte
		var fields []string
		if c.Kind == write.ChangeUpsert {
			b, err := json.Marshal(c.Snapshot)
			if err != nil {
				return lastSeq, fmt.Errorf("序列化变更快照: %w", err)
			}
			snapshot = b
			fields = c.ChangedFields
			if fields == nil {
				fields = []string{}
			}
		}
		var tripID uuid.NullUUID
		if c.TripID != nil {
			tripID = uuid.NullUUID{UUID: *c.TripID, Valid: true}
		}
		if err := q.InsertSyncChange(ctx, dbgen.InsertSyncChangeParams{
			AccountID: accountID, Seq: lastSeq + int64(i) + 1, BatchID: batchID, BatchEndSeq: endSeq,
			EntityType: c.EntityType, EntityID: c.EntityID, TripID: tripID, EntityVersion: c.Version,
			ChangeKind: string(c.Kind), SchemaVersion: 1, Snapshot: snapshot, ChangedFields: fields,
			RequiresSnapshot: c.RequiresSnapshot, CreatedAt: now,
		}); err != nil {
			return lastSeq, fmt.Errorf("写入变更日志: %w", err)
		}
	}
	return endSeq, nil
}

// UnitOfWork 把 Writer 适配为模块声明的 write.UnitOfWork[T]：bind 用事务内的 Queries 构造模块仓储。
type UnitOfWork[T any] struct {
	writer *Writer
	bind   func(scope *TxScope) T
}

// NewUnitOfWork 创建适配。
func NewUnitOfWork[T any](writer *Writer, bind func(scope *TxScope) T) *UnitOfWork[T] {
	return &UnitOfWork[T]{writer: writer, bind: bind}
}

// Run 实现 write.UnitOfWork。
func (u *UnitOfWork[T]) Run(ctx context.Context, req write.Request, fn func(ctx context.Context, scope write.Scope, repo T) error, reload func(ctx context.Context, repo T) (any, error)) (write.Result, error) {
	var reloadFn func(ctx context.Context, scope *TxScope) (any, error)
	if reload != nil {
		reloadFn = func(ctx context.Context, scope *TxScope) (any, error) { return reload(ctx, u.bind(scope)) }
	}
	return u.writer.Run(ctx, req, func(ctx context.Context, scope *TxScope) error {
		return fn(ctx, scope, u.bind(scope))
	}, reloadFn)
}
