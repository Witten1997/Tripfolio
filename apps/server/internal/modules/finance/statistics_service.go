package finance

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/metadata"
)

const (
	// DefaultDailyLimit 是每日明细的默认页大小（接口设计 3.9）。
	DefaultDailyLimit = 31
	shareScale        = 4
)

// StatisticsService 实现开支统计（接口设计 2.5、3.8；数据库设计 §5）：
// 净额 = 支出 − 退款；预算余额基于整趟旅行；占比仅在总净额 > 0 且各分类净额非负时提供。
type StatisticsService struct {
	reader  LedgerReader
	cursors paging.Codec
}

// NewStatisticsService 创建服务。
func NewStatisticsService(reader LedgerReader, cursors paging.Codec) *StatisticsService {
	return &StatisticsService{reader: reader, cursors: cursors}
}

// dailyPosition 是每日明细的游标位置：上一页最后一天。
type dailyPosition struct {
	Date types.Date `json:"date"`
}

func statisticsScope(tripID uuid.UUID, q StatisticsQuery) string {
	s := fmt.Sprintf("statistics|trip=%s", tripID)
	if q.DateFrom != nil {
		s += "|from=" + string(*q.DateFrom)
	}
	if q.DateTo != nil {
		s += "|to=" + string(*q.DateTo)
	}
	if q.CategoryID != nil {
		s += "|category=" + q.CategoryID.String()
	}
	return s
}

func dailyLimit(requested int) (int, *apperr.FieldError) {
	if requested == 0 {
		return DefaultDailyLimit, nil
	}
	if requested < 1 || requested > paging.MaxLimit {
		e := apperr.Field("daily_limit", "INVALID", fmt.Sprintf("daily_limit 须在 1–%d 之间", paging.MaxLimit))
		return 0, &e
	}
	return requested, nil
}

// Get 返回旅行开支统计；旅行不存在或非本人 404，回收站中 410 TRIP_DELETED。
func (s *StatisticsService) Get(ctx context.Context, a actor.Actor, tripID uuid.UUID, f StatisticsFilters) (Statistics, error) {
	var fields []apperr.FieldError
	q := StatisticsQuery{CategoryID: f.CategoryID}
	var ferr *apperr.FieldError
	if f.DateFrom != "" {
		q.DateFrom, ferr = parseLedgerDate("date_from", &f.DateFrom)
		addField(&fields, ferr)
	}
	if f.DateTo != "" {
		q.DateTo, ferr = parseLedgerDate("date_to", &f.DateTo)
		addField(&fields, ferr)
	}
	addField(&fields, validateDateRange(q.DateFrom, q.DateTo))
	limit, ferr := dailyLimit(f.DailyLimit)
	addField(&fields, ferr)
	if len(fields) > 0 {
		return Statistics{}, apperr.Validation(fields...)
	}
	scope := statisticsScope(tripID, q)
	if f.DailyCursor != "" {
		var p dailyPosition
		if err := s.cursors.Decode(a.AccountID, scope, f.DailyCursor, &p); err != nil {
			return Statistics{}, paging.InvalidCursor()
		}
		q.DailyAfter = &p.Date
	}
	q.DailyLimit = limit + 1
	data, found, err := s.reader.Statistics(ctx, a.AccountID, tripID, q)
	if err != nil {
		return Statistics{}, apperr.Internal(err)
	}
	if !found {
		return Statistics{}, apperr.NotFound()
	}
	if data.Trip.DeletedAt != nil {
		return Statistics{}, tripDeleted()
	}
	units, ok := metadata.MinorUnits(data.Trip.CurrencyCode)
	if !ok {
		units = money.Scale
	}
	out, err := buildStatistics(data, q, units)
	if err != nil {
		return Statistics{}, apperr.Internal(err)
	}
	if len(data.Daily) > limit {
		token, err := s.cursors.Encode(a.AccountID, scope, dailyPosition{Date: data.Daily[limit-1].Date})
		if err != nil {
			return Statistics{}, apperr.Internal(err)
		}
		out.Daily.NextCursor = &token
		out.Daily.Items = out.Daily.Items[:limit]
	}
	return out, nil
}

// buildStatistics 把原始聚合换算为响应：净额、预算对比、分类占比与每日明细（不含游标）。
func buildStatistics(data StatisticsData, q StatisticsQuery, units int) (Statistics, error) {
	fmtMoney := func(d money.Decimal) string { return d.Format(units) }
	filteredExpense, err := parseAmount(data.FilteredExpense)
	if err != nil {
		return Statistics{}, fmt.Errorf("筛选支出合计 %q 无法解析: %w", data.FilteredExpense, err)
	}
	filteredRefund, err := parseAmount(data.FilteredRefund)
	if err != nil {
		return Statistics{}, fmt.Errorf("筛选退款合计 %q 无法解析: %w", data.FilteredRefund, err)
	}
	filteredNet := filteredExpense.Sub(filteredRefund)
	tripExpense, err := parseAmount(data.TripExpense)
	if err != nil {
		return Statistics{}, fmt.Errorf("旅行支出合计 %q 无法解析: %w", data.TripExpense, err)
	}
	tripRefund, err := parseAmount(data.TripRefund)
	if err != nil {
		return Statistics{}, fmt.Errorf("旅行退款合计 %q 无法解析: %w", data.TripRefund, err)
	}
	tripNet := tripExpense.Sub(tripRefund)

	budget := TripBudget{BudgetAmount: data.Trip.BudgetAmount, TripNetAmount: fmtMoney(tripNet)}
	if data.Trip.BudgetAmount != nil {
		b, err := parseAmount(*data.Trip.BudgetAmount)
		if err != nil {
			return Statistics{}, fmt.Errorf("总预算 %q 无法解析: %w", *data.Trip.BudgetAmount, err)
		}
		remaining := fmtMoney(b.Sub(tripNet))
		overspent := tripNet.Sub(b)
		if overspent.Sign() < 0 {
			overspent = money.Zero()
		}
		over := fmtMoney(overspent)
		budget.RemainingAmount, budget.OverspentAmount = &remaining, &over
	}

	type categoryNet struct {
		totals CategoryTotals
		net    money.Decimal
	}
	categories := make([]categoryNet, 0, len(data.Categories))
	ratioAvailable := filteredNet.Sign() > 0
	for _, c := range data.Categories {
		expense, err := parseAmount(c.FilteredExpense)
		if err != nil {
			return Statistics{}, fmt.Errorf("分类 %s 支出 %q 无法解析: %w", c.CategoryID, c.FilteredExpense, err)
		}
		refund, err := parseAmount(c.FilteredRefund)
		if err != nil {
			return Statistics{}, fmt.Errorf("分类 %s 退款 %q 无法解析: %w", c.CategoryID, c.FilteredRefund, err)
		}
		tripCatExpense, err := parseAmount(c.TripExpense)
		if err != nil {
			return Statistics{}, fmt.Errorf("分类 %s 旅行支出 %q 无法解析: %w", c.CategoryID, c.TripExpense, err)
		}
		tripCatRefund, err := parseAmount(c.TripRefund)
		if err != nil {
			return Statistics{}, fmt.Errorf("分类 %s 旅行退款 %q 无法解析: %w", c.CategoryID, c.TripRefund, err)
		}
		net := expense.Sub(refund)
		if net.Sign() < 0 {
			ratioAvailable = false
		}
		categories = append(categories, categoryNet{
			totals: CategoryTotals{
				CategoryID: c.CategoryID, Name: c.Name, Icon: c.Icon,
				ExpenseAmount: fmtMoney(expense), RefundAmount: fmtMoney(refund), NetAmount: fmtMoney(net),
				TripCategoryNetAmount: fmtMoney(tripCatExpense.Sub(tripCatRefund)),
			},
			net: net,
		})
	}
	byCategory := make([]CategoryTotals, 0, len(categories))
	for _, c := range categories {
		if ratioAvailable {
			share := roundShare(c.net.Ratio(filteredNet))
			c.totals.Share = &share
		}
		byCategory = append(byCategory, c.totals)
	}

	daily := make([]DailyTotals, 0, len(data.Daily))
	for _, d := range data.Daily {
		expense, err := parseAmount(d.Expense)
		if err != nil {
			return Statistics{}, fmt.Errorf("%s 支出 %q 无法解析: %w", d.Date, d.Expense, err)
		}
		refund, err := parseAmount(d.Refund)
		if err != nil {
			return Statistics{}, fmt.Errorf("%s 退款 %q 无法解析: %w", d.Date, d.Refund, err)
		}
		daily = append(daily, DailyTotals{
			Date: d.Date, ExpenseAmount: fmtMoney(expense), RefundAmount: fmtMoney(refund), NetAmount: fmtMoney(expense.Sub(refund)),
		})
	}

	return Statistics{
		Scope:        StatisticsScope{DateFrom: q.DateFrom, DateTo: q.DateTo, CategoryID: q.CategoryID},
		CurrencyCode: data.Trip.CurrencyCode,
		FilteredTotals: Totals{
			ExpenseAmount: fmtMoney(filteredExpense), RefundAmount: fmtMoney(filteredRefund),
			NetAmount: fmtMoney(filteredNet), EntryCount: data.FilteredCount,
		},
		TripBudget:     budget,
		ByCategory:     byCategory,
		RatioAvailable: ratioAvailable,
		Daily:          paging.Page[DailyTotals]{Items: daily},
	}, nil
}

// roundShare 把占比四舍五入到 4 位小数并夹在 0–1 之间。
func roundShare(v float64) float64 {
	factor := math.Pow(10, shareScale)
	r := math.Round(v*factor) / factor
	return math.Min(math.Max(r, 0), 1)
}
