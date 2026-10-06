package sync

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"tripfolio/server/internal/foundation/apperr"
	syncmodule "tripfolio/server/internal/modules/sync"
)

type ActivationStore struct{ pool *pgxpool.Pool }

func NewActivationStore(pool *pgxpool.Pool) *ActivationStore { return &ActivationStore{pool: pool} }

func (s *ActivationStore) Activate(ctx context.Context, owner, id uuid.UUID, validate func(syncmodule.Snapshot, uuid.UUID, int64, time.Time) error) (syncmodule.Activation, error) {
	var result syncmodule.Activation
	notReady := func() error { return apperr.New(503, "SYNC_NOT_READY", "同步激活条件尚未满足") }
	if s == nil || s.pool == nil || validate == nil || owner == uuid.Nil || id == uuid.Nil {
		return result, notReady()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	// Same order as snapshot publication: account, sync state, snapshot.
	epoch, retained, err := snapshotAccount(ctx, tx, owner, true)
	if err != nil {
		return result, err
	}
	meta, err := scanSnapshot(tx.QueryRow(ctx, `SELECT `+snapshotColumns+` FROM data_snapshots WHERE account_id=$1 AND id=$2 FOR UPDATE`, owner, id))
	if err != nil {
		return result, err
	}
	var count, first, last int64
	if err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(min(ordinal),0),COALESCE(max(ordinal),0) FROM snapshot_items WHERE account_id=$1 AND snapshot_id=$2`, owner, id).Scan(&count, &first, &last); err != nil {
		return result, err
	}
	// Unique positive ordinals plus count/min/max establish no missing rows.
	if (count == 0 && (first != 0 || last != 0)) || (count > 0 && (first != 1 || last != count)) {
		return result, notReady()
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return result, err
	}
	if err = validate(meta, epoch, retained, now); err != nil {
		return result, err
	}
	// Validate count against metadata independently of the caller callback.
	var storedCount int64
	if err = tx.QueryRow(ctx, `SELECT item_count FROM data_snapshots WHERE account_id=$1 AND id=$2`, owner, id).Scan(&storedCount); err != nil {
		return result, err
	}
	if count != storedCount {
		return result, notReady()
	}
	// Recheck expiry after release verification, which can perform artifact I/O.
	var stillFresh bool
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp() < $1::timestamptz`, meta.ExpiresAt).Scan(&stillFresh); err != nil {
		return result, err
	}
	if !stillFresh {
		return result, notReady()
	}
	result.AccountID, result.SyncEpoch = owner, epoch
	err = tx.QueryRow(ctx, `INSERT INTO account_sync_capabilities(account_id,collection_guards_required,v2_enabled_epoch,enabled_at)
VALUES($1,true,$2,clock_timestamp())
ON CONFLICT(account_id) DO UPDATE SET collection_guards_required=true,v2_enabled_epoch=EXCLUDED.v2_enabled_epoch,
enabled_at=CASE WHEN account_sync_capabilities.v2_enabled_epoch=EXCLUDED.v2_enabled_epoch THEN account_sync_capabilities.enabled_at ELSE EXCLUDED.enabled_at END
RETURNING enabled_at`, owner, epoch).Scan(&result.EnabledAt)
	if err != nil {
		return syncmodule.Activation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return syncmodule.Activation{}, err
	}
	return result, nil
}
