package integration

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	syncpg "tripfolio/server/internal/adapters/postgres/sync"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
	syncmodule "tripfolio/server/internal/modules/sync"
)

func admissionState(t *testing.T, f *pushFixture) string {
	t.Helper()
	var state string
	err := f.pool.QueryRow(context.Background(), `SELECT jsonb_build_object(
'trips',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM trips t WHERE account_id=$1),
'members',(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM trip_members m WHERE account_id=$1),
'changes',(SELECT jsonb_agg(to_jsonb(c) ORDER BY seq) FROM sync_changes c WHERE account_id=$1),
'receipts',(SELECT jsonb_agg(to_jsonb(r) ORDER BY operation_id) FROM mutation_receipts r WHERE account_id=$1),
'seq',(SELECT last_seq FROM account_sync_state WHERE account_id=$1),
'jobs',(SELECT jsonb_agg(to_jsonb(j) ORDER BY id) FROM river_job j WHERE args->>'account_id'=$1::uuid::text))::text`, f.owner).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func admissionError(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func disablePushTestAccount(f *pushFixture) {
	f.t.Helper()
	f.sql(`UPDATE account_sync_capabilities SET v2_enabled_epoch=NULL,enabled_at=NULL WHERE account_id=$1`, f.owner)
}

func TestSyncAdmissionPolicyAndNoWrites(t *testing.T) {
	for _, policy := range []string{"absent", "false", "null", "old epoch"} {
		t.Run(policy, func(t *testing.T) {
			f := newPushFixture(t)
			switch policy {
			case "false":
				f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,false)`, f.owner)
			case "null":
				f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, f.owner)
			case "old epoch":
				f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required,v2_enabled_epoch,enabled_at) VALUES($1,true,$2,now())`, f.owner, uuid.New())
			}
			before := admissionState(t, f)
			op, _ := createPush("must not write")
			// Admission precedes even the lookup/execution of an unresolved dependency.
			op.DependsOn = []uuid.UUID{uuid.New()}
			later, _ := createPush("independent must not run")
			expectStatus(t, f.do(request{method: "POST", path: "/sync/push", token: f.token, headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}, body: f.input(op, later)}), 409, "SYNC_NOT_READY")
			if admissionState(t, f) != before {
				t.Fatal("denied push changed business, changes, receipts, sequence or jobs")
			}
			enablePushTestAccount(f)
			op.DependsOn = nil
			financeApplied(t, f.push(op).Results[0])
		})
	}
}

func TestSyncAdmissionReplayAfterDisable(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	op, _ := createPush("original")
	first := f.push(op).Results[0]
	financeApplied(t, first)
	update := pushOp("trip.update", *op.EntityID, versionBase("1"), map[string]any{"name": "current"})
	financeApplied(t, f.push(update).Results[0])
	disablePushTestAccount(f)
	before := admissionState(t, f)
	replay := f.push(op).Results[0]
	if replay.Status != "replayed" || !reflect.DeepEqual(first.Result.References, replay.Result.References) || !reflect.DeepEqual(first.Result.ScopeRevisions, replay.Result.ScopeRevisions) || !reflect.DeepEqual(first.Result.CommitCursor, replay.Result.CommitCursor) {
		t.Fatalf("original receipt facts changed: %+v", replay)
	}
	// The current resource is reloaded, but cannot replace original committed facts.
	data, err := json.Marshal(replay.Result.Data)
	if err != nil || !strings.Contains(string(data), `"name":"current"`) {
		t.Fatalf("reload: %s %v", data, err)
	}
	fresh, _ := createPush("new id")
	_, err = f.svc.Push(context.Background(), f.a, "2", f.input(fresh))
	admissionError(t, err, "SYNC_NOT_READY")
	changed := op
	changed.Payload = []byte(strings.Replace(string(op.Payload), "original", "changed", 1))
	financeCode(t, f.push(changed).Results[0], "IDEMPOTENCY_CONFLICT")
	if admissionState(t, f) != before {
		t.Fatal("replay or rejection performed a new write")
	}
	for _, corrupt := range []string{"missing", "different epoch"} {
		t.Run(corrupt, func(t *testing.T) {
			if corrupt == "missing" {
				f.sql(`UPDATE mutation_receipts SET result=result-'sync' WHERE account_id=$1 AND operation_id=$2`, f.owner, op.OperationID)
			} else {
				f.sql(`UPDATE mutation_receipts SET result=jsonb_set(result,'{sync}',jsonb_build_object('epoch',$3::text)) WHERE account_id=$1 AND operation_id=$2`, f.owner, op.OperationID, uuid.NewString())
			}
			before := admissionState(t, f)
			financeCode(t, f.push(op).Results[0], "SYNC_RECEIPT_UNAVAILABLE")
			if admissionState(t, f) != before {
				t.Fatal("unverifiable receipt reran operation")
			}
		})
	}
}

type admissionCountingRepository struct {
	syncmodule.PushRepository
	calls int
}

func (r *admissionCountingRepository) Execute(ctx context.Context, a actor.Actor, epoch uuid.UUID, op syncmodule.PreparedOperation) (write.Result, error) {
	r.calls++
	return r.PushRepository.Execute(ctx, a, epoch, op)
}

func TestSyncAdmissionMidBatchStops(t *testing.T) {
	for _, invalidation := range []string{"disable", "session", "account"} {
		t.Run(invalidation, func(t *testing.T) {
			f := newPushFixture(t)
			enablePushTestAccount(f)
			first, _ := createPush("committed")
			second, _ := createPush("denied")
			third, _ := createPush("never attempted")
			var committedState string
			repo := &admissionCountingRepository{PushRepository: &afterPush{
				repo: syncpg.NewPushStore(pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger())),
				after: func() {
					switch invalidation {
					case "disable":
						disablePushTestAccount(f)
					case "session":
						f.sql(`UPDATE account_sessions SET revoked_at=now() WHERE id=$1`, f.a.SessionID)
					case "account":
						f.sql(`UPDATE accounts SET status='banned' WHERE id=$1`, f.owner)
					}
					committedState = admissionState(t, f)
				},
			}}
			f.svc.WithPush(repo)
			out, err := f.svc.Push(context.Background(), f.a, "2", f.input(first, second, third))
			want := map[string]string{"disable": "SYNC_NOT_READY", "session": "SESSION_EXPIRED", "account": "ACCOUNT_BANNED"}[invalidation]
			admissionError(t, err, want)
			if len(out.Results) != 0 || repo.calls != 2 || committedState == "" || admissionState(t, f) != committedState {
				t.Fatalf("batch continued or lost committed first operation: calls=%d out=%+v", repo.calls, out)
			}
		})
	}
}

func TestSyncAdmissionIdentityBeforeReplay(t *testing.T) {
	for _, invalidation := range []string{"revoked", "expired", "account", "web", "missing", "foreign", "epoch"} {
		t.Run(invalidation, func(t *testing.T) {
			f := newPushFixture(t)
			enablePushTestAccount(f)
			op, _ := createPush("identity")
			financeApplied(t, f.push(op).Results[0])
			disablePushTestAccount(f)
			req := write.Request{AccountID: f.owner, OperationID: op.OperationID}
			var fingerprint []byte
			if err := f.pool.QueryRow(context.Background(), `SELECT request_hash FROM mutation_receipts WHERE account_id=$1 AND operation_id=$2`, f.owner, op.OperationID).Scan(&fingerprint); err != nil {
				t.Fatal(err)
			}
			copy(req.Fingerprint[:], fingerprint)
			a := f.a
			want := "SESSION_EXPIRED"
			switch invalidation {
			case "revoked":
				f.sql(`UPDATE account_sessions SET revoked_at=now() WHERE id=$1`, a.SessionID)
			case "expired":
				f.sql(`UPDATE account_sessions SET expires_at=now()-interval '1 minute' WHERE id=$1`, a.SessionID)
			case "account":
				f.sql(`UPDATE accounts SET status='banned' WHERE id=$1`, f.owner)
				want = "ACCOUNT_BANNED"
			case "web":
				a.ClientKind = actor.ClientWeb
			case "foreign":
				a.AccountID = uuid.New()
			case "epoch":
				f.sql(`UPDATE account_sync_state SET sync_epoch=$2 WHERE account_id=$1`, f.owner, uuid.New())
				want = "SYNC_EPOCH_MISMATCH"
			}
			ctx := context.Background()
			if invalidation != "missing" {
				ctx = actor.WithActor(ctx, a)
			}
			before := admissionState(t, f)
			called := false
			_, err := pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger()).RunSync(ctx, req, pgcore.SyncWriteOptions{Epoch: f.epoch}, func(context.Context, *pgcore.TxScope) error { called = true; return nil }, func(context.Context, *pgcore.TxScope) (any, error) { called = true; return nil, nil })
			admissionError(t, err, want)
			if called || admissionState(t, f) != before {
				t.Fatal("invalid identity reached business or receipt reload")
			}
		})
	}
}

func TestSyncAdmissionRestoreEpoch(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	op, _ := createPush("before restore")
	financeApplied(t, f.push(op).Results[0])
	oldEpoch := f.epoch
	f.epoch = uuid.New()
	// Model the already-verified restore postconditions, without running restore here.
	f.sql(`UPDATE account_sync_state SET sync_epoch=$2 WHERE account_id=$1`, f.owner, f.epoch)
	disablePushTestAccount(f)
	f.sql(`UPDATE account_sessions SET revoked_at=now() WHERE account_id=$1`, f.owner)
	f.a.SessionID = uuid.New()
	// A newly authenticated native session still has no admission for the new epoch.
	f.sql(`INSERT INTO account_sessions(id,account_id,client_kind,device_id,refresh_token_hash,expires_at) VALUES($1,$2,'harmony',$3,decode(repeat('00',32),'hex'),now()+interval '1 hour')`, f.a.SessionID, f.owner, uuid.New())
	before := admissionState(t, f)
	fresh, _ := createPush("after restore")
	for _, operation := range []syncmodule.Operation{op, fresh} {
		old := f.input(operation)
		old.SyncEpoch = oldEpoch
		_, err := f.svc.Push(context.Background(), f.a, "2", old)
		admissionError(t, err, "SYNC_EPOCH_MISMATCH")
	}
	_, err := f.svc.Push(context.Background(), f.a, "2", f.input(fresh))
	admissionError(t, err, "SYNC_NOT_READY")
	out, err := f.svc.Push(context.Background(), f.a, "2", f.input(op))
	if err != nil {
		t.Fatal(err)
	}
	financeCode(t, out.Results[0], "SYNC_RECEIPT_UNAVAILABLE")
	if admissionState(t, f) != before {
		t.Fatal("restore state bypassed admission")
	}
	var sticky bool
	if err := f.pool.QueryRow(context.Background(), `SELECT collection_guards_required FROM account_sync_capabilities WHERE account_id=$1`, f.owner).Scan(&sticky); err != nil || !sticky {
		t.Fatalf("sticky protection lost: %v", err)
	}
}

func TestSyncAdmissionAccountLock(t *testing.T) {
	for _, enable := range []bool{true, false} {
		name := "disable"
		if enable {
			name = "enable"
		}
		t.Run(name, func(t *testing.T) {
			f := newPushFixture(t)
			if !enable {
				enablePushTestAccount(f)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			var locked uuid.UUID
			if err = tx.QueryRow(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, f.owner).Scan(&locked); err != nil {
				t.Fatal(err)
			}
			if enable {
				_, err = tx.Exec(ctx, `INSERT INTO account_sync_capabilities(account_id,collection_guards_required,v2_enabled_epoch,enabled_at) VALUES($1,true,$2,now())`, f.owner, f.epoch)
			} else {
				_, err = tx.Exec(ctx, `UPDATE account_sync_capabilities SET v2_enabled_epoch=NULL,enabled_at=NULL WHERE account_id=$1`, f.owner)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := admissionState(t, f)
			op, _ := createPush(name)
			type outcome struct {
				out syncmodule.PushOutput
				err error
			}
			done := make(chan outcome, 1)
			go func() { out, err := f.svc.Push(ctx, f.a, "2", f.input(op)); done <- outcome{out, err} }()
			// Observe an actual PostgreSQL lock waiter, not a timing-only sleep assertion.
			for {
				var blocked bool
				if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, int32(tx.Conn().PgConn().PID())).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case got := <-done:
					t.Fatalf("push escaped account lock: %+v", got)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-done:
				if enable {
					if got.err != nil {
						t.Fatal(got.err)
					}
					financeApplied(t, got.out.Results[0])
				} else {
					admissionError(t, got.err, "SYNC_NOT_READY")
					if admissionState(t, f) != before {
						t.Fatal("disable race wrote data")
					}
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestSyncAdmissionMissingPolicyTable(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	ctx := context.Background()
	schema := pgx.Identifier{"admission_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	f.sql(`CREATE SCHEMA ` + schema)
	t.Cleanup(func() { f.sql(`DROP SCHEMA ` + schema + ` CASCADE`) })
	// Only views into this test database; public policy storage remains untouched.
	for _, table := range []string{"accounts", "account_sync_state", "account_sessions", "mutation_receipts"} {
		f.sql(`CREATE VIEW ` + schema + `.` + table + ` AS SELECT * FROM public.` + table)
	}
	cfg, err := pgxpool.ParseConfig(f.rawURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",pg_catalog"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	before := admissionState(t, f)
	called := false
	_, err = pgcore.NewWriter(pool, nil, clock.Real{}, quietLogger()).RunSync(actor.WithActor(ctx, f.a), write.Request{AccountID: f.owner, OperationID: uuid.New()}, pgcore.SyncWriteOptions{Epoch: f.epoch}, func(context.Context, *pgcore.TxScope) error { called = true; return nil }, nil)
	admissionError(t, err, "INTERNAL_ERROR")
	if called || admissionState(t, f) != before {
		t.Fatal("missing policy table downgraded admission")
	}
}
