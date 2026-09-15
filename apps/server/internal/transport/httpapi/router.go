package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/share"

	"github.com/go-chi/chi/v5"

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

// Deps 是路由需要的全部依赖，由 bootstrap 显式装配。
type Deps struct {
	Logger      *slog.Logger
	Metadata    metadata.Metadata
	Readiness   Readiness
	CORSOrigins []string
	Cookies     CookieSettings
	Identity    *account.IdentityService
	Sessions    *account.SessionService
	Profile     *account.ProfileService
	Categories  *finance.CategoryService
	Trips       *trip.Service
	Itinerary   *itinerary.Service
	Packing     *packing.Service
	Todos       *todo.Service
	// Shares 未装配时，缺少分享头返回 401，有分享头返回 503。
	Shares *share.Service
	// 任一服务为 nil 时对应接口返回 503 DEPENDENCY_UNAVAILABLE。
	Ledger     *finance.LedgerService
	Statistics *finance.StatisticsService
	Assets     *assets.Service
	Geo        *geo.Service
}

// publicPaths 是不要求身份的业务路径（接口设计 1.1）。带令牌访问时仍会解析身份。
var publicPaths = map[string]struct{}{
	"/metadata":              {},
	"/auth/email-challenges": {},
	"/auth/register":         {},
	"/auth/login":            {},
	"/auth/refresh":          {},
	"/auth/password-reset":   {},
}

// isSharePath 是访客分享路径：不要求账号身份，由 middleware.Share 校验分享令牌。
func isSharePath(r *http.Request) bool {
	return strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/api/v1"), "/public/")
}

func isPublic(r *http.Request) bool {
	if isSharePath(r) {
		return true
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	_, ok := publicPaths[path]
	return ok
}

// allowWhileDeleting 是账号注销中仍可访问的路径：退出与注销进度。
func allowWhileDeleting(r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	return path == "/auth/logout" || strings.HasPrefix(path, "/account/deletion")
}

// NewRouter 组装中间件顺序、探针与 /api/v1 下的生成路由。
// 中间件顺序：请求编号 → 恢复 → 日志 → CORS → 请求上下文 → 认证。
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer(d.Logger))
	r.Use(middleware.Logging(d.Logger))
	r.Use(middleware.CORS(d.CORSOrigins))
	r.NotFound(notFound)
	r.MethodNotAllowed(methodNotAllowed)

	r.Get("/health/live", live)
	r.Get("/health/ready", ready(d.Readiness))

	handler := &Handler{
		logger: d.Logger, metadata: d.Metadata, identity: d.Identity, sessions: d.Sessions, profile: d.Profile,
		categories: d.Categories, trips: d.Trips, itinerary: d.Itinerary, packing: d.Packing, todos: d.Todos,
		ledger: d.Ledger, statistics: d.Statistics, assets: d.Assets, geo: d.Geo,
		cookies: d.Cookies, corsOrigins: d.CORSOrigins, shares: d.Shares,
	}
	strict := generated.NewStrictHandlerWithOptions(handler, nil, generated.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  requestError,
		ResponseErrorHandlerFunc: handler.responseError,
	})

	api := chi.NewRouter()
	api.Use(middleware.WithRequest)
	if d.Sessions != nil {
		api.Use(middleware.Auth(d.Sessions, middleware.AuthPolicy{Public: isPublic, AllowWhileDeleting: allowWhileDeleting}))
	}
	if d.Shares != nil {
		api.Use(middleware.Share(d.Shares, isSharePath))
	} else {
		// 未装配分享服务时访客路径一律 401，避免生成路由把请求放到占位处理器。
		api.Use(middleware.Share(unavailableResolver{}, isSharePath))
	}

	api.NotFound(notFound)
	api.MethodNotAllowed(methodNotAllowed)
	generated.HandlerWithOptions(strict, generated.ChiServerOptions{
		BaseRouter:       api,
		ErrorHandlerFunc: requestError,
	})
	r.Mount("/api/v1", api)
	return r
}

// unavailableResolver 在分享服务未装配时拒绝所有访客请求。
type unavailableResolver struct{}

func (unavailableResolver) Resolve(context.Context, string, string) (share.Viewer, error) {
	return share.Viewer{}, apperr.New(http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "分享服务未启用")
}
