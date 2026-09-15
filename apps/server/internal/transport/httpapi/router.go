package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

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

func isPublic(r *http.Request) bool {
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
		cookies: d.Cookies, corsOrigins: d.CORSOrigins,
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
	api.NotFound(notFound)
	api.MethodNotAllowed(methodNotAllowed)
	generated.HandlerWithOptions(strict, generated.ChiServerOptions{
		BaseRouter:       api,
		ErrorHandlerFunc: requestError,
	})
	r.Mount("/api/v1", api)
	return r
}
