package content

import (
	"context"
	"github.com/google/uuid"
	"strings"
	"time"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/types"
	"unicode/utf8"
)

type TripInfo struct {
	Timezone  string
	DeletedAt *time.Time
}
type TripReader interface {
	Trip(context.Context, uuid.UUID, uuid.UUID) (TripInfo, bool, error)
}

func LoadTrip(ctx context.Context, r TripReader, accountID, tripID uuid.UUID) (TripInfo, error) {
	v, ok, err := r.Trip(ctx, accountID, tripID)
	if err != nil {
		return v, apperr.Internal(err)
	}
	if !ok {
		return v, apperr.NotFound()
	}
	if v.DeletedAt != nil {
		return v, apperr.Gone("TRIP_DELETED", "旅行已在回收站中")
	}
	return v, nil
}

type AssetInfo struct {
	MediaType string
	DeletedAt *time.Time
}
type AssetReader interface {
	Asset(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (AssetInfo, bool, error)
}

func CheckAsset(ctx context.Context, r AssetReader, accountID, tripID, id uuid.UUID, imageOnly bool) error {
	v, ok, err := r.Asset(ctx, accountID, tripID, id)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.NotFound()
	}
	if v.DeletedAt != nil {
		return Invalid("asset_id", "资产已删除")
	}
	if v.MediaType == "image/jpeg" || v.MediaType == "image/png" || v.MediaType == "image/webp" || (!imageOnly && v.MediaType == "application/pdf") {
		return nil
	}
	return Invalid("asset_id", "资产类型不适用")
}
func Invalid(field, message string) error {
	return apperr.Validation(apperr.Field(field, "INVALID_REFERENCE", message))
}
func Text(field string, value *string, min, max int) error {
	if value == nil {
		return nil
	}
	n := utf8.RuneCountInString(*value)
	if !utf8.ValidString(*value) || n < min || n > max || (min > 0 && strings.TrimSpace(*value) == "") {
		return apperr.Validation(apperr.Field(field, "INVALID", "文本长度或内容无效"))
	}
	return nil
}
func Local(field string, value *types.LocalDateTime) error {
	if value == nil {
		return nil
	}
	if _, err := types.ParseLocalDateTime(string(*value)); err != nil {
		return apperr.Validation(apperr.Field(field, "INVALID", err.Error()))
	}
	return nil
}
func Date(field string, value types.Date) error {
	if _, err := types.ParseDate(string(value)); err != nil {
		return apperr.Validation(apperr.Field(field, "INVALID", err.Error()))
	}
	return nil
}
func Gone() error { return apperr.Gone("RESOURCE_GONE", "资源已删除") }
