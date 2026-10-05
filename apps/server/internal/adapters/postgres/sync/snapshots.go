package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/account"
	syncmodule "tripfolio/server/internal/modules/sync"
)

type SnapshotStore struct {
	pool  *pgxpool.Pool
	queue *river.Client[pgx.Tx]
}

func NewSnapshotStore(pool *pgxpool.Pool, queue *river.Client[pgx.Tx]) *SnapshotStore {
	return &SnapshotStore{pool: pool, queue: queue}
}

const snapshotColumns = `id,purpose,selected_trip_ids,status,schema_version,sync_epoch,high_water_seq,item_count,captured_at,expires_at,error_code`

func scanSnapshot(row pgx.Row) (syncmodule.Snapshot, error) {
	var m syncmodule.Snapshot
	var selected []byte
	var high *int64
	var count int64
	err := row.Scan(&m.ID, &m.Purpose, &selected, &m.Status, &m.SchemaVersion, &m.SyncEpoch, &high, &count, &m.CapturedAt, &m.ExpiresAt, &m.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, apperr.NotFound()
	}
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(selected, &m.SelectedTripIDs); err != nil {
		return m, err
	}
	if high != nil {
		s := strconv.FormatInt(*high, 10)
		m.HighWaterSeq = &s
	}
	m.ItemCount = strconv.FormatInt(count, 10)
	return m, nil
}

func loadSnapshot(ctx context.Context, tx pgx.Tx, owner, id uuid.UUID) (syncmodule.Snapshot, error) {
	return scanSnapshot(tx.QueryRow(ctx, `SELECT `+snapshotColumns+` FROM data_snapshots WHERE account_id=$1 AND id=$2`, owner, id))
}

func snapshotAccount(ctx context.Context, tx pgx.Tx, owner uuid.UUID, exclusive bool) (uuid.UUID, int64, error) {
	mode := " FOR SHARE"
	if exclusive {
		mode = " FOR UPDATE"
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM accounts WHERE id=$1`+mode, owner).Scan(&status); err != nil {
		return uuid.Nil, 0, err
	}
	if status != "active" {
		return uuid.Nil, 0, account.StatusError(status)
	}
	var epoch uuid.UUID
	var retained int64
	err := tx.QueryRow(ctx, `SELECT sync_epoch,retained_after_seq FROM account_sync_state WHERE account_id=$1`+mode, owner).Scan(&epoch, &retained)
	return epoch, retained, err
}

func snapshotSession(ctx context.Context, tx pgx.Tx, a actor.Actor) error {
	st, err := dbgen.New(tx).GetSyncReadState(ctx, dbgen.GetSyncReadStateParams{AccountID: a.AccountID, ID: a.SessionID})
	if err != nil {
		return err
	}
	if !st.SessionActive {
		return apperr.Unauthorized("SESSION_EXPIRED", "请重新登录")
	}
	return nil
}

func validateSnapshotSelection(ctx context.Context, tx pgx.Tx, owner uuid.UUID, in syncmodule.SnapshotInput) ([]uuid.UUID, error) {
	selected := make([]uuid.UUID, 0, len(in.SelectedTripIDs))
	for _, id := range in.SelectedTripIDs {
		var deleted, purging bool
		err := tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL,purge_requested_at IS NOT NULL FROM trips WHERE account_id=$1 AND id=$2`, owner, id).Scan(&deleted, &purging)
		if errors.Is(err, pgx.ErrNoRows) {
			var owned bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM entity_tombstones WHERE account_id=$1 AND entity_type='trip' AND entity_id=$2)`, owner, id).Scan(&owned); err != nil {
				return nil, err
			}
			if !owned {
				return nil, apperr.NotFound()
			}
			deleted = true
		} else if err != nil {
			return nil, err
		}
		if deleted || purging {
			if in.Purpose == "trip_reload" {
				return nil, apperr.Gone("TRIP_DELETED", "旅行已删除，无法下载详情")
			}
			continue
		}
		selected = append(selected, id)
	}
	return selected, nil
}

func (s *SnapshotStore) Create(ctx context.Context, a actor.Actor, operation uuid.UUID, in syncmodule.SnapshotInput) (syncmodule.Snapshot, error) {
	var empty syncmodule.Snapshot
	if s.queue == nil {
		return empty, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "快照任务队列尚不可用")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(context.Background())
	epoch, _, err := snapshotAccount(ctx, tx, a.AccountID, true)
	if err != nil {
		return empty, err
	}
	if err := snapshotSession(ctx, tx, a); err != nil {
		return empty, err
	}
	if in.SyncEpoch != epoch {
		return empty, apperr.Conflicted("SYNC_EPOCH_MISMATCH", "请重新建立账号基线")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return empty, err
	}
	hash := sha256.Sum256(raw)
	var kind string
	var previousHash, result []byte
	err = tx.QueryRow(ctx, `SELECT operation_type,request_hash,result FROM mutation_receipts WHERE account_id=$1 AND operation_id=$2`, a.AccountID, operation).Scan(&kind, &previousHash, &result)
	if err == nil {
		if kind != "sync.snapshot.create" || !bytes.Equal(hash[:], previousHash) {
			return empty, apperr.Conflicted("IDEMPOTENCY_CONFLICT", "操作编号已用于其他请求")
		}
		var receipt struct {
			ID uuid.UUID `json:"snapshot_id"`
		}
		if err = json.Unmarshal(result, &receipt); err != nil {
			return empty, err
		}
		meta, err := loadSnapshot(ctx, tx, a.AccountID, receipt.ID)
		if err != nil {
			return empty, err
		}
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		return meta, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	selected, err := validateSnapshotSelection(ctx, tx, a.AccountID, in)
	if err != nil {
		return empty, err
	}
	selectedJSON, _ := json.Marshal(selected)
	id := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO data_snapshots(id,account_id,purpose,selected_trip_ids,schema_version,sync_epoch,expires_at) VALUES($1,$2,$3,$4,2,$5,clock_timestamp()+interval '24 hours')`, id, a.AccountID, in.Purpose, selectedJSON, epoch)
	if err != nil {
		return empty, err
	}
	if _, err = s.queue.InsertTx(ctx, tx, syncmodule.SnapshotArgs{ID: id}, &river.InsertOpts{MaxAttempts: 3}); err != nil {
		return empty, err
	}
	result, _ = json.Marshal(map[string]uuid.UUID{"snapshot_id": id})
	if _, err = tx.Exec(ctx, `INSERT INTO mutation_receipts(account_id,operation_id,operation_type,request_hash,result) VALUES($1,$2,'sync.snapshot.create',$3,$4)`, a.AccountID, operation, hash[:], result); err != nil {
		return empty, err
	}
	meta, err := loadSnapshot(ctx, tx, a.AccountID, id)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return meta, nil
}

func (s *SnapshotStore) Inspect(ctx context.Context, a actor.Actor, id uuid.UUID, fn func(syncmodule.Snapshot, func(int64, int) ([]syncmodule.SnapshotItem, error)) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	epoch, retained, err := snapshotAccount(ctx, tx, a.AccountID, false)
	if err != nil {
		return err
	}
	if err = snapshotSession(ctx, tx, a); err != nil {
		return err
	}
	meta, err := loadSnapshot(ctx, tx, a.AccountID, id)
	if err != nil {
		return err
	}
	if meta.SyncEpoch != epoch {
		return apperr.Conflicted("SYNC_EPOCH_MISMATCH", "快照属于旧同步代次")
	}
	if meta.HighWaterSeq != nil {
		high, err := strconv.ParseInt(*meta.HighWaterSeq, 10, 64)
		if err != nil {
			return err
		}
		if high < retained {
			meta.Status = "invalidated"
		}
	}
	err = fn(meta, func(after int64, limit int) ([]syncmodule.SnapshotItem, error) {
		rows, err := tx.Query(ctx, `SELECT ordinal,entity_type,entity_id,trip_id,entity_version,payload FROM snapshot_items WHERE account_id=$1 AND snapshot_id=$2 AND ordinal>$3 ORDER BY ordinal LIMIT $4`, a.AccountID, id, after, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := make([]syncmodule.SnapshotItem, 0)
		for rows.Next() {
			var item syncmodule.SnapshotItem
			var ordinal, version int64
			if err := rows.Scan(&ordinal, &item.EntityType, &item.EntityID, &item.TripID, &version, &item.Data); err != nil {
				return nil, err
			}
			item.Ordinal = strconv.FormatInt(ordinal, 10)
			item.Version = strconv.FormatInt(version, 10)
			result = append(result, item)
		}
		return result, rows.Err()
	})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *SnapshotStore) Build(ctx context.Context, id uuid.UUID, lastAttempt bool) (result error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	var key int64
	var locked bool
	// Drop staging on every exit before returning this physical connection.
	defer func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, cleanupErr := conn.Exec(end, `DROP TABLE IF EXISTS pg_temp.tripfolio_snapshot_stage`)
		if locked {
			_, unlockErr := conn.Exec(end, `SELECT pg_advisory_unlock($1)`, key)
			if cleanupErr == nil {
				cleanupErr = unlockErr
			}
		}
		if cleanupErr != nil {
			_ = conn.Conn().Close(end)
			if result == nil {
				result = cleanupErr
			}
		}
		conn.Release()
	}()
	if err = conn.QueryRow(ctx, `SELECT hashtextextended('tripfolio-snapshot:'||$1::text,0)`, id.String()).Scan(&key); err != nil {
		return err
	}
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return errors.New("snapshot build already running")
	}
	var owner uuid.UUID
	err = conn.QueryRow(ctx, `SELECT account_id FROM data_snapshots WHERE id=$1`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	claim, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer claim.Rollback(context.Background())
	epoch, _, err := snapshotAccount(ctx, claim, owner, true)
	if err != nil {
		return err
	}
	meta, err := loadSnapshot(ctx, claim, owner, id)
	if err != nil {
		return err
	}
	if meta.Status != "queued" && meta.Status != "building" {
		return claim.Commit(ctx)
	}
	if meta.SyncEpoch != epoch || !time.Now().Before(meta.ExpiresAt) {
		_, err = claim.Exec(ctx, `UPDATE data_snapshots SET status='invalidated' WHERE id=$1`, id)
		if err != nil {
			return err
		}
		return claim.Commit(ctx)
	}
	var generation int64
	err = claim.QueryRow(ctx, `UPDATE data_snapshots SET status='building',capture_generation=capture_generation+1,error_code=NULL WHERE id=$1 RETURNING capture_generation`, id).Scan(&generation)
	if err != nil {
		return err
	}
	if err = claim.Commit(ctx); err != nil {
		return err
	}
	defer func() {
		if result == nil {
			return
		}
		state, code := "building", "SNAPSHOT_BUILD_FAILED"
		if result.Error() == "SNAPSHOT_SCOPE_CHANGED" {
			state = "invalidated"
			code = "SNAPSHOT_SCOPE_CHANGED"
		} else if lastAttempt {
			state = "failed"
		}
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(end, `UPDATE data_snapshots SET status=$3,error_code=$4 WHERE id=$1 AND capture_generation=$2 AND status='building'`, id, generation, state, code)
	}()
	read, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer read.Rollback(context.Background())
	var high int64
	var captured time.Time
	var capturedEpoch uuid.UUID
	err = read.QueryRow(ctx, `SELECT sync_epoch,last_seq,clock_timestamp() FROM account_sync_state WHERE account_id=$1`, owner).Scan(&capturedEpoch, &high, &captured)
	if err != nil {
		return err
	}
	if capturedEpoch != epoch {
		return errors.New("SNAPSHOT_SCOPE_CHANGED")
	}
	if _, err = read.Exec(ctx, `CREATE TEMP TABLE tripfolio_snapshot_stage (ordinal bigint PRIMARY KEY,entity_type text NOT NULL,entity_id uuid NOT NULL,trip_id uuid,entity_version bigint NOT NULL,payload jsonb NOT NULL,UNIQUE(entity_type,entity_id)) ON COMMIT PRESERVE ROWS`); err != nil {
		return err
	}
	count, err := materializeSnapshot(ctx, read, owner, &meta)
	if err != nil {
		return err
	}
	if err = read.Commit(ctx); err != nil {
		return err
	}
	// A new READ COMMITTED transaction sees any cleanup committed during capture.
	publish, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer publish.Rollback(context.Background())
	nowEpoch, retained, err := snapshotAccount(ctx, publish, owner, true)
	if err != nil {
		return err
	}
	var state string
	var currentGeneration int64
	err = publish.QueryRow(ctx, `SELECT status,capture_generation FROM data_snapshots WHERE id=$1 FOR UPDATE`, id).Scan(&state, &currentGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "building" || currentGeneration != generation {
		return publish.Commit(ctx)
	}
	if nowEpoch != capturedEpoch || high < retained || !time.Now().Before(captured.Add(24*time.Hour)) {
		_, err = publish.Exec(ctx, `UPDATE data_snapshots SET status='invalidated' WHERE id=$1`, id)
		if err != nil {
			return err
		}
		return publish.Commit(ctx)
	}
	if _, err = publish.Exec(ctx, `INSERT INTO snapshot_items(snapshot_id,account_id,ordinal,entity_type,entity_id,trip_id,entity_version,payload) SELECT $1,$2,ordinal,entity_type,entity_id,trip_id,entity_version,payload FROM pg_temp.tripfolio_snapshot_stage`, id, owner); err != nil {
		return err
	}
	if _, err = publish.Exec(ctx, `INSERT INTO snapshot_asset_refs(snapshot_id,account_id,asset_id) SELECT $1,$2,entity_id FROM pg_temp.tripfolio_snapshot_stage WHERE entity_type='asset'`, id, owner); err != nil {
		return err
	}
	selected, _ := json.Marshal(meta.SelectedTripIDs)
	_, err = publish.Exec(ctx, `UPDATE data_snapshots SET status='ready',selected_trip_ids=$2,high_water_seq=$3,item_count=$4,captured_at=$5::timestamptz,expires_at=$5::timestamptz+interval '24 hours' WHERE id=$1`, id, selected, high, count, captured)
	if err != nil {
		return err
	}
	return publish.Commit(ctx)
}
