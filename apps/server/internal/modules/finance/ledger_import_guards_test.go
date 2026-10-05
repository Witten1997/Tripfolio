package finance_test

import (
	"bytes"
	"context"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
	"reflect"
	"strings"
	"testing"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/finance"
)

type importSnapshotReader struct {
	finance.LedgerReader
	snapshot finance.ImportSnapshot
	err      error
}

func (r importSnapshotReader) ReadImportSnapshot(context.Context, uuid.UUID, uuid.UUID) (finance.ImportSnapshot, error) {
	return r.snapshot, r.err
}
func importWorkbook(t *testing.T, f *ledgerFixture) []byte {
	t.Helper()
	data, err := f.svc.ImportTemplate(context.Background(), f.actor, f.tripID)
	if err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	for cell, value := range map[string]string{"A5": "100", "B5": "美食", "C5": "个人", "D5": "2026-10-01"} {
		if err := book.SetCellStr("账单", cell, value); err != nil {
			t.Fatal(err)
		}
	}
	out, err := book.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func importReader(t *testing.T, f *ledgerFixture) importSnapshotReader {
	t.Helper()
	ctx := context.Background()
	trip, _, err := f.store.Trip(ctx, f.actor.AccountID, f.tripID)
	if err != nil {
		t.Fatal(err)
	}
	categories, err := f.store.ImportCategories(ctx, f.actor.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	members, err := f.store.ActiveMembers(ctx, f.actor.AccountID, f.tripID)
	if err != nil {
		t.Fatal(err)
	}
	return importSnapshotReader{snapshot: finance.ImportSnapshot{Trip: trip, Categories: categories, Members: members, ScopeRevisions: []write.ScopeRevision{{Kind: "members", ScopeID: f.tripID.String(), Revision: "fixture-revision"}}}}
}
func TestImportSnapshotAndIndependentGuard(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	file := importWorkbook(t, f)
	_, err := f.svc.PreviewImport(ctx, f.actor, f.tripID, file)
	e, ok := apperr.As(err)
	if !ok || e.Status != 503 {
		t.Fatalf("missing snapshot: %v", err)
	}
	// The legacy interface is nil; any fallback query panics.
	r := importReader(t, f)
	u := &ledgerGuardUOW{inner: f.uow, err: apperr.New(428, "COLLECTION_BASE_REQUIRED", "required")}
	svc := finance.NewLedgerService(u, r, paging.InsecureCodec{}, f.clock)
	preview, err := svc.PreviewImport(ctx, f.actor, f.tripID, file)
	if err != nil || preview.ValidCount != 1 || !reflect.DeepEqual(preview.ScopeRevisions, r.snapshot.ScopeRevisions) {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	id := uuid.New()
	_, err = svc.Import(ctx, f.actor, id, f.tripID, file, preview.Digest)
	e, ok = apperr.As(err)
	if !ok || e.Status != 428 || u.calls != 1 || len(u.required) != 1 {
		t.Fatalf("independent guard: %+v %v", u, err)
	}
	if len(f.uow.Changes()) != 0 {
		t.Fatal("failed import recorded changes")
	}
	u.err = nil
	_, err = svc.Import(ctx, f.actor, id, f.tripID, file, strings.Repeat("0", 64))
	e, ok = apperr.As(err)
	if !ok || e.Code != "IMPORT_PREVIEW_CHANGED" {
		t.Fatalf("digest: %v", err)
	}
	if len(f.uow.Changes()) != 0 {
		t.Fatal("stale digest wrote data")
	}
	result, err := svc.Import(ctx, f.actor, id, f.tripID, file, preview.Digest)
	if err != nil || result.Replayed {
		t.Fatalf("retry after rejection: %+v %v", result, err)
	}
	count := len(f.uow.Changes())
	calls := u.calls
	replay, err := svc.Import(ctx, f.actor, id, f.tripID, file, preview.Digest)
	if err != nil || !replay.Replayed || u.calls != calls || len(f.uow.Changes()) != count {
		t.Fatalf("replay: %+v %v", replay, err)
	}
}
func TestImportSnapshotErrorDoesNotFallBack(t *testing.T) {
	svc := finance.NewLedgerService(nil, importSnapshotReader{err: apperr.Gone("TRIP_DELETED", "deleted")}, nil, nil)
	_, err := svc.PreviewImport(context.Background(), actor.Actor{}, uuid.New(), nil)
	e, ok := apperr.As(err)
	if !ok || e.Status != 410 {
		t.Fatalf("snapshot error: %v", err)
	}
}
