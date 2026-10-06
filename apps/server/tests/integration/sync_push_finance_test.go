package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	syncmodule "tripfolio/server/internal/modules/sync"
)

func financeOp(entity, action string, trip, id uuid.UUID, base *syncmodule.BaseReference, payload any, deps ...uuid.UUID) syncmodule.Operation {
	op := itemOp(entity, action, trip, id, base, payload, deps...)
	if entity == "expense_category" {
		op.TripID = nil
	}
	return op
}
func financeGuard(f *pushFixture, kind string, scope uuid.UUID) syncmodule.GuardReference {
	f.t.Helper()
	table := "trip_members"
	query := `SELECT id,version FROM trip_members WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id`
	args := []any{f.owner, scope}
	if kind == "categories" {
		table = "expense_categories"
		query = `SELECT id,version FROM expense_categories WHERE account_id=$1 AND deleted_at IS NULL ORDER BY id`
		args = []any{f.owner}
	}
	rows, err := f.pool.Query(context.Background(), query, args...)
	if err != nil {
		f.t.Fatal(table, err)
	}
	defer rows.Close()
	raw := fmt.Sprintf("%s\n%s\n%s\n", f.epoch, kind, scope)
	for rows.Next() {
		var id uuid.UUID
		var version int64
		if err = rows.Scan(&id, &version); err != nil {
			f.t.Fatal(err)
		}
		raw += fmt.Sprintf("%s:%d\n", id, version)
	}
	if err = rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	revision := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(raw)))
	return syncmodule.GuardReference{Kind: kind, ScopeID: scope.String(), Revision: &revision}
}
func withFinanceGuard(op syncmodule.Operation, g syncmodule.GuardReference) syncmodule.Operation {
	op.Guards = []syncmodule.GuardReference{g}
	return op
}
func financeStart(t *testing.T, f *pushFixture) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	op, self := createPush("finance")
	financeApplied(t, f.push(op).Results[0])
	cat := financeOp("expense_category", "create", uuid.Nil, uuid.New(), nil, map[string]any{"name": "custom"})
	financeApplied(t, f.push(cat).Results[0])
	return *op.EntityID, self, *cat.EntityID
}
func memberInput(id uuid.UUID, name, percent string) map[string]any {
	return map[string]any{"id": id, "name": name, "share_percent": percent}
}
func replaceMembers(f *pushFixture, trip uuid.UUID, inputs ...map[string]any) syncmodule.Operation {
	return withFinanceGuard(financeOp("trip_member", "replace", trip, trip, nil, map[string]any{"members": inputs}), financeGuard(f, "members", trip))
}
func ledgerCreate(f *pushFixture, trip, category, self uuid.UUID, amount string, participants ...uuid.UUID) syncmodule.Operation {
	return withFinanceGuard(financeOp("ledger_entry", "create", trip, uuid.New(), nil, map[string]any{"kind": "expense", "amount": amount, "currency_code": "CNY", "category_id": category, "occurred_on": "2026-10-01", "payer_member_id": self, "split_mode": "ratio", "participant_member_ids": participants}), financeGuard(f, "members", trip))
}
func financeCode(t *testing.T, r syncmodule.PushResult, code string) {
	t.Helper()
	if r.Error == nil || r.Error.Code != code {
		raw, _ := json.Marshal(r)
		t.Fatalf("want %s: %s", code, raw)
	}
}
func financePayload(op syncmodule.Operation, changes map[string]any) syncmodule.Operation {
	var p map[string]any
	_ = json.Unmarshal(op.Payload, &p)
	for k, v := range changes {
		p[k] = v
	}
	op.Payload, _ = json.Marshal(p)
	return op
}
func financeCheck(f *pushFixture, sql string, args ...any) {
	f.t.Helper()
	var ok bool
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&ok); err != nil || !ok {
		f.t.Fatalf("SQL invariant failed: %v %t", err, ok)
	}
}

func TestSyncPushFinanceLifecycle(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	trip, self, cat := financeStart(t, f)
	friend := uuid.New()
	members := replaceMembers(f, trip, memberInput(self, "Me", "60"), memberInput(friend, "Friend", "40"))
	mr := f.push(members).Results[0]
	financeApplied(t, mr)
	create := ledgerCreate(f, trip, cat, self, "100", self, friend)
	create.DependsOn = []uuid.UUID{members.OperationID}
	create.Guards = []syncmodule.GuardReference{{Kind: "members", ScopeID: trip.String(), OperationID: &members.OperationID}}
	out := f.push(create).Results[0]
	financeApplied(t, out)
	if itemVersion(t, out, *create.EntityID) != "1" || resultVersion(t, out, trip) != "2" {
		t.Fatal("creation refs")
	}
	// True no-op retains original versions, references, and collection facts.
	noop := financeOp("ledger_entry", "update", trip, *create.EntityID, refBase(create.OperationID), map[string]any{"amount": "100.00"}, create.OperationID)
	financeCode(t, f.push(noop).Results[0], "COLLECTION_BASE_REQUIRED")
	noop = withFinanceGuard(noop, financeGuard(f, "members", trip))
	nr := f.push(noop).Results[0]
	financeApplied(t, nr)
	if nr.Result.CommitCursor != nil || itemVersion(t, nr, *create.EntityID) != "1" {
		t.Fatal("ledger no-op")
	}
	changed := replaceMembers(f, trip, memberInput(self, "Me", "80"), memberInput(friend, "Friend", "20"))
	financeApplied(t, f.push(changed).Results[0])
	financeCheck(f, `SELECT version=1 AND personal_amount=60 FROM ledger_entries WHERE id=$1`, *create.EntityID)
	missing := financeOp("ledger_entry", "update", trip, *create.EntityID, versionBase("1"), map[string]any{"currency_code": "CNY"})
	financeCode(t, f.push(missing).Results[0], "COLLECTION_BASE_REQUIRED")
	stale := noop
	stale.OperationID = uuid.New()
	financeCode(t, f.push(stale).Results[0], "COLLECTION_CONFLICT")
	replay := f.push(noop).Results[0]
	if replay.Status != "replayed" || itemRevision(t, replay) != itemRevision(t, nr) || itemVersion(t, replay, *create.EntityID) != "1" {
		t.Fatal("no-op replay facts drift")
	}
	recalc := withFinanceGuard(financeOp("ledger_entry", "update", trip, *create.EntityID, versionBase("1"), map[string]any{"amount": "100"}), financeGuard(f, "members", trip))
	financeApplied(t, f.push(recalc).Results[0])
	financeCheck(f, `SELECT version=2 AND personal_amount=80 FROM ledger_entries WHERE id=$1`, *create.EntityID)
	notes := financeOp("ledger_entry", "update", trip, *create.EntityID, versionBase("2"), map[string]any{"notes": "memo"})
	financeApplied(t, f.push(notes).Results[0])
	replay = f.push(create).Results[0]
	if replay.Status != "replayed" || itemVersion(t, replay, *create.EntityID) != "1" || resultVersion(t, replay, trip) != "2" {
		t.Fatal("create facts drift")
	}
	catNoop := financeOp("expense_category", "update", uuid.Nil, cat, versionBase("1"), map[string]any{"name": "custom", "icon": nil})
	cn := f.push(catNoop).Results[0]
	financeApplied(t, cn)
	if cn.Result.CommitCursor != nil || itemVersion(t, cn, cat) != "1" {
		t.Fatal("category noop")
	}
	financeCode(t, f.push(financeOp("expense_category", "delete", uuid.Nil, cat, versionBase("1"), map[string]any{})).Results[0], "CATEGORY_IN_USE")
	// Current collection no-op retains all original member references.
	same := replaceMembers(f, trip, memberInput(self, "Me", "80"), memberInput(friend, "Friend", "20"))
	sameResult := f.push(same).Results[0]
	financeApplied(t, sameResult)
	if sameResult.Result.CommitCursor != nil || len(sameResult.Result.References) != 2 {
		t.Fatal("member no-op facts")
	}
}

func TestSyncPushFinanceMemberNamesAndRollback(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	trip, self, _ := financeStart(t, f)
	a, b := uuid.New(), uuid.New()
	first := replaceMembers(f, trip, memberInput(self, "~SYNC0", "50"), memberInput(a, "Alpha", "25"), memberInput(b, "Beta", "25"))
	financeApplied(t, f.push(first).Results[0])
	cleanupNames := func() {
		f.sql(`DROP TRIGGER IF EXISTS h07_name_audit ON trip_members; DROP FUNCTION IF EXISTS public.h07_name_audit(); DROP TABLE IF EXISTS h07_name_audit`)
	}
	cleanupNames()
	t.Cleanup(cleanupNames)
	f.sql(`CREATE TABLE h07_name_audit (name text, old_version bigint, new_version bigint);
 CREATE FUNCTION public.h07_name_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.name<>OLD.name AND NEW.version=OLD.version THEN INSERT INTO h07_name_audit VALUES(NEW.name,OLD.version,NEW.version); END IF; RETURN NEW; END $$;
 CREATE TRIGGER h07_name_audit BEFORE UPDATE ON trip_members FOR EACH ROW EXECUTE FUNCTION public.h07_name_audit()`)
	swap := replaceMembers(f, trip, memberInput(self, "~SYNC0", "50"), memberInput(a, "Beta", "25"), memberInput(b, "Alpha", "25"))
	financeApplied(t, f.push(swap).Results[0])
	financeCheck(f, `SELECT (SELECT count(*)=2 AND bool_and(lower(btrim(name))<>'~sync0') AND bool_and(char_length(name) BETWEEN 1 AND 30) AND bool_and(old_version=new_version) FROM h07_name_audit) AND (SELECT name='Beta' AND version=2 FROM trip_members WHERE id=$1) AND (SELECT name='Alpha' AND version=2 FROM trip_members WHERE id=$2)`, a, b)
	// A final name can collide with the next staging candidate; it must also be reserved.
	finalCollision := replaceMembers(f, trip, memberInput(self, "~SYNC0", "50"), memberInput(a, "Alpha", "25"), memberInput(b, "~SYNC1", "25"))
	f.sql(`TRUNCATE h07_name_audit`)
	financeApplied(t, f.push(finalCollision).Results[0])
	financeCheck(f, `SELECT count(*)=2 AND bool_and(name NOT IN ('~sync0','~sync1')) AND count(DISTINCT name)=2 FROM h07_name_audit`)
	replacement := uuid.New()
	reuse := replaceMembers(f, trip, memberInput(self, "~SYNC0", "50"), memberInput(a, "Alpha", "25"), memberInput(replacement, "~SYNC1", "25"))
	financeApplied(t, f.push(reuse).Results[0])
	financeCheck(f, `SELECT (SELECT deleted_at IS NOT NULL FROM trip_members WHERE id=$1) AND (SELECT name='~SYNC1' AND version=1 FROM trip_members WHERE id=$2)`, b, replacement)
	financeCode(t, f.push(replaceMembers(f, trip, memberInput(self, "~SYNC0", "50"), memberInput(a, "Alpha", "25"), memberInput(b, "Restored", "25"))).Results[0], "ID_ALREADY_USED")
	var before string
	_ = f.pool.QueryRow(context.Background(), `SELECT string_agg(id::text||':'||name||':'||version::text,',' ORDER BY id) FROM trip_members WHERE trip_id=$1`, trip).Scan(&before)
	var seq int64
	_ = f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&seq)
	fail := replaceMembers(f, trip, memberInput(self, "Alpha", "50"), memberInput(a, "~SYNC0", "25"), memberInput(replacement, "~SYNC1", "25"))
	f.sql(`CREATE FUNCTION h07_finance_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='sync.trip_member.replace' THEN RAISE EXCEPTION 'receipt failure'; END IF;RETURN NEW; END $$;CREATE TRIGGER h07_finance_receipt_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION h07_finance_receipt_fail()`)
	if x := f.push(fail).Results[0]; x.Status != "failed" {
		t.Fatal("expected receipt failure", x)
	}
	financeCheck(f, `SELECT (SELECT string_agg(id::text||':'||name||':'||version::text,',' ORDER BY id)=$2 FROM trip_members WHERE trip_id=$1) AND (SELECT last_seq=$3 FROM account_sync_state WHERE account_id=$4) AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE operation_id=$5)`, trip, before, seq, f.owner, fail.OperationID)
	f.sql(`DROP TRIGGER h07_finance_receipt_fail ON mutation_receipts;DROP FUNCTION h07_finance_receipt_fail()`)
	financeApplied(t, f.push(fail).Results[0])
	financeCode(t, f.push(replaceMembers(f, trip, memberInput(a, "Other", "100"))).Results[0], "VALIDATION_FAILED")
	financeCode(t, f.push(replaceMembers(f, trip, memberInput(self, "Me", "99"))).Results[0], "SHARE_PERCENT_SUM")
	financeCheck(f, `SELECT NOT EXISTS(SELECT 1 FROM sync_changes WHERE account_id=$1 AND snapshot::text LIKE '%~sync%')`, f.owner)
}

func TestSyncPushFinanceCategoryGuards(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	_, _, cat := financeStart(t, f)
	getIDs := func() []uuid.UUID {
		rows, err := f.pool.Query(context.Background(), `SELECT id FROM expense_categories WHERE account_id=$1 AND deleted_at IS NULL ORDER BY sort_order,id`, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		ids := []uuid.UUID{}
		for rows.Next() {
			var id uuid.UUID
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return ids
	}
	ids := getIDs()
	slices.Reverse(ids)
	reorder := financeOp("expense_category", "reorder", uuid.Nil, uuid.Nil, nil, map[string]any{"ordered_ids": ids})
	financeCode(t, f.push(reorder).Results[0], "COLLECTION_BASE_REQUIRED")
	reorder = withFinanceGuard(reorder, financeGuard(f, "categories", f.owner))
	// Distinct commands sharing the same old guard cannot both reorder.
	other := reorder
	other.OperationID = uuid.New()
	var wg sync.WaitGroup
	out := make(chan syncmodule.PushResult, 2)
	errs := make(chan error, 2)
	for _, op := range []syncmodule.Operation{reorder, other} {
		wg.Add(1)
		go func(op syncmodule.Operation) {
			defer wg.Done()
			r, err := f.svc.Push(context.Background(), f.a, "2", f.input(op))
			if err != nil {
				errs <- err
				return
			}
			out <- r.Results[0]
		}(op)
	}
	wg.Wait()
	close(out)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	wins, conflicts := 0, 0
	for r := range out {
		if r.Status == "applied" {
			wins++
		} else if r.Error != nil && r.Error.Code == "COLLECTION_CONFLICT" {
			conflicts++
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("guard race %d/%d", wins, conflicts)
	}
	noop := withFinanceGuard(financeOp("expense_category", "reorder", uuid.Nil, uuid.Nil, nil, map[string]any{"ordered_ids": getIDs()}), financeGuard(f, "categories", f.owner))
	n := f.push(noop).Results[0]
	financeApplied(t, n)
	if n.Result.CommitCursor != nil || len(n.Result.References) != len(ids) {
		t.Fatal("order no-op facts")
	}
	var orderBefore string
	_ = f.pool.QueryRow(context.Background(), `SELECT string_agg(id::text||':'||sort_order::text||':'||version::text,',' ORDER BY id) FROM expense_categories WHERE account_id=$1`, f.owner).Scan(&orderBefore)
	back := getIDs()
	slices.Reverse(back)
	rollback := withFinanceGuard(financeOp("expense_category", "reorder", uuid.Nil, uuid.Nil, nil, map[string]any{"ordered_ids": back}), financeGuard(f, "categories", f.owner))
	f.sql(`CREATE FUNCTION h07_category_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='sync.expense_category.reorder' THEN RAISE EXCEPTION 'receipt failure'; END IF;RETURN NEW; END $$;CREATE TRIGGER h07_category_receipt_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION h07_category_receipt_fail()`)
	if x := f.push(rollback).Results[0]; x.Status != "failed" {
		t.Fatal("category fault missing", x)
	}
	financeCheck(f, `SELECT (SELECT string_agg(id::text||':'||sort_order::text||':'||version::text,',' ORDER BY id)=$2 FROM expense_categories WHERE account_id=$1) AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE operation_id=$3) AND NOT EXISTS(SELECT 1 FROM sync_changes WHERE batch_id=$3)`, f.owner, orderBefore, rollback.OperationID)
	f.sql(`DROP TRIGGER h07_category_receipt_fail ON mutation_receipts;DROP FUNCTION h07_category_receipt_fail()`)
	wrong := noop
	wrong.OperationID = uuid.New()
	wrong.Guards = []syncmodule.GuardReference{{Kind: "categories", ScopeID: uuid.NewString(), Revision: noop.Guards[0].Revision}}
	financeCode(t, f.push(wrong).Results[0], "INVALID_REFERENCE")
	incomplete := withFinanceGuard(financeOp("expense_category", "reorder", uuid.Nil, uuid.Nil, nil, map[string]any{"ordered_ids": []uuid.UUID{cat}}), financeGuard(f, "categories", f.owner))
	financeCode(t, f.push(incomplete).Results[0], "VALIDATION_FAILED")
	added := financeOp("expense_category", "create", uuid.Nil, uuid.New(), nil, map[string]any{"name": "late"})
	financeApplied(t, f.push(added).Results[0])
	stale := noop
	stale.OperationID = uuid.New()
	financeCode(t, f.push(stale).Results[0], "COLLECTION_CONFLICT")
	r := f.push(noop).Results[0]
	if r.Status != "replayed" || itemRevision(t, r) != itemRevision(t, n) {
		t.Fatal("order original revision drift")
	}
	// Delete all effective categories, then sort the empty set and append at zero.
	for _, id := range getIDs() {
		var version string
		_ = f.pool.QueryRow(context.Background(), `SELECT version::text FROM expense_categories WHERE id=$1`, id).Scan(&version)
		financeApplied(t, f.push(financeOp("expense_category", "delete", uuid.Nil, id, versionBase(version), map[string]any{})).Results[0])
	}
	empty := withFinanceGuard(financeOp("expense_category", "reorder", uuid.Nil, uuid.Nil, nil, map[string]any{"ordered_ids": []uuid.UUID{}}), financeGuard(f, "categories", f.owner))
	financeApplied(t, f.push(empty).Results[0])
	zero := financeOp("expense_category", "create", uuid.Nil, uuid.New(), nil, map[string]any{"name": "zero"})
	financeApplied(t, f.push(zero).Results[0])
	financeCheck(f, `SELECT sort_order=0 FROM expense_categories WHERE id=$1`, *zero.EntityID)
	f.sql(`UPDATE expense_categories SET sort_order=2147483647 WHERE id=$1`, *zero.EntityID)
	financeCode(t, f.push(financeOp("expense_category", "create", uuid.Nil, uuid.New(), nil, map[string]any{"name": "overflow"})).Results[0], "SORT_ORDER_EXHAUSTED")
}

func TestSyncPushFinanceConflictsAndReferences(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	trip, self, cat := financeStart(t, f)
	create := ledgerCreate(f, trip, cat, self, "100", self)
	financeApplied(t, f.push(create).Results[0])
	id := *create.EntityID
	amount := withFinanceGuard(financeOp("ledger_entry", "update", trip, id, versionBase("1"), map[string]any{"amount": "110"}), financeGuard(f, "members", trip))
	financeApplied(t, f.push(amount).Results[0])
	participants := withFinanceGuard(financeOp("ledger_entry", "update", trip, id, versionBase("1"), map[string]any{"participant_member_ids": []uuid.UUID{self}}), financeGuard(f, "members", trip))
	financeCode(t, f.push(participants).Results[0], "VERSION_CONFLICT")
	notes := financeOp("ledger_entry", "update", trip, id, versionBase("1"), map[string]any{"notes": "independent"})
	financeApplied(t, f.push(notes).Results[0])
	other := f.newTrip("other")
	var otherSelf uuid.UUID
	_ = f.pool.QueryRow(context.Background(), `SELECT id FROM trip_members WHERE trip_id=$1 AND is_self`, other).Scan(&otherSelf)
	invalid := ledgerCreate(f, trip, cat, otherSelf, "5", otherSelf)
	financeCode(t, f.push(invalid).Results[0], "INVALID_REFERENCE")
	pdf := contentAsset(f, trip, "application/pdf")
	badAsset := financeOp("ledger_entry", "update", trip, id, versionBase("3"), map[string]any{"attachment_asset_ids": []uuid.UUID{pdf}})
	financeCode(t, f.push(badAsset).Results[0], "INVALID_REFERENCE")
	image := contentAsset(f, trip, "image/png")
	assetOp := financeOp("ledger_entry", "update", trip, id, versionBase("3"), map[string]any{"attachment_asset_ids": []uuid.UUID{image}})
	financeApplied(t, f.push(assetOp).Results[0])
	financeCheck(f, `SELECT status='uploading' FROM assets WHERE id=$1`, image)
	f.sql(`UPDATE assets SET deleted_at=now() WHERE id=$1`, image)
	badNoop := financeOp("ledger_entry", "update", trip, id, versionBase("4"), map[string]any{"attachment_asset_ids": []uuid.UUID{image}})
	financeCode(t, f.push(badNoop).Results[0], "INVALID_REFERENCE")
	clear := financeOp("ledger_entry", "update", trip, id, versionBase("4"), map[string]any{"attachment_asset_ids": []uuid.UUID{}})
	financeApplied(t, f.push(clear).Results[0])
	f.sql(`DELETE FROM sync_changes WHERE account_id=$1 AND entity_type='ledger_entry' AND entity_id=$2 AND entity_version=2`, f.owner, id)
	history := financeOp("ledger_entry", "update", trip, id, versionBase("1"), map[string]any{"occurred_on": "2026-10-02"})
	financeCode(t, f.push(history).Results[0], "MERGE_HISTORY_UNAVAILABLE")
	// A foreign account's category is never usable even though it is account-scoped.
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery")
	owner := uuid.MustParse(foreign.data()["account"].(map[string]any)["id"].(string))
	var foreignCat uuid.UUID
	_ = f.pool.QueryRow(context.Background(), `SELECT id FROM expense_categories WHERE account_id=$1 LIMIT 1`, owner).Scan(&foreignCat)
	financeCode(t, f.push(ledgerCreate(f, trip, foreignCat, self, "1", self)).Results[0], "INVALID_REFERENCE")
	financeCode(t, f.push(financeOp("expense_category", "update", uuid.Nil, foreignCat, versionBase("1"), map[string]any{"name": "denied"})).Results[0], "RESOURCE_NOT_FOUND")
}

func TestSyncPushFinanceRefundsAndReceiptRollback(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	trip, self, cat := financeStart(t, f)
	expense := ledgerCreate(f, trip, cat, self, "100", self)
	financeApplied(t, f.push(expense).Results[0])
	refund := financePayload(ledgerCreate(f, trip, cat, self, "30", self), map[string]any{"kind": "refund", "refunded_entry_id": *expense.EntityID})
	financeApplied(t, f.push(refund).Results[0])
	cat2 := financeOp("expense_category", "create", uuid.Nil, uuid.New(), nil, map[string]any{"name": "second"})
	financeApplied(t, f.push(cat2).Results[0])
	change := financeOp("ledger_entry", "update", trip, *expense.EntityID, versionBase("1"), map[string]any{"category_id": *cat2.EntityID})
	r := f.push(change).Results[0]
	financeApplied(t, r)
	if itemVersion(t, r, *refund.EntityID) != "2" {
		t.Fatal("linked category original ref")
	}
	del := financeOp("ledger_entry", "delete", trip, *expense.EntityID, versionBase("2"), map[string]any{})
	var seq int64
	_ = f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&seq)
	f.sql(`CREATE FUNCTION h07_ledger_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='sync.ledger_entry.delete' THEN RAISE EXCEPTION 'receipt failure'; END IF;RETURN NEW; END $$;CREATE TRIGGER h07_ledger_receipt_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION h07_ledger_receipt_fail()`)
	if x := f.push(del).Results[0]; x.Status != "failed" {
		t.Fatal("missing fault", x)
	}
	financeCheck(f, `SELECT (SELECT version=2 AND deleted_at IS NULL FROM ledger_entries WHERE id=$1) AND (SELECT version=2 AND refunded_entry_id=$1 FROM ledger_entries WHERE id=$2) AND (SELECT last_seq=$3 FROM account_sync_state WHERE account_id=$4) AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE operation_id=$5)`, *expense.EntityID, *refund.EntityID, seq, f.owner, del.OperationID)
	f.sql(`DROP TRIGGER h07_ledger_receipt_fail ON mutation_receipts;DROP FUNCTION h07_ledger_receipt_fail()`)
	// Independent unique requests must not over-refund the same expense.
	small := financePayload(ledgerCreate(f, trip, *cat2.EntityID, self, "50", self), map[string]any{"kind": "refund", "refunded_entry_id": *expense.EntityID})
	other := small
	other.OperationID = uuid.New()
	otherID := uuid.New()
	other.EntityID = &otherID
	var wg sync.WaitGroup
	results := make(chan syncmodule.PushResult, 2)
	errs := make(chan error, 2)
	for _, op := range []syncmodule.Operation{small, other} {
		wg.Add(1)
		go func(op syncmodule.Operation) {
			defer wg.Done()
			o, e := f.svc.Push(context.Background(), f.a, "2", f.input(op))
			if e != nil {
				errs <- e
				return
			}
			results <- o.Results[0]
		}(op)
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	wins, rejected := 0, 0
	var extra uuid.UUID
	for x := range results {
		if x.Status == "applied" {
			wins++
			extra = x.Result.References[0].ID
		} else if x.Error != nil && x.Error.Code == "REFUND_AMOUNT_EXCEEDED" {
			rejected++
		}
	}
	if wins != 1 || rejected != 1 {
		t.Fatal("refund concurrency", wins, rejected)
	}
	snapshot := f.snapshot("baseline", []uuid.UUID{trip})
	f.build(snapshot.ID)
	_, last := f.pages(snapshot.ID)
	after := financeOp("ledger_entry", "update", trip, *refund.EntityID, refBase(del.OperationID), map[string]any{"notes": "unlinked"}, del.OperationID)
	chain := f.push(del, after)
	for _, x := range chain.Results {
		financeApplied(t, x)
	}
	if itemVersion(t, chain.Results[0], *refund.EntityID) != "3" {
		t.Fatal("unlink original version")
	}
	cursor := *last.BaselineCursor
	unlinks := 0
	for i := 0; i < 20; i++ {
		page, err := f.svc.Changes(context.Background(), f.a, "2", cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range page.Changes {
			if ch.BatchID == del.OperationID {
				unlinks++
			}
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if unlinks != 3 {
		t.Fatal("related unlink batch", unlinks)
	}
	rr := f.push(del).Results[0]
	if rr.Status != "replayed" || itemVersion(t, rr, *refund.EntityID) != "3" {
		t.Fatal("replayed original refs")
	}
	financeApplied(t, f.push(financeOp("ledger_entry", "delete", trip, *refund.EntityID, versionBase("4"), map[string]any{})).Results[0])
	lastDelete := financeOp("ledger_entry", "delete", trip, extra, versionBase("2"), map[string]any{})
	lr := f.push(lastDelete).Results[0]
	financeApplied(t, lr)
	if resultVersion(t, lr, trip) != "3" {
		t.Fatal("unlock ref")
	}
	financeCheck(f, `SELECT currency_locked_at IS NULL FROM trips WHERE id=$1`, trip)
}

func TestSyncPushFinanceLegacyProjection(t *testing.T) {
	f := newPushFixture(t)
	trip := f.newTrip("legacy finance")
	var self uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM trip_members WHERE trip_id=$1 AND is_self`, trip).Scan(&self); err != nil {
		t.Fatal(err)
	}
	cat := categoryWebCreate(t, f, "custom")
	expense := ledgerCreate(f, trip, cat, self, "10", self)
	var seed map[string]any
	if err := json.Unmarshal(expense.Payload, &seed); err != nil {
		t.Fatal(err)
	}
	seed["id"] = expense.EntityID
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/trips/" + trip.String() + "/ledger-entries", token: f.webToken, headers: f.authHeaders(nil), body: seed}), 201, "")
	snapshot := f.snapshot("baseline", []uuid.UUID{trip})
	f.build(snapshot.ID)
	items, last := f.pages(snapshot.ID)
	found := false
	for _, it := range items {
		if it.EntityType == "ledger_entry" && it.EntityID == *expense.EntityID {
			found = true
		}
	}
	if !found {
		t.Fatal("missing ledger snapshot")
	}
	path := "/trips/" + trip.String() + "/ledger-entries/" + expense.EntityID.String()
	headers := f.authHeaders(map[string]string{"If-Match": `"1"`})
	body := map[string]any{"notes": "web"}
	expectStatus(t, f.do(request{method: http.MethodPatch, path: path, token: f.webToken, headers: headers, body: body}), 200, "")
	expectStatus(t, f.do(request{method: http.MethodPatch, path: path, token: f.webToken, headers: headers, body: body}), 200, "")
	delta, err := f.svc.Changes(context.Background(), f.a, "2", *last.BaselineCursor, 100)
	if err != nil || len(delta.Changes) != 1 {
		t.Fatal("legacy change", err, len(delta.Changes))
	}
	if !strings.Contains(string(delta.Changes[0].Data), `"notes":"web"`) {
		t.Fatal("legacy projection")
	}
	catPath := "/expense-categories/" + cat.String()
	expectStatus(t, f.do(request{method: http.MethodPatch, path: catPath, token: f.webToken, headers: f.authHeaders(map[string]string{"If-Match": `"1"`}), body: map[string]any{"icon": "food"}}), 200, "")
	// REST members remain usable before v2 enablement, and invalidate old guards.
	stale := financeGuard(f, "members", trip)
	expectStatus(t, f.do(request{method: http.MethodPut, path: "/trips/" + trip.String() + "/members", token: f.webToken, headers: f.authHeaders(nil), body: map[string]any{"members": []any{memberInput(self, "Web me", "100")}}}), 200, "")
	enablePushTestAccount(f)
	op := withFinanceGuard(financeOp("ledger_entry", "update", trip, *expense.EntityID, versionBase("2"), map[string]any{"amount": "10"}), stale)
	financeCode(t, f.push(op).Results[0], "COLLECTION_CONFLICT")
	// Memo updates still work without a member guard, preserving old split amounts.
	financeApplied(t, f.push(financeOp("ledger_entry", "update", trip, *expense.EntityID, versionBase("2"), map[string]any{"notes": "native"})).Results[0])
}

func financeApplied(t *testing.T, r syncmodule.PushResult) {
	t.Helper()
	if r.Status != "applied" {
		raw, _ := json.Marshal(r)
		t.Fatalf("not applied: %s", raw)
	}
}

func TestSyncPushFinanceBoundaryFacts(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	trip, self, cat := financeStart(t, f)
	// Same command ID applies exactly once, including the first currency lock.
	create := ledgerCreate(f, trip, cat, self, "10", self)
	var wg sync.WaitGroup
	results := make(chan syncmodule.PushResult, 5)
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, e := f.svc.Push(context.Background(), f.a, "2", f.input(create))
			if e != nil {
				errs <- e
				return
			}
			results <- o.Results[0]
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	counts := map[string]int{}
	for r := range results {
		counts[r.Status]++
		if itemVersion(t, r, *create.EntityID) != "1" || resultVersion(t, r, trip) != "2" {
			t.Fatal("concurrent original facts")
		}
	}
	if counts["applied"] != 1 || counts["replayed"] != 4 {
		t.Fatal(counts)
	}
	// Both candidate names use the same old member-set baseline.
	a := replaceMembers(f, trip, memberInput(self, "A", "100"))
	b := replaceMembers(f, trip, memberInput(self, "B", "100"))
	results = make(chan syncmodule.PushResult, 2)
	errs = make(chan error, 2)
	for _, op := range []syncmodule.Operation{a, b} {
		wg.Add(1)
		go func(op syncmodule.Operation) {
			defer wg.Done()
			o, e := f.svc.Push(context.Background(), f.a, "2", f.input(op))
			if e != nil {
				errs <- e
				return
			}
			results <- o.Results[0]
		}(op)
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	counts = map[string]int{}
	for r := range results {
		counts[r.Status]++
	}
	if counts["applied"] != 1 || counts["conflict"] != 1 {
		t.Fatal("member set concurrency", counts)
	}
	// A referenced member cannot be replaced by another ID, nor can self vanish.
	friend := uuid.New()
	financeApplied(t, f.push(replaceMembers(f, trip, memberInput(self, "Me", "50"), memberInput(friend, "Other", "50"))).Results[0])
	financial := withFinanceGuard(financeOp("ledger_entry", "update", trip, *create.EntityID, versionBase("1"), map[string]any{"participant_member_ids": []uuid.UUID{self, friend}}), financeGuard(f, "members", trip))
	financeApplied(t, f.push(financial).Results[0])
	financeCode(t, f.push(replaceMembers(f, trip, memberInput(self, "Me", "100"))).Results[0], "MEMBER_IN_USE")
	// A dependency rejection only blocks its descendant.
	bad := ledgerCreate(f, trip, uuid.New(), self, "1", self)
	dependent := ledgerCreate(f, trip, cat, self, "1", self)
	dependent.DependsOn = []uuid.UUID{bad.OperationID}
	independent := financeOp("expense_category", "create", uuid.Nil, uuid.New(), nil, map[string]any{"name": "independent"})
	batch := f.push(bad, dependent, independent)
	financeCode(t, batch.Results[0], "INVALID_REFERENCE")
	financeCode(t, batch.Results[1], "DEPENDENCY_REJECTED")
	financeApplied(t, batch.Results[2])
	tomb := uuid.New()
	f.sql(`INSERT INTO entity_tombstones(account_id,entity_type,entity_id,last_version,deleted_at,purged_at) VALUES($1,'expense_category',$2,5,now(),now())`, f.owner, tomb)
	financeCode(t, f.push(financeOp("expense_category", "create", uuid.Nil, tomb, nil, map[string]any{"name": "tomb"})).Results[0], "ID_ALREADY_USED")
	// Last entry deletion, currency unlock, all logs, and receipt roll back together.
	del := financeOp("ledger_entry", "delete", trip, *create.EntityID, versionBase("2"), map[string]any{})
	var seq int64
	_ = f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&seq)
	f.sql(`CREATE FUNCTION h07_unlock_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='sync.ledger_entry.delete' THEN RAISE EXCEPTION 'receipt failure'; END IF;RETURN NEW; END $$;CREATE TRIGGER h07_unlock_receipt_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION h07_unlock_receipt_fail()`)
	if x := f.push(del).Results[0]; x.Status != "failed" {
		t.Fatal("unlock fault missing", x)
	}
	financeCheck(f, `SELECT (SELECT version=2 AND deleted_at IS NULL FROM ledger_entries WHERE id=$1) AND (SELECT version=2 AND currency_locked_at IS NOT NULL FROM trips WHERE id=$2) AND (SELECT last_seq=$3 FROM account_sync_state WHERE account_id=$4) AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE operation_id=$5)`, *create.EntityID, trip, seq, f.owner, del.OperationID)
	f.sql(`DROP TRIGGER h07_unlock_receipt_fail ON mutation_receipts;DROP FUNCTION h07_unlock_receipt_fail()`)
	r := f.push(del).Results[0]
	financeApplied(t, r)
	if resultVersion(t, r, trip) != "3" {
		t.Fatal("unlock original fact")
	}
	// Original facts survive physical deletion of the resource; replay data is null.
	f.sql(`DELETE FROM ledger_entry_splits WHERE ledger_entry_id=$1`, *create.EntityID)
	f.sql(`DELETE FROM ledger_entries WHERE id=$1`, *create.EntityID)
	replay := f.push(del).Results[0]
	if replay.Status != "replayed" || replay.Result.Data != nil || itemVersion(t, replay, *create.EntityID) != "3" {
		t.Fatal("purged replay", replay)
	}
	financeApplied(t, f.push(pushOp("trip.delete", trip, versionBase("3"), map[string]any{})).Results[0])
	financeCode(t, f.push(ledgerCreate(f, trip, cat, self, "1", self)).Results[0], "TRIP_DELETED")
}
