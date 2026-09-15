package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/account"
	"tripfolio/server/internal/modules/assets"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/packing"
	"tripfolio/server/internal/modules/travel/todo"
	"tripfolio/server/internal/modules/travel/trip"
	"tripfolio/server/internal/transport/httpapi/generated"
	"tripfolio/server/internal/transport/httpapi/middleware"
)

// Handler 实现生成的 StrictServerInterface。它只做分发与模型转换，业务状态与规则留在模块服务中；
// 各业务的方法分散在对应文件（metadata.go、account.go、finance.go、travel_*.go 等）。
type Handler struct {
	logger      *slog.Logger
	metadata    metadata.Metadata
	identity    *account.IdentityService
	sessions    *account.SessionService
	profile     *account.ProfileService
	categories  *finance.CategoryService
	ledger      *finance.LedgerService
	statistics  *finance.StatisticsService
	assets      *assets.Service
	geo         *geo.Service
	trips       *trip.Service
	itinerary   *itinerary.Service
	packing     *packing.Service
	todos       *todo.Service
	cookies     CookieSettings
	corsOrigins []string
}

var _ generated.StrictServerInterface = (*Handler)(nil)

// responseError 处理业务方法返回的 error：业务错误映射为 problem+json，其余记录日志并返回 500。
func (h *Handler) responseError(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok {
		if e.Status >= 500 {
			h.logger.ErrorContext(r.Context(), "处理器返回服务端错误",
				"error", err, "cause", errors.Unwrap(e), "code", e.Code, "method", r.Method, "path", r.URL.Path, "request_id", middleware.RequestIDFrom(r.Context()))
		}
		writeAppError(w, r, e)
		return
	}
	h.logger.ErrorContext(r.Context(), "处理器返回未映射的错误",
		"error", err, "method", r.Method, "path", r.URL.Path, "request_id", middleware.RequestIDFrom(r.Context()))
	WriteProblem(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "")
}
