package finance_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/modules/travel/member"
)

func (f *ledgerFixture) addMember(name, percent string, order int32) member.Resource {
	m := member.Resource{ID: uuid.New(), TripID: f.tripID, Name: name, SharePercent: percent, SortOrder: order, Version: 1, CreatedAt: f.clock.Now(), UpdatedAt: f.clock.Now()}
	f.store.PutMember(f.actor.AccountID, m)
	return m
}

func splitsOf(r finance.LedgerResource) string {
	var parts []string
	for _, s := range r.Splits {
		parts = append(parts, s.Amount)
	}
	return strings.Join(parts, ",")
}

func TestSplitDefaultsToSelfAndAllMembers(t *testing.T) {
	f := newLedgerFixture(t)
	f.store.PutMember(f.actor.AccountID, member.Resource{ID: f.self.ID, TripID: f.tripID, Name: "我", SharePercent: "50", IsSelf: true, Version: 1})
	b := f.addMember("小王", "30", 1)
	c := f.addMember("小李", "20", 2)

	r := f.create(t, finance.CreateLedgerCommand{Amount: "100"})
	if r.PayerMemberID != f.self.ID || r.SplitMode != finance.SplitEven || r.SplitCount != 3 {
		t.Fatalf("defaults: %+v", r)
	}
	// JPY 无小数：100 / 3 → 34, 33, 33，余数给前面的参与人
	if splitsOf(r) != "34,33,33" || r.PersonalAmount != "34" || r.Splits[1].MemberID != b.ID || r.Splits[2].MemberID != c.ID {
		t.Fatalf("even splits: %s personal=%s", splitsOf(r), r.PersonalAmount)
	}

	// 按比例：50/30/20 → 50, 30, 20
	r2 := f.create(t, finance.CreateLedgerCommand{Amount: "100", SplitMode: str("ratio")})
	if splitsOf(r2) != "50,30,20" || r2.PersonalAmount != "50" {
		t.Fatalf("ratio splits: %s", splitsOf(r2))
	}

	// 只选部分参与人并归一化：小王 30 / 小李 20 → 60%/40%；「我」不参与则 personal=0；付款人为小王
	r3 := f.create(t, finance.CreateLedgerCommand{Amount: "101", SplitMode: str("ratio"), PayerMemberID: uid(b.ID), ParticipantMemberIDs: []uuid.UUID{c.ID, b.ID}})
	if r3.PayerMemberID != b.ID || r3.SplitCount != 2 || r3.PersonalAmount != "0" || r3.Splits[0].MemberID != c.ID {
		t.Fatalf("partial: %+v", r3)
	}
	// 101 → 小李 40.4→40, 小王 60.6→60, 余 1 给第一位参与人（小李）
	if splitsOf(r3) != "41,60" {
		t.Fatalf("partial ratio splits: %s", splitsOf(r3))
	}
}

func TestSplitValidationAndReferences(t *testing.T) {
	f := newLedgerFixture(t)
	zero := f.addMember("零", "0", 1)
	f.store.PutMember(f.actor.AccountID, member.Resource{ID: f.self.ID, TripID: f.tripID, Name: "我", SharePercent: "100", IsSelf: true, Version: 1})
	stranger := uuid.New()

	_, err := f.tryCreate(finance.CreateLedgerCommand{PayerMemberID: uid(stranger)})
	expectCode(t, err, 422, "INVALID_REFERENCE")
	_, err = f.tryCreate(finance.CreateLedgerCommand{ParticipantMemberIDs: []uuid.UUID{stranger}})
	expectCode(t, err, 422, "INVALID_REFERENCE")
	_, err = f.tryCreate(finance.CreateLedgerCommand{ParticipantMemberIDs: []uuid.UUID{zero.ID, zero.ID}})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if fieldCodes(e) != "participant_member_ids:DUPLICATE" {
		t.Fatalf("fields: %s", fieldCodes(e))
	}
	_, err = f.tryCreate(finance.CreateLedgerCommand{SplitMode: str("weird")})
	e = expectCode(t, err, 422, "VALIDATION_FAILED")
	if fieldCodes(e) != "split_mode:INVALID" {
		t.Fatalf("fields: %s", fieldCodes(e))
	}
	// 参与人百分比之和为 0 时不能按比例
	_, err = f.tryCreate(finance.CreateLedgerCommand{SplitMode: str("ratio"), ParticipantMemberIDs: []uuid.UUID{zero.ID}})
	e = expectCode(t, err, 422, "VALIDATION_FAILED")
	if fieldCodes(e) != "participant_member_ids:RATIO_UNAVAILABLE" {
		t.Fatalf("fields: %s", fieldCodes(e))
	}
	// 均摊给 0% 的成员是允许的
	r := f.create(t, finance.CreateLedgerCommand{Amount: "10", ParticipantMemberIDs: []uuid.UUID{zero.ID}})
	if splitsOf(r) != "10" || r.PersonalAmount != "0" {
		t.Fatalf("even to zero member: %+v", r)
	}
}

func TestSplitRecomputeOnUpdate(t *testing.T) {
	f := newLedgerFixture(t)
	b := f.addMember("小王", "0", 1)
	r := f.create(t, finance.CreateLedgerCommand{Amount: "100"})
	if r.SplitCount != 2 || splitsOf(r) != "50,50" {
		t.Fatalf("initial: %+v", r)
	}
	ctx := context.Background()
	// 只改金额：份额重算，changed_fields 额外含 splits
	res, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, r.ID, 1, finance.LedgerPatch{Amount: str("101")})
	if err != nil {
		t.Fatal(err)
	}
	u := res.Data.(finance.LedgerResource)
	if splitsOf(u) != "51,50" || u.PersonalAmount != "51" {
		t.Fatalf("after amount: %s", splitsOf(u))
	}
	last := f.uow.Changes()[len(f.uow.Changes())-1]
	if strings.Join(last.ChangedFields, ",") != "amount,splits" {
		t.Fatalf("changed fields: %v", last.ChangedFields)
	}
	// 改参与人：只剩小王
	res, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, r.ID, 2, finance.LedgerPatch{ParticipantMemberIDs: &[]uuid.UUID{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	u = res.Data.(finance.LedgerResource)
	if u.SplitCount != 1 || splitsOf(u) != "101" || u.PersonalAmount != "0" {
		t.Fatalf("after participants: %+v", u)
	}
	last = f.uow.Changes()[len(f.uow.Changes())-1]
	if strings.Join(last.ChangedFields, ",") != "splits" {
		t.Fatalf("changed fields: %v", last.ChangedFields)
	}
	// 改付款人
	res, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, r.ID, 3, finance.LedgerPatch{PayerMemberID: uid(b.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Data.(finance.LedgerResource).PayerMemberID != b.ID {
		t.Fatal("payer not updated")
	}
	// 无效付款人
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, r.ID, 4, finance.LedgerPatch{PayerMemberID: uid(uuid.New())})
	expectCode(t, err, 422, "INVALID_REFERENCE")
}

func TestRefundInheritsOriginalSplit(t *testing.T) {
	f := newLedgerFixture(t)
	b := f.addMember("小王", "0", 1)
	original := f.create(t, finance.CreateLedgerCommand{Amount: "100", PayerMemberID: uid(b.ID), ParticipantMemberIDs: []uuid.UUID{b.ID, f.self.ID}})
	refund := f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "10", RefundedEntryID: uid(original.ID)})
	if refund.PayerMemberID != b.ID || refund.SplitCount != 2 || refund.Splits[0].MemberID != b.ID || splitsOf(refund) != "5,5" || refund.PersonalAmount != "5" {
		t.Fatalf("refund should inherit payer/participants: %+v", refund)
	}
	// 显式指定则不继承
	own := f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "10", RefundedEntryID: uid(original.ID), ParticipantMemberIDs: []uuid.UUID{f.self.ID}})
	if own.PayerMemberID != f.self.ID || own.SplitCount != 1 || own.PersonalAmount != "10" {
		t.Fatalf("explicit refund split: %+v", own)
	}
	// 独立退款默认「我」全额
	solo := f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "7"})
	if solo.PayerMemberID != f.self.ID || solo.SplitCount != 2 {
		t.Fatalf("solo refund defaults to all members: %+v", solo)
	}
}

func TestSettlementAndTransfers(t *testing.T) {
	f := newLedgerFixture(t)
	b := f.addMember("小王", "0", 1)
	c := f.addMember("小李", "0", 2)
	ctx := context.Background()
	// 我付 90 三人均摊：我 30、王 30、李 30
	f.create(t, finance.CreateLedgerCommand{Amount: "90"})
	// 小王付 30，只有小李参与
	f.create(t, finance.CreateLedgerCommand{Amount: "30", PayerMemberID: uid(b.ID), ParticipantMemberIDs: []uuid.UUID{c.ID}})
	// 退款 9：我收款，三人各退 3
	f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "9"})
	// 已删除账目不计
	dead := f.create(t, finance.CreateLedgerCommand{Amount: "1000"})
	if _, err := f.svc.Delete(ctx, f.actor, uuid.New(), f.tripID, dead.ID, 1); err != nil {
		t.Fatal(err)
	}

	s, err := f.settle.Get(ctx, f.actor, f.tripID)
	if err != nil {
		t.Fatal(err)
	}
	if s.CurrencyCode != "JPY" || len(s.Members) != 3 {
		t.Fatalf("settlement: %+v", s)
	}
	// 我：paid 90-9=81，owed 30-3=27，net 54
	// 王：paid 30，owed 27，net 3
	// 李：paid 0，owed 30+30-3=57，net -57
	want := []finance.MemberSettlement{
		{MemberID: f.self.ID, Name: "我", IsSelf: true, PaidAmount: "81", OwedAmount: "27", NetAmount: "54"},
		{MemberID: b.ID, Name: "小王", PaidAmount: "30", OwedAmount: "27", NetAmount: "3"},
		{MemberID: c.ID, Name: "小李", PaidAmount: "0", OwedAmount: "57", NetAmount: "-57"},
	}
	for i, w := range want {
		if s.Members[i] != w {
			t.Fatalf("member %d: got %+v want %+v", i, s.Members[i], w)
		}
	}
	if len(s.Transfers) != 2 || s.Transfers[0] != (finance.SettlementTransfer{FromMemberID: c.ID, ToMemberID: f.self.ID, Amount: "54"}) ||
		s.Transfers[1] != (finance.SettlementTransfer{FromMemberID: c.ID, ToMemberID: b.ID, Amount: "3"}) {
		t.Fatalf("transfers: %+v", s.Transfers)
	}

	other := f.otherAcc
	if _, err := f.settle.Get(ctx, other, f.tripID); err == nil {
		t.Fatal("other account must not read settlement")
	} else {
		expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
	}
	_ = write.Result{}
}

func TestStatisticsUsesPersonalRefundShare(t *testing.T) {
	f := newLedgerFixture(t)
	b := f.addMember("小王", "0", 1)
	f.create(t, finance.CreateLedgerCommand{Amount: "100"})                                                                   // 我 50
	f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "20"})                                                    // 我退 10
	f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "6", ParticipantMemberIDs: []uuid.UUID{b.ID}})            // 我 0
	f.create(t, finance.CreateLedgerCommand{Amount: "30", PayerMemberID: uid(b.ID), ParticipantMemberIDs: []uuid.UUID{b.ID}}) // 我 0
	s, err := f.stats.Get(context.Background(), f.actor, f.tripID, finance.StatisticsFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if s.FilteredTotals.ExpenseAmount != "50" || s.FilteredTotals.RefundAmount != "10" || s.FilteredTotals.NetAmount != "40" || s.FilteredTotals.EntryCount != 4 {
		t.Fatalf("totals: %+v", s.FilteredTotals)
	}
}
