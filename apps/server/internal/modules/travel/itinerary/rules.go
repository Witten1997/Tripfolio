package itinerary

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/metadata"
)

const (
	maxTitleChars     = 200
	maxPlaceNameChars = 200
	maxAddressChars   = 500
	maxNotesChars     = 10000
	coordinateScale   = 6
)

// 错误代码（接口设计 1.4）。
const (
	codeTripDeleted      = "TRIP_DELETED"
	codeResourceGone     = "RESOURCE_GONE"
	codeIDAlreadyUsed    = "ID_ALREADY_USED"
	codeOrderChanged     = "ORDER_CHANGED"
	codeCurrencyMismatch = "CURRENCY_MISMATCH"
)

func tripDeleted() *apperr.Error  { return apperr.Gone(codeTripDeleted, "旅行已在回收站中") }
func resourceGone() *apperr.Error { return apperr.Gone(codeResourceGone, "行程项目已删除") }

// tripReader 是 loadTrip 需要的最小能力，Repo 与 Reader 都满足。
type tripReader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
}

// loadTrip 读取并校验所属旅行：不存在或非本人 404，回收站中 410 TRIP_DELETED。
func loadTrip(ctx context.Context, r tripReader, accountID, tripID uuid.UUID) (TripInfo, error) {
	info, found, err := r.Trip(ctx, accountID, tripID)
	if err != nil {
		return TripInfo{}, apperr.Internal(err)
	}
	if !found {
		return TripInfo{}, apperr.NotFound()
	}
	if info.DeletedAt != nil {
		return TripInfo{}, tripDeleted()
	}
	return info, nil
}

func addField(fields *[]apperr.FieldError, e *apperr.FieldError) {
	if e != nil {
		*fields = append(*fields, *e)
	}
}

func validateTitle(title string) (string, *apperr.FieldError) {
	title = strings.TrimSpace(title)
	if n := utf8.RuneCountInString(title); n < 1 || n > maxTitleChars {
		e := apperr.Field("title", "INVALID", fmt.Sprintf("标题须为 1–%d 个字符", maxTitleChars))
		return "", &e
	}
	return title, nil
}

func validateText(field, value string, max int) *apperr.FieldError {
	if utf8.RuneCountInString(value) > max {
		e := apperr.Field(field, "TOO_LONG", fmt.Sprintf("最多 %d 个字符", max))
		return &e
	}
	if !utf8.ValidString(value) {
		e := apperr.Field(field, "INVALID", "必须是有效的 UTF-8 文本")
		return &e
	}
	return nil
}

// optionalText 校验可空文本字段，返回其有效值（nil 时为空串）。
func optionalText(field string, value *string, max int, fields *[]apperr.FieldError) string {
	if value == nil {
		return ""
	}
	addField(fields, validateText(field, *value, max))
	return *value
}

func optionalStatus(raw *string, fields *[]apperr.FieldError) Status {
	if raw == nil {
		return StatusPending
	}
	s := Status(*raw)
	if !s.Valid() {
		*fields = append(*fields, apperr.Field("status", "INVALID", "状态须为 pending、completed 或 skipped"))
	}
	return s
}

func parseDate(field, raw string) (types.Date, *apperr.FieldError) {
	d, err := types.ParseDate(raw)
	if err != nil {
		e := apperr.Field(field, "INVALID", "日期格式必须是 YYYY-MM-DD")
		return "", &e
	}
	return d, nil
}

// parseLocal 解析可空当地时间；nil 表示缺省或清空，返回 nil。
func parseLocal(field string, raw *string, fields *[]apperr.FieldError) *types.LocalDateTime {
	if raw == nil {
		return nil
	}
	l, err := types.ParseLocalDateTime(*raw)
	if err != nil {
		*fields = append(*fields, apperr.Field(field, "INVALID", "时间格式必须是 YYYY-MM-DDTHH:mm:ss"))
		return nil
	}
	return &l
}

// validateDuration 校验计划时长；hasEnd 为 true 表示同一命令还提供了 planned_end_local。
func validateDuration(raw *int32, hasEnd bool, fields *[]apperr.FieldError) *int32 {
	if raw == nil {
		return nil
	}
	if hasEnd {
		*fields = append(*fields, apperr.Field("planned_duration_minutes", "EXCLUSIVE", "计划结束时间与时长不能同时提供"))
		return raw
	}
	if *raw < 1 {
		*fields = append(*fields, apperr.Field("planned_duration_minutes", "INVALID", "时长必须为正整数分钟"))
	}
	return raw
}

func validateTimeOrder(endField string, start, end *types.LocalDateTime, fields *[]apperr.FieldError) {
	if start == nil || end == nil {
		return
	}
	if end.Time().Before(start.Time()) {
		*fields = append(*fields, apperr.Field(endField, "TIME_ORDER", "结束时间不能早于开始时间"))
	}
}

// validateCoordinates 校验坐标成对、范围与精度；返回四舍五入到 6 位小数的坐标。
func validateCoordinates(lat, lng *float64, fields *[]apperr.FieldError) (*float64, *float64) {
	switch {
	case lat == nil && lng == nil:
		return nil, nil
	case lat == nil:
		*fields = append(*fields, apperr.Field("latitude", "REQUIRED", "纬度与经度必须成对提供"))
		return nil, lng
	case lng == nil:
		*fields = append(*fields, apperr.Field("longitude", "REQUIRED", "纬度与经度必须成对提供"))
		return lat, nil
	}
	ok := true
	if *lat < -90 || *lat > 90 {
		*fields = append(*fields, apperr.Field("latitude", "INVALID", "纬度须在 -90 到 90 之间"))
		ok = false
	} else if exceedsScale(*lat, coordinateScale) {
		*fields = append(*fields, apperr.Field("latitude", "PRECISION", "纬度最多 6 位小数"))
		ok = false
	}
	if *lng < -180 || *lng > 180 {
		*fields = append(*fields, apperr.Field("longitude", "INVALID", "经度须在 -180 到 180 之间"))
		ok = false
	} else if exceedsScale(*lng, coordinateScale) {
		*fields = append(*fields, apperr.Field("longitude", "PRECISION", "经度最多 6 位小数"))
		ok = false
	}
	if !ok {
		return lat, lng
	}
	rlat, rlng := roundScale(*lat, coordinateScale), roundScale(*lng, coordinateScale)
	return &rlat, &rlng
}

// validatePatchCoordinates 校验局部更新的坐标：只提交其中一个字段时也视为成对要求，
// 因为坐标在数据库层要求同时为空或同时非空。
func validatePatchCoordinates(p Patch, fields *[]apperr.FieldError) (*float64, *float64) {
	if !p.LatitudeSet && !p.LongitudeSet {
		return p.Latitude, p.Longitude
	}
	if p.LatitudeSet != p.LongitudeSet {
		missing := "longitude"
		if !p.LatitudeSet {
			missing = "latitude"
		}
		*fields = append(*fields, apperr.Field(missing, "REQUIRED", "修改坐标时纬度与经度必须一起提交"))
		return p.Latitude, p.Longitude
	}
	// 两者都提交：可能是成对清空（都为 nil）或成对设置。
	if p.Latitude == nil && p.Longitude == nil {
		return nil, nil
	}
	return validateCoordinates(p.Latitude, p.Longitude, fields)
}

func validateCurrencyCode(code *string, fields *[]apperr.FieldError) {
	if code == nil {
		return
	}
	if _, ok := metadata.MinorUnits(*code); !ok {
		*fields = append(*fields, apperr.Field("currency_code", "INVALID", "不支持的币种"))
	}
}

// compactAmount 校验金额格式并化简（用于指纹）；金额非空时要求提供币种。
func compactAmount(amount, currency *string, fields *[]apperr.FieldError) *string {
	if amount == nil {
		return nil
	}
	out, err := money.Compact(*amount)
	if err != nil {
		*fields = append(*fields, apperr.Field("estimated_amount", "INVALID", budgetMessage(err)))
		return amount
	}
	if currency == nil {
		*fields = append(*fields, apperr.Field("currency_code", "REQUIRED", "提供预计费用时必须同时提供币种"))
	}
	return &out
}

func budgetMessage(err error) string {
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

// canonicalAmount 在事务内按旅行币种规范化预计费用；币种与旅行不符返回 422 CURRENCY_MISMATCH。
func canonicalAmount(compact, requestCurrency *string, tripCurrency string) (*string, error) {
	if compact == nil {
		return nil, nil
	}
	if requestCurrency == nil || *requestCurrency != tripCurrency {
		return nil, apperr.Unprocessable(codeCurrencyMismatch, "预计费用币种必须与旅行币种一致")
	}
	units, ok := metadata.MinorUnits(tripCurrency)
	if !ok {
		return nil, apperr.Validation(apperr.Field("currency_code", "INVALID", "不支持的币种"))
	}
	out, err := money.Canonicalize(*compact, units)
	if err != nil {
		return nil, apperr.Validation(apperr.Field("estimated_amount", "INVALID", budgetMessage(err)))
	}
	return &out, nil
}

// validateMergedValues 在合并后再校验互斥与顺序约束，避免局部更新绕过创建时的规则。
func validateMergedValues(v Values) error {
	var fields []apperr.FieldError
	if v.PlannedEndLocal != nil && v.PlannedDurationMinutes != nil {
		fields = append(fields, apperr.Field("planned_duration_minutes", "EXCLUSIVE", "计划结束时间与时长不能同时存在"))
	}
	if v.PlannedStartLocal != nil && v.PlannedEndLocal != nil && v.PlannedEndLocal.Time().Before(v.PlannedStartLocal.Time()) {
		fields = append(fields, apperr.Field("planned_end_local", "TIME_ORDER", "计划结束时间不能早于开始时间"))
	}
	if v.ActualStartLocal != nil && v.ActualEndLocal != nil && v.ActualEndLocal.Time().Before(v.ActualStartLocal.Time()) {
		fields = append(fields, apperr.Field("actual_end_local", "TIME_ORDER", "实际结束时间不能早于开始时间"))
	}
	if (v.Latitude == nil) != (v.Longitude == nil) {
		missing := "longitude"
		if v.Latitude == nil {
			missing = "latitude"
		}
		fields = append(fields, apperr.Field(missing, "REQUIRED", "纬度与经度必须成对存在"))
	}
	if len(fields) > 0 {
		return apperr.Validation(fields...)
	}
	return nil
}

func outsideTripDates(on types.Date, info TripInfo) bool {
	return on.Before(info.StartDate) || on.After(info.EndDate)
}

// exceedsScale 判断 v 的小数位是否超过 scale 位。
func exceedsScale(v float64, scale int) bool {
	factor := math.Pow(10, float64(scale))
	scaled := v * factor
	return math.Abs(scaled-math.Round(scaled)) > 1e-6
}

func roundScale(v float64, scale int) float64 {
	factor := math.Pow(10, float64(scale))
	return math.Round(v*factor) / factor
}
