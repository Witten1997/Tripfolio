package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"tripfolio/server/db"
)

// The store only persists executor state; dump, archive validation, rendering,
// advisory-lock handoff and the complete psql restore transaction are production code.
type restoreTestStore struct {
	pool *pgxpool.Pool
	job  RestoreJob
}

func (s *restoreTestStore) PendingRestore(ctx context.Context) (*RestoreJob, error) {
	job := s.job
	err := s.pool.QueryRow(ctx, `SELECT state FROM tripfolio_restore.jobs WHERE id=$1`, job.ID).Scan(&job.State)
	return &job, err
}

func (s *restoreTestStore) RestoreProgress(ctx context.Context, id uuid.UUID, state, code string) error {
	_, err := s.pool.Exec(ctx, `UPDATE tripfolio_restore.jobs SET state=$2,error_code=$3 WHERE id=$1 AND state NOT IN ('succeeded','failed')`, id, state, code)
	return err
}

func (*restoreTestStore) RestoreStatus(context.Context, uuid.UUID, []byte) (RestoreJob, error) {
	return RestoreJob{}, fmt.Errorf("status is not used by the executor")
}

func (s *restoreTestStore) ResetConnections() { s.pool.Reset() }

func TestRestoreSyncEpochTransaction(t *testing.T) {
	rawURL := os.Getenv("TRIPFOLIO_RESTORE_TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("TRIPFOLIO_RESTORE_TEST_DATABASE_URL is not set")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Path != "/h07_restore_test" {
		t.Fatal("restore verification requires the dedicated h07_restore_test database")
	}
	for _, tool := range []string{"pg_dump", "pg_restore", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required production restore tool %s: %v", tool, err)
		}
	}
	for _, scenario := range []struct {
		version int64
		fail    bool
	}{{22, false}, {25, false}, {26, false}, {27, false}, {27, true}} {
		t.Run(fmt.Sprintf("schema_%d_rollback_%t", scenario.version, scenario.fail), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			pool, err := pgxpool.New(ctx, rawURL)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			execSQL := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			execSQL(`DROP SCHEMA IF EXISTS tripfolio_restore CASCADE; DROP SCHEMA IF EXISTS tripfolio_private CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			river, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: logger})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := river.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
				t.Fatal(err)
			}
			migrations, err := fs.Sub(db.Migrations, db.MigrationsDir)
			if err != nil {
				t.Fatal(err)
			}
			provider, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations)
			if err != nil {
				t.Fatal(err)
			}
			defer provider.Close()
			if _, err := provider.UpTo(ctx, scenario.version); err != nil {
				t.Fatal(err)
			}
			accounts := []uuid.UUID{uuid.New(), uuid.New()}
			snapshotID := uuid.New()
			for _, id := range accounts {
				execSQL(`INSERT INTO accounts(id,email,email_key,email_verified_at,password_hash,nickname) VALUES($1,$2,$2,now(),'test-only','archive')`, id, id.String()+"@example.test")
				execSQL(`INSERT INTO account_sync_state(account_id) VALUES($1)`, id)
			}
			execSQL(`INSERT INTO admin_principals(account_id,reason) VALUES($1,'restore integration')`, accounts[0])
			tripID := uuid.New()
			execSQL(`INSERT INTO trips(id,account_id,name,start_date,end_date,timezone) VALUES($1,$2,'archive trip','2026-01-01','2026-01-04','Asia/Shanghai')`, tripID, accounts[0])
			items := []uuid.UUID{uuid.New(), uuid.New()}
			for i, id := range items {
				execSQL(`INSERT INTO packing_items(id,account_id,trip_id,name,category,created_at,updated_at,deleted_at) VALUES($1,$2,$3,'archive packing','documents',timestamp '2026-01-01'+$4*interval '1 day',timestamp '2026-01-01'+$4*interval '1 day',CASE WHEN $4=1 THEN now() END)`, id, accounts[0], tripID, i)
				execSQL(`INSERT INTO todo_items(id,account_id,trip_id,title,due_on,deleted_at) VALUES($1,$2,$3,'archive todo',date '2026-01-01'+$4::int,CASE WHEN $4=1 THEN now() END)`, id, accounts[0], tripID, i)
				if scenario.version >= 27 {
					execSQL(`UPDATE packing_items SET sort_order=$2 WHERE id=$1`, id, 41+i)
					execSQL(`UPDATE todo_items SET sort_order=$2 WHERE id=$1`, id, 51+i)
				}
			}
			if scenario.version >= 27 {
				execSQL(`INSERT INTO data_snapshots(id,account_id,purpose,selected_trip_ids,status,high_water_seq,captured_at,expires_at,sync_epoch) SELECT $1,$2,'baseline','[]','ready',0,now(),now()+interval '1 day',sync_epoch FROM account_sync_state WHERE account_id=$2`, snapshotID, accounts[0])
			} else {
				execSQL(`INSERT INTO data_snapshots(id,account_id,purpose,selected_trip_ids,status,high_water_seq,captured_at,expires_at) VALUES($1,$2,'baseline','[]','ready',0,now(),now()+interval '1 day')`, snapshotID, accounts[0])
			}
			epochs := func() map[uuid.UUID]uuid.UUID {
				t.Helper()
				rows, err := pool.Query(ctx, `SELECT account_id,sync_epoch FROM account_sync_state`)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				result := make(map[uuid.UUID]uuid.UUID)
				for rows.Next() {
					var id, epoch uuid.UUID
					if err := rows.Scan(&id, &epoch); err != nil {
						t.Fatal(err)
					}
					result[id] = epoch
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				return result
			}
			var archivedEpochs map[uuid.UUID]uuid.UUID
			if scenario.version >= 26 {
				archivedEpochs = epochs()
			}
			var databaseID string
			if err := pool.QueryRow(ctx, `SELECT database_id::text FROM admin_backup_settings WHERE id=1`).Scan(&databaseID); err != nil {
				t.Fatal(err)
			}
			svc := New(nil, Options{Password: "h07-fixture-password-only", DatabaseURL: rawURL, Directory: t.TempDir(), MaxBytes: 64 << 20, Timeout: time.Minute})
			backupID := uuid.New()
			archive := filepath.Join(svc.options.Directory, backupID.String()+".dump.age")
			manifest, err := svc.dump(ctx, backupID, archive, databaseID)
			if err != nil {
				t.Fatalf("production dump: %v", err)
			}
			if manifest.GooseVersion != scenario.version {
				t.Fatalf("archive source version: %d", manifest.GooseVersion)
			}
			if _, err := provider.Up(ctx); err != nil {
				t.Fatal(err)
			}
			// The destination now differs from the archive. A rollback must retain
			// these live values; a commit must restore the archive and rotate epochs.
			execSQL(`UPDATE accounts SET nickname='live'; UPDATE account_sync_state SET sync_epoch=gen_random_uuid()`)
			execSQL(`UPDATE packing_items SET sort_order=97,name='live packing'; UPDATE todo_items SET sort_order=98,title='live todo'`)
			liveEpochs := epochs()
			if len(liveEpochs) != 2 || liveEpochs[accounts[0]] == uuid.Nil || liveEpochs[accounts[0]] == liveEpochs[accounts[1]] {
				t.Fatal("migration must initialize distinct non-null account epochs")
			}
			execSQL(`INSERT INTO account_sessions(id,account_id,client_kind,device_id,refresh_token_hash,expires_at) VALUES($1,$2,'harmony',$3,decode(repeat('00',32),'hex'),now()+interval '1 day')`, uuid.New(), accounts[0], uuid.New())
			execSQL(`UPDATE admin_backup_settings SET config=jsonb_set(config,'{enabled}','true')`)
			webdav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || filepath.Base(r.URL.Path) != backupID.String()+".dump.age" {
					http.NotFound(w, r)
					return
				}
				http.ServeFile(w, r, archive)
			}))
			defer webdav.Close()
			secret, err := svc.Seal("fixture-webdav")
			if err != nil {
				t.Fatal(err)
			}
			job := RestoreJob{ID: uuid.New(), BackupID: backupID, State: "queued", Manifest: manifest,
				Config: Config{DatabaseID: databaseID, URL: webdav.URL, Destination: uuid.NewString(), Secret: secret}}
			cfgJSON, _ := json.Marshal(job.Config)
			manifestJSON, _ := json.Marshal(manifest)
			execSQL(`INSERT INTO tripfolio_restore.jobs(id,backup_id,actor_id,state,token_hash,secret,config,manifest) VALUES($1,$2,$3,'queued',decode(repeat('00',32),'hex'),'',$4,$5)`, job.ID, backupID, accounts[0], cfgJSON, manifestJSON)
			if scenario.fail {
				// The control schema is outside the archive. Fail after epoch rotation,
				// snapshot invalidation and the success update, before transaction commit.
				execSQL(`CREATE FUNCTION tripfolio_restore.fail_epoch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'H07 late transaction failure'; END $$;
CREATE TRIGGER h07_fail_epoch BEFORE UPDATE ON tripfolio_restore.control FOR EACH ROW EXECUTE FUNCTION tripfolio_restore.fail_epoch()`)
			}
			svc.WithRestores(&restoreTestStore{pool: pool, job: job}, rawURL)
			svc.restoreNext(ctx, false)
			var state, errorCode, nickname, snapshotStatus string
			var controlEpoch, sessionCount, version, auditCount int64
			var enabled bool
			if err := pool.QueryRow(ctx, `SELECT state,error_code FROM tripfolio_restore.jobs WHERE id=$1`, job.ID).Scan(&state, &errorCode); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT nickname FROM accounts WHERE id=$1`, accounts[0]).Scan(&nickname); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT status FROM data_snapshots WHERE id=$1`, snapshotID).Scan(&snapshotStatus); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT (SELECT epoch FROM tripfolio_restore.control),(SELECT count(*) FROM account_sessions),(SELECT max(version_id) FROM goose_db_version WHERE is_applied),(SELECT count(*) FROM admin_audit_events WHERE action='backup.restore.completed'),(SELECT (config->>'enabled')::boolean FROM admin_backup_settings WHERE id=1)`).Scan(&controlEpoch, &sessionCount, &version, &auditCount, &enabled); err != nil {
				t.Fatal(err)
			}
			afterEpochs := epochs()
			for i, id := range items {
				var packingOrder, todoOrder int
				var packingName, todoTitle string
				var packingDeleted, todoDeleted bool
				if err := pool.QueryRow(ctx, `SELECT p.sort_order,t.sort_order,p.name,t.title,p.deleted_at IS NOT NULL,t.deleted_at IS NOT NULL FROM packing_items p JOIN todo_items t ON t.id=p.id WHERE p.id=$1`, id).Scan(&packingOrder, &todoOrder, &packingName, &todoTitle, &packingDeleted, &todoDeleted); err != nil {
					t.Fatal(err)
				}
				wantPacking, wantTodo, wantName, wantTitle := i, i, "archive packing", "archive todo"
				if scenario.version >= 27 {
					wantPacking, wantTodo = 41+i, 51+i
				}
				if scenario.fail {
					wantPacking, wantTodo, wantName, wantTitle = 97, 98, "live packing", "live todo"
				}
				if packingOrder != wantPacking || todoOrder != wantTodo || packingName != wantName || todoTitle != wantTitle || packingDeleted != (i == 1) || todoDeleted != (i == 1) {
					t.Fatalf("restore packing/todo mismatch: order=%d/%d expected=%d/%d names=%s/%s deleted=%t/%t", packingOrder, todoOrder, wantPacking, wantTodo, packingName, todoTitle, packingDeleted, todoDeleted)
				}
			}
			if version != 27 || len(afterEpochs) != len(accounts) {
				t.Fatalf("schema/accounts not preserved: %d %+v", version, afterEpochs)
			}
			if scenario.fail {
				if state != "failed" || errorCode != "RESTORE_FAILED" || nickname != "live" || snapshotStatus != "ready" || controlEpoch != 0 || sessionCount != 1 || auditCount != 0 || !enabled {
					t.Fatalf("late failure did not roll back: state=%s code=%s nickname=%s snapshot=%s control=%d sessions=%d audits=%d enabled=%t", state, errorCode, nickname, snapshotStatus, controlEpoch, sessionCount, auditCount, enabled)
				}
				for _, id := range accounts {
					if afterEpochs[id] != liveEpochs[id] {
						t.Fatal("rollback lost the live account epoch")
					}
				}
			} else {
				if state != "succeeded" || errorCode != "" || nickname != "archive" || snapshotStatus != "invalidated" || controlEpoch != 1 || sessionCount != 0 || auditCount != 1 || enabled {
					t.Fatalf("restore did not commit atomically: state=%s code=%s nickname=%s snapshot=%s control=%d sessions=%d audits=%d enabled=%t", state, errorCode, nickname, snapshotStatus, controlEpoch, sessionCount, auditCount, enabled)
				}
				for _, id := range accounts {
					if afterEpochs[id] == uuid.Nil || afterEpochs[id] == liveEpochs[id] || afterEpochs[id] == archivedEpochs[id] {
						t.Fatal("restore reused a live/archive epoch")
					}
				}
			}
			t.Logf("production dump/restore schema %d -> 27, rollback=%t: epoch/snapshot/session/control/audit assertions passed", scenario.version, scenario.fail)
		})
	}
}
