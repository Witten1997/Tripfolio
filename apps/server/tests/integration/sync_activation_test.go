package integration

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	syncpg "tripfolio/server/internal/adapters/postgres/sync"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/paging"
	syncmodule "tripfolio/server/internal/modules/sync"
)

// This test double approves only the isolated database fixture. Production
// release verification and the real CLI are a separate integration dependency.
type activationFixtureRelease struct{ err error }

func (v activationFixtureRelease) Verify(context.Context) error { return v.err }

func activationFixtureCodec(t *testing.T) paging.Codec {
	t.Helper()
	keys, err := security.ParseKeyring("snapshot-test=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	return security.NewCursorCodec(keys)
}

func TestSyncActivationCore(t *testing.T) {
	f := newSnapshotFixture(t)
	ctx := context.Background()
	f.newTrip("activation baseline")
	codec := activationFixtureCodec(t)
	service := syncmodule.NewActivationService(syncpg.NewActivationStore(f.pool), codec, activationFixtureRelease{})
	baseline := func() (uuid.UUID, string) {
		meta := f.snapshot("baseline", []uuid.UUID{})
		f.build(meta.ID)
		_, page := f.pages(meta.ID)
		if page.BaselineCursor == nil {
			t.Fatal("missing completion proof")
		}
		return meta.ID, *page.BaselineCursor
	}
	assertDisabled := func() {
		t.Helper()
		var enabled bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_sync_capabilities WHERE account_id=$1 AND v2_enabled_epoch IS NOT NULL)`, f.owner).Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled {
			t.Fatal("rejected activation wrote policy")
		}
	}
	for _, tc := range []struct{ name, mutation string }{
		{"invalidated", `UPDATE data_snapshots SET status='invalidated' WHERE id=$1`},
		{"not_baseline", `UPDATE data_snapshots SET purpose='trip_reload' WHERE id=$1`},
		{"schema", `UPDATE data_snapshots SET schema_version=1 WHERE id=$1`},
		{"expired", `UPDATE data_snapshots SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`},
		{"changed_count", `UPDATE data_snapshots SET item_count=item_count+1 WHERE id=$1`},
		{"missing_item", `DELETE FROM snapshot_items WHERE snapshot_id=$1 AND ordinal=1`},
		{"changed_high", `UPDATE data_snapshots SET high_water_seq=high_water_seq+1 WHERE id=$1`},
		{"changed_epoch", `UPDATE data_snapshots SET sync_epoch=gen_random_uuid() WHERE id=$1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, proof := baseline()
			f.sql(tc.mutation, id)
			if _, err := service.Activate(ctx, f.owner, id, proof); err == nil {
				t.Fatal("invalid baseline accepted")
			}
			assertDisabled()
		})
	}
	id, proof := baseline()
	ordinary, err := f.svc.BaselineCursor(f.owner, f.epoch, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		owner, id uuid.UUID
		proof     string
	}{
		{"ordinary", f.owner, id, ordinary},
		{"forged", f.owner, id, proof + "!"},
		{"other_owner", uuid.New(), id, proof},
		{"other_snapshot", f.owner, uuid.New(), proof},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.Activate(ctx, tc.owner, tc.id, tc.proof); err == nil {
				t.Fatal("invalid proof accepted")
			}
			assertDisabled()
		})
	}
	for _, verifier := range []syncmodule.ReleaseVerifier{nil, activationFixtureRelease{err: errors.New("not accepted")}} {
		denied := syncmodule.NewActivationService(syncpg.NewActivationStore(f.pool), codec, verifier)
		if _, err := denied.Activate(ctx, f.owner, id, proof); err == nil {
			t.Fatal("missing release gate accepted")
		}
		assertDisabled()
	}
	// The activation waits for the same account lock as writers/publication.
	lock, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, f.owner); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = service.Activate(blocked, f.owner, id, proof)
	cancel()
	if err == nil {
		t.Fatal("activation bypassed account lock")
	}
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertDisabled()
	first, err := service.Activate(ctx, f.owner, id, proof)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Activate(ctx, f.owner, id, proof)
	if err != nil || first.AccountID != f.owner || first.SyncEpoch != f.epoch || first.EnabledAt.IsZero() || !first.EnabledAt.Equal(second.EnabledAt) {
		t.Fatalf("repeat activation: first=%+v second=%+v err=%v", first, second, err)
	}
	var sticky bool
	var enabledEpoch uuid.UUID
	if err = f.pool.QueryRow(ctx, `SELECT collection_guards_required,v2_enabled_epoch FROM account_sync_capabilities WHERE account_id=$1`, f.owner).Scan(&sticky, &enabledEpoch); err != nil {
		t.Fatal(err)
	}
	if !sticky || enabledEpoch != f.epoch {
		t.Fatal("activation policy not atomic")
	}
	// Simulate the restore epoch/invalidated-snapshot transition only; this is
	// not a test of backup restore execution, nor permission to run it live.
	newEpoch := uuid.New()
	f.sql(`UPDATE account_sync_state SET sync_epoch=$2 WHERE account_id=$1`, f.owner, newEpoch)
	f.sql(`UPDATE data_snapshots SET status='invalidated' WHERE account_id=$1`, f.owner)
	if _, err = service.Activate(ctx, f.owner, id, proof); err == nil {
		t.Fatal("old proof survived epoch rotation")
	}
	if err = f.pool.QueryRow(ctx, `SELECT collection_guards_required,v2_enabled_epoch FROM account_sync_capabilities WHERE account_id=$1`, f.owner).Scan(&sticky, &enabledEpoch); err != nil {
		t.Fatal(err)
	}
	if !sticky || enabledEpoch != f.epoch || enabledEpoch == newEpoch {
		t.Fatal("rejected activation changed sticky or epoch")
	}
}

func TestSyncActivationRetention(t *testing.T) {
	f := newSnapshotFixture(t)
	ctx := context.Background()
	meta := f.snapshot("baseline", []uuid.UUID{})
	f.build(meta.ID)
	_, page := f.pages(meta.ID)
	if page.BaselineCursor == nil {
		t.Fatal("missing proof")
	}
	f.sql(`UPDATE account_sync_state SET last_seq=last_seq+1,retained_after_seq=last_seq+1 WHERE account_id=$1`, f.owner)
	svc := syncmodule.NewActivationService(syncpg.NewActivationStore(f.pool), activationFixtureCodec(t), activationFixtureRelease{})
	if _, err := svc.Activate(ctx, f.owner, meta.ID, *page.BaselineCursor); err == nil {
		t.Fatal("retained baseline accepted")
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM account_sync_capabilities WHERE account_id=$1`, f.owner).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retention denial changed policy: %d %v", count, err)
	}
}
