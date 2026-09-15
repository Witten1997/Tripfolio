package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/modules/travel/share"
)

func TestShareStoreLifecycleAndIsolation(t *testing.T) {
	f := newAPIFixture(t)
	reg := f.registerWeb(uniqueEmail(), "correct horse battery")
	accountID := uuid.MustParse(reg.data()["account"].(map[string]any)["id"].(string))
	tr := f.createTrip(reg.data()["access_token"].(string), map[string]any{"name": "仓储测试", "start_date": "2026-10-01", "end_date": "2026-10-02"})
	tripID := uuid.MustParse(tr["id"].(string))
	ctx := context.Background()
	s := travelpg.NewShareStore(f.pool)
	before, _, _ := f.countChanges(accountID.String(), "trip")
	now := time.Now().UTC().Truncate(time.Microsecond)
	record := share.Record{ID: uuid.New(), AccountID: accountID, TripID: tripID, Token: "a000000000000000000001", CreatedAt: now}
	if _, ok, err := s.GetByTrip(ctx, accountID, tripID); err != nil || ok {
		t.Fatalf("未开启: %v %v", ok, err)
	}
	got, inserted, err := s.Insert(ctx, record)
	if err != nil || !inserted || got.ID != record.ID || !got.CreatedAt.Equal(now) {
		t.Fatalf("insert: %+v %v %v", got, inserted, err)
	}
	duplicate := record
	duplicate.ID, duplicate.Token = uuid.New(), "a000000000000000000002"
	if _, inserted, err := s.Insert(ctx, duplicate); err != nil || inserted {
		t.Fatalf("重复开启: %v %v", inserted, err)
	}
	if _, ok, err := s.GetByTrip(ctx, uuid.New(), tripID); err != nil || ok {
		t.Fatalf("跨账号读取: %v %v", ok, err)
	}
	if _, ok, err := s.Rotate(ctx, uuid.New(), tripID, duplicate.Token, now); err != nil || ok {
		t.Fatalf("跨账号轮换: %v %v", ok, err)
	}
	if err := s.Delete(ctx, uuid.New(), tripID); err != nil {
		t.Fatal(err)
	}
	resolved, ok, err := s.ResolveToken(ctx, record.Token)
	if err != nil || !ok || resolved.AccountID != accountID || resolved.TripID != tripID || resolved.OwnerStatus != "active" {
		t.Fatalf("resolve: %+v %v %v", resolved, ok, err)
	}
	for i := 0; i < 2; i++ {
		if err := s.RecordView(ctx, record.ID, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err = s.GetByTrip(ctx, accountID, tripID)
	if err != nil || !ok || got.ViewCount != 2 || got.LastViewedAt == nil || !got.LastViewedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("view count/time: %+v %v", got, err)
	}
	rotatedAt := now.Add(2 * time.Minute)
	got, ok, err = s.Rotate(ctx, accountID, tripID, duplicate.Token, rotatedAt)
	if err != nil || !ok || got.Token != duplicate.Token || got.RotatedAt == nil || !got.RotatedAt.Equal(rotatedAt) {
		t.Fatalf("rotate: %+v %v", got, err)
	}
	if _, ok, err := s.ResolveToken(ctx, record.Token); err != nil || ok {
		t.Fatalf("旧令牌: %v %v", ok, err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Delete(ctx, accountID, tripID); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := s.ResolveToken(ctx, duplicate.Token); err != nil || ok {
		t.Fatalf("关闭: %v %v", ok, err)
	}
	if after, _, _ := f.countChanges(accountID.String(), "trip"); after != before {
		t.Fatalf("同步日志 %d -> %d", before, after)
	}
	var version int64
	if err := f.pool.QueryRow(ctx, "SELECT version FROM trips WHERE account_id = $1 AND id = $2", accountID, tripID).Scan(&version); err != nil || version != 1 {
		t.Fatalf("旅行版本: %d %v", version, err)
	}
}
