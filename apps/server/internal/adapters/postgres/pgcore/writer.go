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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
)

// Writer 实现总览 4.1 的统一写事务：账号锁 → 收据去重 → 业务写入 → 变更日志 → 收据 → River 入队 → 提交。
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

// Run 执行一次写事务。fn 在账号锁内执行业务写入；reload 读取 primary 的当前资源填充 Data。
func (w *Writer) Run(ctx context.Context, req write.Request, fn func(ctx context.Context, scope *TxScope) error, reload func(ctx context.Context, scope *TxScope) (any, error)) (write.Result, error) {
	started := time.Now()
	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	write.RecordTiming(ctx, "pool_begin", time.Since(started))
	if err != nil {
		return write.Result{}, apperr.Dependency(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbgen.New(tx)
	now := w.clock.Now()
	started = time.Now()
	lock, err := q.LockAccountForWrite(ctx, req.AccountID)
	write.RecordTiming(ctx, "account_lock", time.Since(started))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return write.Result{}, apperr.Unauthorized("SESSION_EXPIRED", "")
		}
		return write.Result{}, apperr.Internal(err)
	}
	if lock.Status != "active" {
		return write.Result{}, apperr.Forbidden("ACCOUNT_DELETING", "账号正在注销")
	}
	scope := &TxScope{Tx: tx, Queries: q, AccountID: req.AccountID, Now: now}

	// 幂等：已成功的相同操作直接重放
	started = time.Now()
	receipt, err := q.GetMutationReceipt(ctx, dbgen.GetMutationReceiptParams{AccountID: req.AccountID, OperationID: req.OperationID})
	write.RecordTiming(ctx, "receipt_lookup", time.Since(started))
	switch {
	case err == nil:
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
	case !errors.Is(err, pgx.ErrNoRows):
		return write.Result{}, apperr.Internal(err)
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

	// 变更日志：连续序列，同一批次
	started = time.Now()
	endSeq, err := RecordChanges(ctx, q, req.AccountID, req.OperationID, lock.LastSeq, scope.changes, now)
	write.RecordTiming(ctx, "sync_changes", time.Since(started))
	if err != nil {
		return write.Result{}, apperr.Internal(err)
	}
	if endSeq != lock.LastSeq {
		started = time.Now()
		err = q.AdvanceAccountSeq(ctx, dbgen.AdvanceAccountSeqParams{AccountID: req.AccountID, LastSeq: endSeq, UpdatedAt: now})
		write.RecordTiming(ctx, "advance_seq", time.Since(started))
		if err != nil {
			return write.Result{}, apperr.Internal(err)
		}
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
	started = time.Now()
	err = q.InsertMutationReceipt(ctx, dbgen.InsertMutationReceiptParams{
		AccountID: req.AccountID, OperationID: req.OperationID, OperationType: req.OperationType,
		RequestHash: req.Fingerprint[:], Result: stored, CreatedAt: now,
	})
	write.RecordTiming(ctx, "receipt_insert", time.Since(started))
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
		if _, err := w.queue.InsertManyTx(ctx, tx, params); err != nil {
			return write.Result{}, apperr.Internal(err)
		}
	}

	if reload != nil {
		started = time.Now()
		data, err := reload(ctx, scope)
		write.RecordTiming(ctx, "reload", time.Since(started))
		if err != nil {
			return write.Result{}, err
		}
		result.Data = data
	}
	started = time.Now()
	err = tx.Commit(ctx)
	write.RecordTiming(ctx, "commit", time.Since(started))
	if err != nil {
		return write.Result{}, apperr.Dependency(err)
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
