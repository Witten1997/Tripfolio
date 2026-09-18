package finance

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/modules/travel/trip"
)

const maxLedgerNotesChars = 4000

// 错误代码（接口设计 1.4）。
const (
	codeTripDeleted          = "TRIP_DELETED"
	codeResourceGone         = "RESOURCE_GONE"
	codeIDAlreadyUsed        = "ID_ALREADY_USED"
	codeCurrencyMismatch     = "CURRENCY_MISMATCH"
	codeRefundAmountExceeded = "REFUND_AMOUNT_EXCEEDED"
	codeInvalidReference     = "INVALID_REFERENCE"
)

func tripDeleted() *apperr.Error { return apperr.Gone(codeTripDeleted, "旅行已在回收站中") }
func ledgerGone() *apperr.Error  { return apperr.Gone(codeResourceGone, "账目已删除") }
func invalidReference(detail string) *apperr.Error {
	return apperr.Unprocessable(codeInvalidReference, detail)
}

// ledgerTripReader 是 loadLedgerTrip 需要的最小能力，LedgerRepo 与 LedgerReader 都满足。
type ledgerTripReader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (LedgerTripInfo, bool, error)
}

// loadLedgerTrip 读取并校验所属旅行：不存在或非本人 404，回收站中 410 TRIP_DELETED。
func loadLedgerTrip(ctx context.Context, r ledgerTripReader, accountID, tripID uuid.UUID) (LedgerTripInfo, error) {
	info, found, err := r.Trip(ctx, accountID, tripID)
	if err != nil {
		return LedgerTripInfo{}, apperr.Internal(err)
	}
	if !found {
		return LedgerTripInfo{}, apperr.NotFound()
	}
	if info.DeletedAt != nil {
		return LedgerTripInfo{}, tripDeleted()
	}
	return info, nil
}

// todayIn 取旅行时区的今天；时区无法加载时退回 UTC。
func todayIn(now time.Time, tz string) types.Date {
	today, err := types.TodayIn(now, tz)
	if err != nil {
		return types.DateOf(now.UTC())
	}
	return today
}

func addField(fields *[]apperr.FieldError, e *apperr.FieldError) {
	if e != nil {
		*fields = append(*fields, *e)
	}
}

func validateKind(raw string) (LedgerKind, *apperr.FieldError) {
	k := LedgerKind(raw)
	if !k.Valid() {
		e := apperr.Field("kind", "INVALID", "类型须为 expense 或 refund")
		return "", &e
	}
	return k, nil
}

func moneyMessage(err error) string {
	switch {
	case errors.Is(err, money.ErrNegative):
		return "金额不能为负数"
	case errors.Is(err, money.ErrScaleExceeded):
		return "金额小数位超过币种允许的位数"
	case errors.Is(err, money.ErrTooLarge):
		return "金额超出可记录范围"
	default:
		return "金额格式不正确"
	}
}

// compactMoney 做币种无关的格式校验并返回最简形式，用于指纹；小数位按币种的校验在事务内完成。
func compactMoney(raw string) (string, *apperr.FieldError) {
	out, err := money.Compact(raw)
	if err != nil {
		e := apperr.Field("amount", "INVALID", moneyMessage(err))
		return "", &e
	}
	if out == "0" {
		e := apperr.Field("amount", "INVALID", "金额必须大于 0")
		return "", &e
	}
	return out, nil
}

// canonicalLedgerAmount 在事务内按旅行币种规范化金额；请求提供的币种与旅行不符返回 422 CURRENCY_MISMATCH。
func canonicalLedgerAmount(compact string, requestCurrency *string, tripCurrency string) (string, error) {
	if requestCurrency != nil && *requestCurrency != tripCurrency {
		return "", apperr.Unprocessable(codeCurrencyMismatch, "账目币种必须与旅行币种一致")
	}
	units, ok := metadata.MinorUnits(tripCurrency)
	if !ok {
		return "", apperr.Validation(apperr.Field("currency_code", "INVALID", "不支持的币种"))
	}
	out, err := money.Canonicalize(compact, units)
	if err != nil {
		return "", apperr.Validation(apperr.Field("amount", "INVALID", moneyMessage(err)))
	}
	return out, nil
}

func personalLedgerAmount(amount, currency string, count int32) (string, error) {
	units, ok := metadata.MinorUnits(currency)
	if !ok {
		return "", apperr.Validation(apperr.Field("currency_code", "INVALID", "不支持的币种"))
	}
	value, err := money.ParseDecimal(amount)
	if err != nil {
		return "", err
	}
	return value.DivideRound(int(count), units).Format(units), nil
}

func validateSplitCount(kind LedgerKind, count int32) *apperr.FieldError {
	if count < 1 || count > 9999 || (kind == KindRefund && count != 1) {
		e := apperr.Field("split_count", "INVALID", "支出均摊人数须为 1 至 9999；退款不可均摊")
		return &e
	}
	return nil
}

func validateLedgerCurrency(code *string) *apperr.FieldError {
	if code == nil {
		return nil
	}
	if _, ok := metadata.MinorUnits(*code); !ok {
		e := apperr.Field("currency_code", "INVALID", "不支持的币种")
		return &e
	}
	return nil
}

func parseLedgerDate(field string, raw *string) (*types.Date, *apperr.FieldError) {
	if raw == nil {
		return nil, nil
	}
	d, err := types.ParseDate(*raw)
	if err != nil {
		e := apperr.Field(field, "INVALID", "日期格式必须是 YYYY-MM-DD")
		return nil, &e
	}
	return &d, nil
}

func validateLedgerNotes(value string) *apperr.FieldError {
	if utf8.RuneCountInString(value) > maxLedgerNotesChars {
		e := apperr.Field("notes", "TOO_LONG", fmt.Sprintf("最多 %d 个字符", maxLedgerNotesChars))
		return &e
	}
	if !utf8.ValidString(value) {
		e := apperr.Field("notes", "INVALID", "必须是有效的 UTF-8 文本")
		return &e
	}
	return nil
}

// validateAttachments 校验票据数组：最多 10 个、不含空 ID、不重复；返回去掉 nil 的副本。
func validateAttachments(ids []uuid.UUID) ([]uuid.UUID, *apperr.FieldError) {
	if len(ids) > MaxLedgerAttachments {
		e := apperr.Field("attachment_asset_ids", "TOO_MANY", fmt.Sprintf("票据最多 %d 张", MaxLedgerAttachments))
		return nil, &e
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for i, id := range ids {
		if id == uuid.Nil {
			e := apperr.Field(fmt.Sprintf("attachment_asset_ids[%d]", i), "INVALID", "必须是 UUID")
			return nil, &e
		}
		if _, dup := seen[id]; dup {
			e := apperr.Field(fmt.Sprintf("attachment_asset_ids[%d]", i), "DUPLICATE", "票据重复")
			return nil, &e
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

// validateDateRange 校验闭区间：date_to 不早于 date_from。
func validateDateRange(from, to *types.Date) *apperr.FieldError {
	if from != nil && to != nil && to.Before(*from) {
		e := apperr.Field("date_to", "DATE_ORDER", "结束日期不能早于开始日期")
		return &e
	}
	return nil
}

// parseAmount 解析规范或 NUMERIC 文本金额；空串视为 0。
func parseAmount(s string) (money.Decimal, error) {
	if s == "" {
		return money.Zero(), nil
	}
	return money.ParseDecimal(s)
}

// sumAmounts 汇总 entries 的金额，跳过 exclude。
func sumAmounts(entries []LedgerResource, exclude uuid.UUID) (money.Decimal, error) {
	total := money.Zero()
	for _, e := range entries {
		if e.ID == exclude {
			continue
		}
		d, err := parseAmount(e.Amount)
		if err != nil {
			return money.Decimal{}, fmt.Errorf("账目 %s 金额 %q 无法解析: %w", e.ID, e.Amount, err)
		}
		total = total.Add(d)
	}
	return total, nil
}

func ledgerRef(r LedgerResource) write.EntityRef {
	return write.Ref(EntityTypeLedger, r.ID, int64(r.Version))
}

func recordLedger(scope write.Scope, r LedgerResource, kind write.ChangeKind, fields []string) {
	tripID := r.TripID
	change := write.Change{EntityType: EntityTypeLedger, EntityID: r.ID, TripID: &tripID, Version: int64(r.Version), Kind: kind}
	if kind == write.ChangeUpsert {
		change.Snapshot = r
		change.ChangedFields = fields
	}
	scope.Record(change)
}

// recordTripCurrencyLock 登记旅行因币种锁定或解锁产生的变更：只有 currency_locked_at 变化。
func recordTripCurrencyLock(scope write.Scope, t trip.Resource) {
	id := t.ID
	scope.Record(write.Change{
		EntityType: trip.EntityType, EntityID: t.ID, TripID: &id, Version: int64(t.Version),
		Kind: write.ChangeUpsert, Snapshot: t, ChangedFields: []string{"currency_locked_at"},
	})
}
