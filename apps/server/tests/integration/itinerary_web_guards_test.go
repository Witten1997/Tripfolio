package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	baselinepg "tripfolio/server/internal/adapters/postgres/collectionbaseline"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/write"
	baseline "tripfolio/server/internal/modules/collectionbaseline"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/itinerary"
)

func itineraryWebBaseline(t *testing.T, f *pushFixture, trip uuid.UUID, dates ...string) (itinerary.ReorderCommand, []collectionguard.Guard) {
	t.Helper()
	service := collectionService(t, baselinepg.NewStore(f.pool))
	cmd := itinerary.ReorderCommand{Days: make([]itinerary.ReorderDay, 0, len(dates))}
	guards := make([]collectionguard.Guard, 0, len(dates))
	for _, day := range dates {
		page, err := service.List(context.Background(), f.a, baseline.Query{Kind: "itinerary_day", ScopeID: trip.String() + "/" + day, Limit: 100})
		if err != nil || page.NextCursor != nil {
			t.Fatalf("fixture needs complete day: %+v %v", page, err)
		}
		resources := make([]itinerary.Resource, 0, len(page.Items))
		for _, raw := range page.Items {
			var item itinerary.Resource
			if err := json.Unmarshal(raw, &item); err != nil {
				t.Fatal(err)
			}
			resources = append(resources, item)
		}
		sort.Slice(resources, func(i, j int) bool { return resources[i].SortOrder < resources[j].SortOrder })
		items := make([]itinerary.ReorderItem, 0, len(resources))
		for _, item := range resources {
			items = append(items, itinerary.ReorderItem{ID: item.ID, BaseVersion: int64(item.Version)})
		}
		cmd.Days = append(cmd.Days, itinerary.ReorderDay{Date: day, Items: items})
		guards = append(guards, collectionguard.Guard{Kind: page.Kind, ScopeID: page.ScopeID, Revision: page.Revision})
	}
	return cmd, guards
}

func itineraryWebOrder(f *pushFixture, trip, key uuid.UUID, cmd itinerary.ReorderCommand, guards []collectionguard.Guard) apiResponse {
	f.t.Helper()
	days := make([]map[string]any, 0, len(cmd.Days))
	for _, day := range cmd.Days {
		items := make([]map[string]any, 0, len(day.Items))
		for _, item := range day.Items {
			items = append(items, map[string]any{"id": item.ID.String(), "base_version": strconv.FormatInt(item.BaseVersion, 10)})
		}
		days = append(days, map[string]any{"date": day.Date, "items": items})
	}
	headers := map[string]string{"Idempotency-Key": key.String()}
	if guards != nil {
		raw, err := json.Marshal(guards)
		if err != nil {
			f.t.Fatal(err)
		}
		headers["X-Collection-Guards"] = string(raw)
	}
	return f.do(request{method: http.MethodPost, path: "/trips/" + trip.String() + "/itinerary-items/reorder", token: f.webToken, headers: headers, body: map[string]any{"days": days}})
}

func itineraryWebResult(t *testing.T, response apiResponse) write.Result {
	t.Helper()
	expectStatus(t, response, 200, "")
	var body struct {
		Data write.Result `json:"data"`
	}
	if err := json.Unmarshal(response.Raw, &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}
func itineraryWebPolicy(f *pushFixture) {
	f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true) ON CONFLICT(account_id) DO UPDATE SET collection_guards_required=true`, f.owner)
}
func itineraryWebFixture(t *testing.T) (*pushFixture, uuid.UUID) {
	f := newItineraryPushFixture(t)
	trip := f.newTrip("web order")
	for _, title := range []string{"A", "B"} {
		applied(t, f.push(itineraryCreate(t, f, trip, "2026-10-02", title)).Results[0])
	}
	return f, trip
}
func itineraryWebState(t *testing.T, f *pushFixture, trip uuid.UUID) string {
	t.Helper()
	var state string
	err := f.pool.QueryRow(context.Background(), `SELECT jsonb_build_object(
'items',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM itinerary_items i WHERE account_id=$1 AND trip_id=$2),
'route',(SELECT to_jsonb(r) FROM trip_route_summaries r WHERE account_id=$1 AND trip_id=$2),
'seq',(SELECT last_seq FROM account_sync_state WHERE account_id=$1),
'receipts',(SELECT count(*) FROM mutation_receipts WHERE account_id=$1),
'jobs',(SELECT count(*) FROM river_job WHERE args->>'account_id'=$1::uuid::text))::text`, f.owner, trip).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestItineraryWebGuardsRequiredAndAtomic(t *testing.T) {
	f, trip := itineraryWebFixture(t)
	itineraryWebPolicy(f)
	cmd, guards := itineraryWebBaseline(t, f, trip, "2026-10-02", "2026-10-20")
	for _, name := range []string{"missing", "explicit-empty", "missing-target", "stale", "unrelated", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			provided := append([]collectionguard.Guard{}, guards...)
			status, code := 422, "INVALID_REFERENCE"
			switch name {
			case "missing":
				provided = nil
				status, code = 428, "COLLECTION_BASE_REQUIRED"
			case "explicit-empty":
				provided = []collectionguard.Guard{}
				status, code = 428, "COLLECTION_BASE_REQUIRED"
			case "missing-target":
				provided = provided[:1]
				status, code = 428, "COLLECTION_BASE_REQUIRED"
			case "stale":
				provided[0].Revision = "sha256:" + strings.Repeat("0", 64)
				status, code = 412, "COLLECTION_CONFLICT"
			case "unrelated":
				provided[1].ScopeID = trip.String() + "/2026-10-03"
			case "duplicate":
				provided = append(provided, provided[0])
			}
			before := itineraryWebState(t, f, trip)
			expectStatus(t, itineraryWebOrder(f, trip, uuid.New(), cmd, provided), status, code)
			if itineraryWebState(t, f, trip) != before {
				t.Fatal("rejected guard modified items/routes/events/receipts/jobs")
			}
		})
	}
	// A valid guard cannot replace the existing ID and entity-version checks.
	for _, name := range []string{"missing-id", "extra-id", "old-version"} {
		t.Run(name, func(t *testing.T) {
			body, _ := itineraryWebBaseline(t, f, trip, "2026-10-02", "2026-10-20")
			status, code := 409, "ORDER_CHANGED"
			switch name {
			case "missing-id":
				body.Days[0].Items = body.Days[0].Items[:1]
			case "extra-id":
				body.Days[0].Items = append(body.Days[0].Items, itinerary.ReorderItem{ID: uuid.New(), BaseVersion: 1})
			case "old-version":
				body.Days[0].Items[0].BaseVersion++
				status, code = 412, "VERSION_CONFLICT"
			}
			before := itineraryWebState(t, f, trip)
			expectStatus(t, itineraryWebOrder(f, trip, uuid.New(), body, guards), status, code)
			if itineraryWebState(t, f, trip) != before {
				t.Fatal("invalid full set/version partially committed")
			}
		})
	}
}

func TestItineraryWebGuardsStaleSourceAndDestination(t *testing.T) {
	for _, mutation := range []string{"source-insert", "target-insert", "delete", "move-out", "move-in", "version"} {
		t.Run(mutation, func(t *testing.T) {
			f, trip := itineraryWebFixture(t)
			itineraryWebPolicy(f)
			cmd, guards := itineraryWebBaseline(t, f, trip, "2026-10-02", "2026-10-20")
			id := cmd.Days[0].Items[0].ID
			cmd.Days[1].Items = append(cmd.Days[1].Items, cmd.Days[0].Items[0])
			cmd.Days[0].Items = cmd.Days[0].Items[1:]
			switch mutation {
			case "source-insert":
				applied(t, f.push(itineraryCreate(t, f, trip, "2026-10-02", "other client")).Results[0])
			case "target-insert":
				applied(t, f.push(itineraryCreate(t, f, trip, "2026-10-20", "other client")).Results[0])
			case "delete":
				op := itemOp("itinerary_item", "delete", trip, id, versionBase("1"), map[string]any{})
				op.Guards = []syncmodule.GuardReference{itineraryGuard(trip, "2026-10-02", guards[0].Revision)}
				applied(t, f.push(op).Results[0])
			case "move-out":
				applied(t, f.push(itineraryOrder(t, f, trip, []string{"2026-10-02", "2026-10-03"}, [][]uuid.UUID{{cmd.Days[0].Items[0].ID}, {id}})).Results[0])
			case "move-in":
				incoming := itineraryCreate(t, f, trip, "2026-10-03", "another day")
				applied(t, f.push(incoming).Results[0])
				applied(t, f.push(itineraryOrder(t, f, trip, []string{"2026-10-03", "2026-10-20"}, [][]uuid.UUID{{}, {*incoming.EntityID}})).Results[0])
			case "version":
				applied(t, f.push(itemOp("itinerary_item", "update", trip, id, versionBase("1"), map[string]any{"notes": "other client"})).Results[0])
			}
			before := itineraryWebState(t, f, trip)
			expectStatus(t, itineraryWebOrder(f, trip, uuid.New(), cmd, guards), 412, "COLLECTION_CONFLICT")
			if itineraryWebState(t, f, trip) != before {
				t.Fatal("stale cross-day reorder partially committed")
			}
		})
	}
}

func TestItineraryWebGuardsOriginalFactsAndLegacyReplay(t *testing.T) {
	f, trip := itineraryWebFixture(t)
	cmd, guards := itineraryWebBaseline(t, f, trip, "2026-10-02", "2026-10-20")
	legacyID := uuid.New()
	legacy := itineraryWebResult(t, itineraryWebOrder(f, trip, legacyID, cmd, nil))
	if legacy.CommitCursor != nil || len(legacy.ScopeRevisions) != 2 {
		t.Fatal("legacy no-op missing new final facts")
	}
	// Model a genuine pre-field receipt without changing its original fingerprint.
	f.sql(`UPDATE mutation_receipts SET result=result-'scope_revisions' WHERE account_id=$1 AND operation_id=$2`, f.owner, legacyID)
	itineraryWebPolicy(f)
	replayed := itineraryWebResult(t, itineraryWebOrder(f, trip, legacyID, cmd, nil))
	if !replayed.Replayed || len(replayed.ScopeRevisions) != 0 {
		t.Fatal("legacy receipt gained fabricated facts")
	}
	expectStatus(t, itineraryWebOrder(f, trip, uuid.New(), cmd, nil), 428, "COLLECTION_BASE_REQUIRED")
	key := uuid.New()
	noOp := itineraryWebResult(t, itineraryWebOrder(f, trip, key, cmd, guards))
	if noOp.CommitCursor != nil || len(noOp.Affected) != 2 || len(noOp.ScopeRevisions) != 2 {
		t.Fatalf("no-op facts %+v", noOp)
	}
	for _, fact := range noOp.ScopeRevisions {
		if fact.Revision != itineraryRevision(t, f, trip, strings.Split(fact.ScopeID, "/")[1]) {
			t.Fatal("no-op revision mismatch")
		}
	}
	move, moveGuards := itineraryWebBaseline(t, f, trip, "2026-10-02", "2026-10-20")
	move.Days[1].Items = move.Days[0].Items
	move.Days[0].Items = []itinerary.ReorderItem{}
	moveKey := uuid.New()
	moved := itineraryWebResult(t, itineraryWebOrder(f, trip, moveKey, move, moveGuards))
	if moved.CommitCursor == nil || len(moved.ScopeRevisions) != 2 {
		t.Fatalf("move facts %+v", moved)
	}
	for _, fact := range moved.ScopeRevisions {
		if fact.Revision != itineraryRevision(t, f, trip, strings.Split(fact.ScopeID, "/")[1]) {
			t.Fatal("post-write revision mismatch")
		}
	}
	old := itineraryWebResult(t, itineraryWebOrder(f, trip, key, cmd, guards))
	if !old.Replayed || !reflect.DeepEqual(old.ScopeRevisions, noOp.ScopeRevisions) || !reflect.DeepEqual(old.Affected, noOp.Affected) {
		t.Fatal("replayed original facts drifted")
	}
	_, latest := itineraryWebBaseline(t, f, trip, "2026-10-02", "2026-10-20")
	expectStatus(t, itineraryWebOrder(f, trip, key, cmd, latest), 409, "IDEMPOTENCY_CONFLICT")
	// All-empty days still carry verified scope facts and do not advance the cursor.
	empty, emptyGuards := itineraryWebBaseline(t, f, trip, "2026-10-02", "2026-10-03")
	emptyResult := itineraryWebResult(t, itineraryWebOrder(f, trip, uuid.New(), empty, emptyGuards))
	if emptyResult.CommitCursor != nil || len(emptyResult.Affected) != 0 || len(emptyResult.ScopeRevisions) != 2 {
		t.Fatal("empty-day facts missing")
	}
	// A later write must not replace the original successful move's facts either.
	applied(t, f.push(itineraryCreate(t, f, trip, "2026-10-20", "later arrival")).Results[0])
	moveReplay := itineraryWebResult(t, itineraryWebOrder(f, trip, moveKey, move, moveGuards))
	if !moveReplay.Replayed || !reflect.DeepEqual(moveReplay.ScopeRevisions, moved.ScopeRevisions) || !reflect.DeepEqual(moveReplay.Affected, moved.Affected) || !reflect.DeepEqual(moveReplay.CommitCursor, moved.CommitCursor) {
		t.Fatal("successful move receipt facts drifted")
	}
}

func TestItineraryWebGuardsConcurrentReorders(t *testing.T) {
	f, trip := itineraryWebFixture(t)
	itineraryWebPolicy(f)
	cmd, guards := itineraryWebBaseline(t, f, trip, "2026-10-02", "2026-10-20")
	cmd.Days[1].Items = cmd.Days[0].Items
	cmd.Days[0].Items = []itinerary.ReorderItem{}
	results := make(chan apiResponse, 2)
	for range 2 {
		go func() { results <- itineraryWebOrder(f, trip, uuid.New(), cmd, guards) }()
	}
	counts := map[int]int{}
	for range 2 {
		response := <-results
		counts[response.Status]++
		if response.Status != 200 {
			expectStatus(t, response, 412, "COLLECTION_CONFLICT")
		}
	}
	if counts[200] != 1 || counts[412] != 1 {
		t.Fatalf("concurrent results %v", counts)
	}
	var count, version int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*),max(version) FROM itinerary_items WHERE account_id=$1 AND trip_id=$2 AND scheduled_on='2026-10-20'`, f.owner, trip).Scan(&count, &version); err != nil || count != 2 || version != 2 {
		t.Fatalf("duplicate/partial move %d %d %v", count, version, err)
	}
}

func TestItineraryWebGuardsNativeProofWithProtection(t *testing.T) {
	f := newItineraryPushFixture(t)
	trip := f.newTrip("protected native itinerary")
	itineraryWebPolicy(f)
	create := itineraryCreate(t, f, trip, "2026-10-02", "native")
	applied(t, f.push(create).Results[0])
	order := itineraryOrder(t, f, trip, []string{"2026-10-02", "2026-10-20"}, [][]uuid.UUID{{}, {*create.EntityID}})
	order.Guards[0] = syncmodule.GuardReference{Kind: "itinerary_day", ScopeID: trip.String() + "/2026-10-02", OperationID: &create.OperationID}
	order.DependsOn = []uuid.UUID{create.OperationID}
	ordered := f.push(order).Results[0]
	applied(t, ordered)
	if len(ordered.Result.ScopeRevisions) != 2 || itemVersion(t, ordered, *create.EntityID) != "2" {
		t.Fatal("native proof did not reach bound reorder service")
	}
	update := itemOp("itinerary_item", "update", trip, *create.EntityID, refBase(order.OperationID), map[string]any{"notes": "independent update"}, order.OperationID)
	applied(t, f.push(update).Results[0])
	remove := itemOp("itinerary_item", "delete", trip, *create.EntityID, refBase(update.OperationID), map[string]any{}, update.OperationID)
	remove.Guards = []syncmodule.GuardReference{{Kind: "itinerary_day", ScopeID: trip.String() + "/2026-10-20", OperationID: &update.OperationID}}
	applied(t, f.push(remove).Results[0])
	replay := f.push(order).Results[0]
	if replay.Status != "replayed" || !reflect.DeepEqual(replay.Result.ScopeRevisions, ordered.Result.ScopeRevisions) || itemVersion(t, replay, *create.EntityID) != "2" {
		t.Fatal("native original facts drifted after delete")
	}
	missing := itineraryCreate(t, f, trip, "2026-10-02", "missing")
	missing.Guards = []syncmodule.GuardReference{}
	if result := f.push(missing).Results[0]; result.Error == nil || result.Error.Code != "COLLECTION_BASE_REQUIRED" {
		t.Fatalf("native missing guard %+v", result)
	}
	dependent := itineraryCreate(t, f, trip, "2026-10-20", "old epoch receipt")
	dependent.DependsOn = []uuid.UUID{remove.OperationID}
	dependent.Guards = []syncmodule.GuardReference{{Kind: "itinerary_day", ScopeID: trip.String() + "/2026-10-20", OperationID: &remove.OperationID}}
	f.sql(`UPDATE mutation_receipts SET result=jsonb_set(result,'{sync,epoch}',to_jsonb($3::text)) WHERE account_id=$1 AND operation_id=$2`, f.owner, remove.OperationID, uuid.NewString())
	if result := f.push(dependent).Results[0]; result.Error == nil || result.Error.Code != "INVALID_REFERENCE" {
		t.Fatalf("old epoch dependency accepted %+v", result)
	}
}

func TestItineraryWebGuardsBoundaryAndRollback(t *testing.T) {
	for _, mode := range []string{"foreign-trip", "deleted-trip", "old-epoch", "receipt-failure"} {
		t.Run(mode, func(t *testing.T) {
			f, trip := itineraryWebFixture(t)
			itineraryWebPolicy(f)
			cmd, guards := itineraryWebBaseline(t, f, trip, "2026-10-02", "2026-10-20")
			status, code := 500, "INTERNAL_ERROR"
			switch mode {
			case "foreign-trip":
				foreign := f.registerWeb(uniqueEmail(), "correct horse battery")
				f.webToken = foreign.data()["access_token"].(string)
				status, code = 404, "RESOURCE_NOT_FOUND"
			case "deleted-trip":
				f.sql(`UPDATE trips SET deleted_at=now(),purge_after_at=now()+interval '30 days' WHERE id=$1`, trip)
				status, code = 410, "TRIP_DELETED"
			case "old-epoch":
				f.sql(`UPDATE account_sync_state SET sync_epoch=$2 WHERE account_id=$1`, f.owner, uuid.New())
				status, code = 412, "COLLECTION_CONFLICT"
			case "receipt-failure":
				cmd.Days[1].Items, cmd.Days[0].Items = cmd.Days[0].Items, []itinerary.ReorderItem{}
				f.sql(`CREATE FUNCTION public.h07_i_web_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='itinerary.reorder' THEN RAISE EXCEPTION 'receipt failure'; END IF; RETURN NEW; END $$;CREATE TRIGGER h07_i_web_receipt_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION public.h07_i_web_receipt_fail()`)
				t.Cleanup(func() {
					f.sql(`DROP TRIGGER IF EXISTS h07_i_web_receipt_fail ON mutation_receipts;DROP FUNCTION IF EXISTS public.h07_i_web_receipt_fail()`)
				})
			}
			before := itineraryWebState(t, f, trip)
			expectStatus(t, itineraryWebOrder(f, trip, uuid.New(), cmd, guards), status, code)
			if itineraryWebState(t, f, trip) != before {
				t.Fatal("boundary/final receipt failure partially committed")
			}
		})
	}
}
