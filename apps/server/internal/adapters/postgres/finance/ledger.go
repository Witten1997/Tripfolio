package financepg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/modules/travel/member"
	"tripfolio/server/internal/modules/travel/trip"
)

// 本文件实现 finance 模块账目与统计的 PostgreSQL 适配：金额 NUMERIC(18,4) 按旅行币种小数位转回规范字符串，
// currency_code 从 trips 派生，票据引用存 ledger_attachments 并按 sort_order 还原顺序。

func toLedgerResource(row dbgen.LedgerEntry, currency string, attachments []uuid.UUID, splits []finance.LedgerSplit) (finance.LedgerResource, error) {
	units, ok := metadata.MinorUnits(currency)
	if !ok {
		return finance.LedgerResource{}, fmt.Errorf("账目 %s 的旅行币种 %q 不受支持", row.ID, currency)
	}
	amount, err := money.FromStorage(row.Amount, units)
	if err != nil {
		return finance.LedgerResource{}, fmt.Errorf("账目 %s 的金额 %q 无法按 %s 表示: %w", row.ID, row.Amount, currency, err)
	}
	personal, err := money.FromStorage(row.PersonalAmount, units)
	if err != nil {
		return finance.LedgerResource{}, fmt.Errorf("账目 %s 的个人金额 %q 无法按 %s 表示: %w", row.ID, row.PersonalAmount, currency, err)
	}
	var refunded *uuid.UUID
	if row.RefundedEntryID.Valid {
		id := row.RefundedEntryID.UUID
		refunded = &id
	}
	if attachments == nil {
		attachments = []uuid.UUID{}
	}
	if splits == nil {
		splits = []finance.LedgerSplit{}
	}
	return finance.LedgerResource{
		ID: row.ID, TripID: row.TripID, Kind: finance.LedgerKind(row.Kind), Amount: amount,
		SplitCount: row.SplitCount, PersonalAmount: personal, PayerMemberID: row.PayerMemberID, SplitMode: finance.SplitMode(row.SplitMode), Splits: splits, CurrencyCode: currency,
		CategoryID: row.CategoryID, OccurredOn: types.DateOf(row.OccurredOn), Notes: row.Notes, RefundedEntryID: refunded,
		AttachmentAssetIDs: attachments, Version: types.Version(row.Version),
		CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
	}, nil
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func datePtr(d *types.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

func ledgerTripInfo(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID) (finance.LedgerTripInfo, bool, error) {
	row, err := q.GetLedgerTripInfo(ctx, dbgen.GetLedgerTripInfoParams{AccountID: accountID, ID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return finance.LedgerTripInfo{}, false, nil
	}
	if err != nil {
		return finance.LedgerTripInfo{}, false, err
	}
	var budget *string
	if row.BudgetAmount != nil {
		units, ok := metadata.MinorUnits(row.CurrencyCode)
		if !ok {
			return finance.LedgerTripInfo{}, false, fmt.Errorf("旅行 %s 的币种 %q 不受支持", tripID, row.CurrencyCode)
		}
		b, err := money.FromStorage(*row.BudgetAmount, units)
		if err != nil {
			return finance.LedgerTripInfo{}, false, fmt.Errorf("旅行 %s 的预算 %q 无法按 %s 表示: %w", tripID, *row.BudgetAmount, row.CurrencyCode, err)
		}
		budget = &b
	}
	return finance.LedgerTripInfo{
		Timezone: row.Timezone, CurrencyCode: row.CurrencyCode, BudgetAmount: budget,
		CurrencyLockedAt: pgcore.UTCPtr(row.CurrencyLockedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
	}, true, nil
}

// splitsFor 按账目分组读取分摊份额，保持 sort_order 顺序；金额按币种小数位转回规范字符串。
func splitsFor(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID, currency string, entryIDs []uuid.UUID) (map[uuid.UUID][]finance.LedgerSplit, error) {
	out := map[uuid.UUID][]finance.LedgerSplit{}
	if len(entryIDs) == 0 {
		return out, nil
	}
	units, ok := metadata.MinorUnits(currency)
	if !ok {
		return nil, fmt.Errorf("旅行 %s 的币种 %q 不受支持", tripID, currency)
	}
	rows, err := q.ListLedgerSplits(ctx, dbgen.ListLedgerSplitsParams{AccountID: accountID, TripID: tripID, EntryIds: entryIDs})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		amount, err := money.FromStorage(r.Amount, units)
		if err != nil {
			return nil, fmt.Errorf("账目 %s 的份额 %q 无法按 %s 表示: %w", r.LedgerEntryID, r.Amount, currency, err)
		}
		out[r.LedgerEntryID] = append(out[r.LedgerEntryID], finance.LedgerSplit{MemberID: r.MemberID, Amount: amount})
	}
	return out, nil
}

// attachmentsFor 按账目分组读取票据资产 ID，保持 sort_order 顺序。
func attachmentsFor(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID, entryIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := map[uuid.UUID][]uuid.UUID{}
	if len(entryIDs) == 0 {
		return out, nil
	}
	rows, err := q.ListLedgerAttachments(ctx, dbgen.ListLedgerAttachmentsParams{AccountID: accountID, TripID: tripID, EntryIds: entryIDs})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.LedgerEntryID] = append(out[r.LedgerEntryID], r.AssetID)
	}
	return out, nil
}

func getLedgerEntry(ctx context.Context, q *dbgen.Queries, accountID, tripID, id uuid.UUID, forUpdate bool) (finance.LedgerResource, bool, error) {
	var row dbgen.LedgerEntry
	var currency string
	if forUpdate {
		r, err := q.GetLedgerEntryForUpdate(ctx, dbgen.GetLedgerEntryForUpdateParams{AccountID: accountID, TripID: tripID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return finance.LedgerResource{}, false, nil
		}
		if err != nil {
			return finance.LedgerResource{}, false, err
		}
		row, currency = r.LedgerEntry, r.CurrencyCode
	} else {
		r, err := q.GetLedgerEntry(ctx, dbgen.GetLedgerEntryParams{AccountID: accountID, TripID: tripID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return finance.LedgerResource{}, false, nil
		}
		if err != nil {
			return finance.LedgerResource{}, false, err
		}
		row, currency = r.LedgerEntry, r.CurrencyCode
	}
	att, err := attachmentsFor(ctx, q, accountID, tripID, []uuid.UUID{id})
	if err != nil {
		return finance.LedgerResource{}, false, err
	}
	sp, err := splitsFor(ctx, q, accountID, tripID, currency, []uuid.UUID{id})
	if err != nil {
		return finance.LedgerResource{}, false, err
	}
	res, err := toLedgerResource(row, currency, att[id], sp[id])
	return res, err == nil, err
}

// ledgerRepo 绑定到一次写事务。
type ledgerRepo struct {
	scope *pgcore.TxScope
}

var _ finance.LedgerRepo = (*ledgerRepo)(nil)

// NewLedgerUnitOfWork 创建账目写事务入口。
func NewLedgerUnitOfWork(writer *pgcore.Writer) write.UnitOfWork[finance.LedgerRepo] {
	return pgcore.NewUnitOfWork(writer, func(scope *pgcore.TxScope) finance.LedgerRepo {
		return &ledgerRepo{scope: scope}
	})
}

func (r *ledgerRepo) MergeSource() write.MergeSource { return r.scope }

func (r *ledgerRepo) Trip(ctx context.Context, accountID, tripID uuid.UUID) (finance.LedgerTripInfo, bool, error) {
	return ledgerTripInfo(ctx, r.scope.Queries, accountID, tripID)
}

func (r *ledgerRepo) SetTripCurrencyLock(ctx context.Context, accountID, tripID uuid.UUID, lockedAt *time.Time, now time.Time) (trip.Resource, error) {
	row, err := r.scope.Queries.SetTripCurrencyLock(ctx, dbgen.SetTripCurrencyLockParams{AccountID: accountID, ID: tripID, LockedAt: lockedAt, UpdatedAt: now})
	if err != nil {
		return trip.Resource{}, err
	}
	return travelpg.ToTripResource(row)
}

func (r *ledgerRepo) CategoryActive(ctx context.Context, accountID, categoryID uuid.UUID) (bool, error) {
	return r.scope.Queries.ExpenseCategoryActive(ctx, dbgen.ExpenseCategoryActiveParams{AccountID: accountID, ID: categoryID})
}

func (r *ledgerRepo) MissingAssets(ctx context.Context, accountID, tripID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	found, err := r.scope.Queries.ListTripImageAssetIDs(ctx, dbgen.ListTripImageAssetIDsParams{AccountID: accountID, TripID: tripID, Ids: ids})
	if err != nil {
		return nil, err
	}
	present := make(map[uuid.UUID]struct{}, len(found))
	for _, id := range found {
		present[id] = struct{}{}
	}
	var missing []uuid.UUID
	for _, id := range ids {
		if _, ok := present[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

func (r *ledgerRepo) ActiveMembers(ctx context.Context, accountID, tripID uuid.UUID) ([]member.Resource, error) {
	return travelpg.ListActiveMembers(ctx, r.scope.Queries, accountID, tripID)
}

func (r *ledgerRepo) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (finance.LedgerResource, bool, error) {
	return getLedgerEntry(ctx, r.scope.Queries, accountID, tripID, id, false)
}

func (r *ledgerRepo) GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (finance.LedgerResource, bool, error) {
	return getLedgerEntry(ctx, r.scope.Queries, accountID, tripID, id, true)
}

func (r *ledgerRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	exists, err := r.scope.Queries.LedgerEntryIDExists(ctx, dbgen.LedgerEntryIDExistsParams{ID: id, AccountID: r.scope.AccountID})
	return exists != nil && *exists, err
}

// replaceAttachments 整体替换票据引用。
func (r *ledgerRepo) replaceAttachments(ctx context.Context, accountID, tripID, entryID uuid.UUID, ids []uuid.UUID, now time.Time) error {
	if err := r.scope.Queries.DeleteLedgerAttachments(ctx, dbgen.DeleteLedgerAttachmentsParams{AccountID: accountID, TripID: tripID, LedgerEntryID: entryID}); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return r.scope.Queries.InsertLedgerAttachments(ctx, dbgen.InsertLedgerAttachmentsParams{
		AccountID: accountID, TripID: tripID, LedgerEntryID: entryID, CreatedAt: now, AssetIds: ids,
	})
}

// replaceSplits 整体替换分摊份额；存储为 NUMERIC 文本。
func (r *ledgerRepo) replaceSplits(ctx context.Context, accountID, tripID, entryID uuid.UUID, splits []finance.LedgerSplit) error {
	if err := r.scope.Queries.DeleteLedgerSplits(ctx, dbgen.DeleteLedgerSplitsParams{AccountID: accountID, TripID: tripID, LedgerEntryID: entryID}); err != nil {
		return err
	}
	if len(splits) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(splits))
	amounts := make([]string, 0, len(splits))
	for _, s := range splits {
		ids = append(ids, s.MemberID)
		amounts = append(amounts, s.Amount)
	}
	return r.scope.Queries.InsertLedgerSplits(ctx, dbgen.InsertLedgerSplitsParams{AccountID: accountID, TripID: tripID, LedgerEntryID: entryID, MemberIds: ids, Amounts: amounts})
}

func (r *ledgerRepo) Insert(ctx context.Context, accountID uuid.UUID, e finance.LedgerResource) (finance.LedgerResource, error) {
	row, err := r.scope.Queries.InsertLedgerEntry(ctx, dbgen.InsertLedgerEntryParams{
		ID: e.ID, AccountID: accountID, TripID: e.TripID, Kind: string(e.Kind), Amount: e.Amount,
		SplitCount: e.SplitCount, PersonalAmount: e.PersonalAmount, PayerMemberID: e.PayerMemberID, SplitMode: string(e.SplitMode), CategoryID: e.CategoryID,
		OccurredOn: e.OccurredOn.Time(), Notes: e.Notes, RefundedEntryID: nullUUID(e.RefundedEntryID), CreatedAt: e.CreatedAt,
	})
	if err != nil {
		return finance.LedgerResource{}, err
	}
	if err := r.replaceAttachments(ctx, accountID, e.TripID, e.ID, e.AttachmentAssetIDs, e.CreatedAt); err != nil {
		return finance.LedgerResource{}, err
	}
	if err := r.replaceSplits(ctx, accountID, e.TripID, e.ID, e.Splits); err != nil {
		return finance.LedgerResource{}, err
	}
	return toLedgerResource(row, e.CurrencyCode, append([]uuid.UUID{}, e.AttachmentAssetIDs...), append([]finance.LedgerSplit{}, e.Splits...))
}

func (r *ledgerRepo) Update(ctx context.Context, accountID, tripID, id uuid.UUID, v finance.LedgerValues, now time.Time) (finance.LedgerResource, error) {
	row, err := r.scope.Queries.UpdateLedgerEntry(ctx, dbgen.UpdateLedgerEntryParams{
		AccountID: accountID, TripID: tripID, ID: id, Amount: v.Amount,
		SplitCount: v.SplitCount, PersonalAmount: v.PersonalAmount, PayerMemberID: v.PayerMemberID, SplitMode: string(v.SplitMode), CategoryID: v.CategoryID, OccurredOn: v.OccurredOn.Time(),
		Notes: v.Notes, RefundedEntryID: nullUUID(v.RefundedEntryID), UpdatedAt: now,
	})
	if err != nil {
		return finance.LedgerResource{}, err
	}
	if err := r.replaceAttachments(ctx, accountID, tripID, id, v.AttachmentAssetIDs, now); err != nil {
		return finance.LedgerResource{}, err
	}
	if err := r.replaceSplits(ctx, accountID, tripID, id, v.Splits); err != nil {
		return finance.LedgerResource{}, err
	}
	info, _, err := ledgerTripInfo(ctx, r.scope.Queries, accountID, tripID)
	if err != nil {
		return finance.LedgerResource{}, err
	}
	return toLedgerResource(row, info.CurrencyCode, append([]uuid.UUID{}, v.AttachmentAssetIDs...), append([]finance.LedgerSplit{}, v.Splits...))
}

func (r *ledgerRepo) SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (finance.LedgerResource, error) {
	row, err := r.scope.Queries.SoftDeleteLedgerEntry(ctx, dbgen.SoftDeleteLedgerEntryParams{AccountID: accountID, TripID: tripID, ID: id, DeletedAt: &now})
	if err != nil {
		return finance.LedgerResource{}, err
	}
	info, _, err := ledgerTripInfo(ctx, r.scope.Queries, accountID, tripID)
	if err != nil {
		return finance.LedgerResource{}, err
	}
	att, err := attachmentsFor(ctx, r.scope.Queries, accountID, tripID, []uuid.UUID{id})
	if err != nil {
		return finance.LedgerResource{}, err
	}
	sp, err := splitsFor(ctx, r.scope.Queries, accountID, tripID, info.CurrencyCode, []uuid.UUID{id})
	if err != nil {
		return finance.LedgerResource{}, err
	}
	return toLedgerResource(row, info.CurrencyCode, att[id], sp[id])
}

func (r *ledgerRepo) LinkedRefundsForUpdate(ctx context.Context, accountID, tripID, expenseID uuid.UUID) ([]finance.LedgerResource, error) {
	rows, err := r.scope.Queries.ListLinkedRefundsForUpdate(ctx, dbgen.ListLinkedRefundsForUpdateParams{AccountID: accountID, TripID: tripID, ExpenseID: uuid.NullUUID{UUID: expenseID, Valid: true}})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []finance.LedgerResource{}, nil
	}
	info, _, err := ledgerTripInfo(ctx, r.scope.Queries, accountID, tripID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	att, err := attachmentsFor(ctx, r.scope.Queries, accountID, tripID, ids)
	if err != nil {
		return nil, err
	}
	sp, err := splitsFor(ctx, r.scope.Queries, accountID, tripID, info.CurrencyCode, ids)
	if err != nil {
		return nil, err
	}
	out := make([]finance.LedgerResource, 0, len(rows))
	for _, row := range rows {
		res, err := toLedgerResource(row, info.CurrencyCode, att[row.ID], sp[row.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

func (r *ledgerRepo) CountActive(ctx context.Context, accountID, tripID uuid.UUID) (int64, error) {
	return r.scope.Queries.CountActiveLedgerEntries(ctx, dbgen.CountActiveLedgerEntriesParams{AccountID: accountID, TripID: tripID})
}

// LedgerReader 是事务外只读仓储；统计在一个只读 REPEATABLE READ 事务中读取各项聚合。
type LedgerReader struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewLedgerReader 创建只读仓储。
func NewLedgerReader(pool *pgxpool.Pool) *LedgerReader {
	return &LedgerReader{pool: pool, q: dbgen.New(pool)}
}

var _ finance.LedgerReader = (*LedgerReader)(nil)

// Trip 实现 finance.LedgerReader。
func (r *LedgerReader) Trip(ctx context.Context, accountID, tripID uuid.UUID) (finance.LedgerTripInfo, bool, error) {
	return ledgerTripInfo(ctx, r.q, accountID, tripID)
}

// Get 实现 finance.LedgerReader。
func (r *LedgerReader) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (finance.LedgerResource, bool, error) {
	return getLedgerEntry(ctx, r.q, accountID, tripID, id, false)
}

// List 实现 finance.LedgerReader。
func (r *LedgerReader) List(ctx context.Context, accountID, tripID uuid.UUID, q finance.LedgerListQuery) ([]finance.LedgerResource, error) {
	p := dbgen.ListLedgerEntriesParams{
		AccountID: accountID, TripID: tripID, DateFrom: datePtr(q.DateFrom), DateTo: datePtr(q.DateTo),
		CategoryID: nullUUID(q.CategoryID), RefundedEntryID: nullUUID(q.RefundedEntryID), RowLimit: int32(q.Limit),
	}
	if q.Kind != nil {
		k := string(*q.Kind)
		p.Kind = &k
	}
	if q.After != nil {
		on := q.After.OccurredOn.Time()
		p.CursorOccurredOn, p.CursorID = &on, uuid.NullUUID{UUID: q.After.ID, Valid: true}
	}
	rows, err := r.q.ListLedgerEntries(ctx, p)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.LedgerEntry.ID)
	}
	att, err := attachmentsFor(ctx, r.q, accountID, tripID, ids)
	if err != nil {
		return nil, err
	}
	currency := ""
	if len(rows) > 0 {
		currency = rows[0].CurrencyCode
	}
	sp, err := splitsFor(ctx, r.q, accountID, tripID, currency, ids)
	if err != nil {
		return nil, err
	}
	out := make([]finance.LedgerResource, 0, len(rows))
	for _, row := range rows {
		res, err := toLedgerResource(row.LedgerEntry, row.CurrencyCode, att[row.LedgerEntry.ID], sp[row.LedgerEntry.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// Settlement 实现 finance.LedgerReader：每位有效成员的已付与应付聚合。
func (r *LedgerReader) Settlement(ctx context.Context, accountID, tripID uuid.UUID) ([]finance.MemberAggregate, error) {
	rows, err := r.q.TripMemberSettlement(ctx, dbgen.TripMemberSettlementParams{AccountID: accountID, TripID: tripID})
	if err != nil {
		return nil, err
	}
	out := make([]finance.MemberAggregate, 0, len(rows))
	for _, row := range rows {
		out = append(out, finance.MemberAggregate{MemberID: row.MemberID, Name: row.Name, IsSelf: row.IsSelf, Paid: row.PaidAmount, Owed: row.OwedAmount})
	}
	return out, nil
}

// Statistics 实现 finance.LedgerReader：旅行信息、筛选合计、整趟合计、分类与每日聚合在同一个只读一致性事务中读取。
func (r *LedgerReader) Statistics(ctx context.Context, accountID, tripID uuid.UUID, q finance.StatisticsQuery) (finance.StatisticsData, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return finance.StatisticsData{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qs := dbgen.New(tx)

	info, found, err := ledgerTripInfo(ctx, qs, accountID, tripID)
	if err != nil || !found {
		return finance.StatisticsData{}, false, err
	}
	data := finance.StatisticsData{Trip: info}
	if info.DeletedAt != nil {
		return data, true, nil
	}
	from, to, cat := datePtr(q.DateFrom), datePtr(q.DateTo), nullUUID(q.CategoryID)

	filtered, err := qs.LedgerFilteredTotals(ctx, dbgen.LedgerFilteredTotalsParams{AccountID: accountID, TripID: tripID, DateFrom: from, DateTo: to, CategoryID: cat})
	if err != nil {
		return finance.StatisticsData{}, false, err
	}
	data.FilteredExpense, data.FilteredRefund, data.FilteredCount = filtered.ExpenseAmount, filtered.RefundAmount, filtered.EntryCount

	whole, err := qs.LedgerTripTotals(ctx, dbgen.LedgerTripTotalsParams{AccountID: accountID, TripID: tripID})
	if err != nil {
		return finance.StatisticsData{}, false, err
	}
	data.TripExpense, data.TripRefund = whole.ExpenseAmount, whole.RefundAmount

	cats, err := qs.LedgerCategoryTotals(ctx, dbgen.LedgerCategoryTotalsParams{AccountID: accountID, TripID: tripID, DateFrom: from, DateTo: to, CategoryID: cat})
	if err != nil {
		return finance.StatisticsData{}, false, err
	}
	data.Categories = make([]finance.CategoryAggregate, 0, len(cats))
	for _, c := range cats {
		data.Categories = append(data.Categories, finance.CategoryAggregate{
			CategoryID: c.CategoryID, Name: c.Name, Icon: c.Icon, SortOrder: c.SortOrder,
			FilteredExpense: c.FilteredExpense, FilteredRefund: c.FilteredRefund, TripExpense: c.TripExpense, TripRefund: c.TripRefund,
		})
	}

	daily, err := qs.LedgerDailyTotals(ctx, dbgen.LedgerDailyTotalsParams{
		AccountID: accountID, TripID: tripID, DateFrom: from, DateTo: to, CategoryID: cat, CursorDate: datePtr(q.DailyAfter), RowLimit: int32(q.DailyLimit),
	})
	if err != nil {
		return finance.StatisticsData{}, false, err
	}
	data.Daily = make([]finance.DailyAggregate, 0, len(daily))
	for _, d := range daily {
		data.Daily = append(data.Daily, finance.DailyAggregate{Date: types.DateOf(d.OccurredOn), Expense: d.ExpenseAmount, Refund: d.RefundAmount})
	}
	return data, true, tx.Commit(ctx)
}
