package share

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/modules/travel/itinerary"
)

// 错误代码（接口设计 1.4）。
const (
	codeShareNotFound   = "SHARE_NOT_FOUND"
	codeTripDeleted     = "TRIP_DELETED"
	codeAccountDeleting = "ACCOUNT_DELETING"
)

// 访客限流（设计 5.4、7.2）：解析令牌按来源 IP；算路按分享与 IP（routes.go）。
const (
	resolvePerMinute = 120
	routesPerMinute  = 30
)

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// GenerateToken 生成 16 字节随机数的 base64url 无填充编码，恒为 22 个字符、128 位熵。
func GenerateToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成分享令牌: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Deps 是服务依赖，由 bootstrap 显式装配。
type Deps struct {
	Store     Store
	Trips     TripSource
	Itinerary ItinerarySource
	// Routes 为 nil 时访客算路返回 503。
	Routes  RouteSource
	Limiter Limiter
	Cursors paging.Codec
	Clock   clock.Clock
	// ShareBaseURL 每次生成链接时读取后台配置。
	ShareBaseURL func(context.Context) (string, error)
	// Tokens 为 nil 时用 GenerateToken；测试注入确定性令牌。
	Tokens func() (string, error)
	// RouteCacheTTL 为 0 时取 5 分钟。
	RouteCacheTTL time.Duration
}

// Service 实现分享的业务规则（设计 2、5）。
type Service struct {
	d      Deps
	routes *routeCache
}

// NewService 创建服务。
func NewService(d Deps) *Service {
	if d.Tokens == nil {
		d.Tokens = GenerateToken
	}
	if d.RouteCacheTTL <= 0 {
		d.RouteCacheTTL = 5 * time.Minute
	}
	return &Service{d: d, routes: newRouteCache(d.RouteCacheTTL, 1000)}
}

func shareNotFound() *apperr.Error {
	return apperr.New(404, codeShareNotFound, "分享链接不存在或已失效")
}
func tripDeleted() *apperr.Error { return apperr.Gone(codeTripDeleted, "旅行已在回收站中") }

func (s *Service) shareBaseURL(ctx context.Context) (string, error) {
	if s.d.ShareBaseURL == nil {
		return "", apperr.New(503, "SHARE_SITE_NOT_CONFIGURED", "请先在后台站点设置中配置旅行分享地址")
	}
	value, err := s.d.ShareBaseURL(ctx)
	if err != nil {
		return "", shareError(err)
	}
	if value == "" {
		return "", apperr.New(503, "SHARE_SITE_NOT_CONFIGURED", "请先在后台站点设置中配置旅行分享地址")
	}
	return value, nil
}

// resource 把数据库行转成主人可见的资源并拼出完整链接。
func (s *Service) resource(r Record, baseURL string) Resource {
	return Resource{
		ID: r.ID, Token: r.Token, URL: baseURL + "/s/" + r.Token,
		CreatedAt: r.CreatedAt, RotatedAt: r.RotatedAt, LastViewedAt: r.LastViewedAt, ViewCount: r.ViewCount,
	}
}

// ownerTrip 校验旅行属于本人且不在回收站：不存在或非本人 404，回收站 410。
func (s *Service) ownerTrip(ctx context.Context, a actor.Actor, tripID uuid.UUID) error {
	t, ok, err := s.d.Trips.Get(ctx, a.AccountID, tripID)
	if err != nil {
		return shareError(err)
	}
	if !ok {
		return apperr.NotFound()
	}
	if t.DeletedAt != nil {
		return tripDeleted()
	}
	return nil
}

// Get 返回旅行当前的分享；未开启 404 SHARE_NOT_FOUND。
func (s *Service) Get(ctx context.Context, a actor.Actor, tripID uuid.UUID) (Resource, error) {
	if err := s.ownerTrip(ctx, a, tripID); err != nil {
		return Resource{}, err
	}
	baseURL, err := s.shareBaseURL(ctx)
	if err != nil {
		return Resource{}, err
	}

	r, ok, err := s.d.Store.GetByTrip(ctx, a.AccountID, tripID)
	if err != nil {
		return Resource{}, shareError(err)
	}
	if !ok {
		return Resource{}, shareNotFound()
	}
	return s.resource(r, baseURL), nil
}

// Enable 开启分享；已开启原样返回。并发开启时后到者读取先到者的结果。
func (s *Service) Enable(ctx context.Context, a actor.Actor, tripID uuid.UUID) (Resource, error) {
	if err := s.ownerTrip(ctx, a, tripID); err != nil {
		return Resource{}, err
	}
	baseURL, err := s.shareBaseURL(ctx)
	if err != nil {
		return Resource{}, err
	}

	if r, ok, err := s.d.Store.GetByTrip(ctx, a.AccountID, tripID); err != nil {
		return Resource{}, shareError(err)
	} else if ok {
		return s.resource(r, baseURL), nil
	}
	token, err := s.d.Tokens()
	if err != nil {
		return Resource{}, shareError(err)
	}
	r, inserted, err := s.d.Store.Insert(ctx, Record{ID: uuid.New(), AccountID: a.AccountID, TripID: tripID, Token: token, CreatedAt: s.d.Clock.Now()})
	if err != nil {
		return Resource{}, shareError(err)
	}
	if !inserted {
		if r, _, err = s.d.Store.GetByTrip(ctx, a.AccountID, tripID); err != nil {
			return Resource{}, shareError(err)
		}
	}
	return s.resource(r, baseURL), nil
}

// Rotate 重新生成令牌，旧链接立即失效、无宽限期（设计 3）。
func (s *Service) Rotate(ctx context.Context, a actor.Actor, tripID uuid.UUID) (Resource, error) {
	if err := s.ownerTrip(ctx, a, tripID); err != nil {
		return Resource{}, err
	}
	baseURL, err := s.shareBaseURL(ctx)
	if err != nil {
		return Resource{}, err
	}

	token, err := s.d.Tokens()
	if err != nil {
		return Resource{}, shareError(err)
	}
	r, ok, err := s.d.Store.Rotate(ctx, a.AccountID, tripID, token, s.d.Clock.Now())
	if err != nil {
		return Resource{}, shareError(err)
	}
	if !ok {
		return Resource{}, shareNotFound()
	}
	return s.resource(r, baseURL), nil
}

// Disable 关闭分享（物理删行）；未开启也成功。
func (s *Service) Disable(ctx context.Context, a actor.Actor, tripID uuid.UUID) error {
	if err := s.ownerTrip(ctx, a, tripID); err != nil {
		return err
	}
	if err := s.d.Store.Delete(ctx, a.AccountID, tripID); err != nil {
		return shareError(err)
	}
	return nil
}

// Resolve 把分享令牌换成访客身份：格式不符不查库；按来源 IP 限流；主人注销中 403；旅行在回收站 410。
func (s *Service) Resolve(ctx context.Context, token, clientIP string) (Viewer, error) {
	if !tokenPattern.MatchString(token) {
		return Viewer{}, shareNotFound()
	}
	if ok, wait := s.d.Limiter.Allow("share-resolve|"+clientIP, resolvePerMinute, time.Minute, s.d.Clock.Now()); !ok {
		return Viewer{}, apperr.RateLimited(wait)
	}
	res, ok, err := s.d.Store.ResolveToken(ctx, token)
	if err != nil {
		return Viewer{}, shareError(err)
	}
	if !ok {
		return Viewer{}, shareNotFound()
	}
	if res.OwnerStatus != "active" {
		if res.OwnerStatus == "banned" {
			return Viewer{}, apperr.Forbidden("ACCOUNT_BANNED", "账号已被封禁，暂时无法访问分享")
		}
		return Viewer{}, apperr.Forbidden(codeAccountDeleting, "账号正在注销")
	}
	if res.TripDeletedAt != nil {
		return Viewer{}, tripDeleted()
	}
	return Viewer{ShareID: res.ID, AccountID: res.AccountID, TripID: res.TripID, ClientIP: clientIP}, nil
}

// ViewTrip 返回访客可见的旅行信息并计一次访问；计数是 best-effort，失败不影响读取。
func (s *Service) ViewTrip(ctx context.Context, v Viewer) (PublicTrip, error) {
	t, ok, err := s.d.Trips.Get(ctx, v.AccountID, v.TripID)
	if err != nil {
		return PublicTrip{}, shareError(err)
	}
	if !ok {
		return PublicTrip{}, shareNotFound()
	}
	if t.DeletedAt != nil {
		return PublicTrip{}, tripDeleted()
	}
	_ = s.d.Store.RecordView(ctx, v.ShareID, s.d.Clock.Now())
	return ToPublicTrip(t), nil
}

// ListFilters 是访客行程列表的分页参数。
type ListFilters struct {
	Limit  int
	Cursor string
}

func listScope(v Viewer) string { return "share|" + v.ShareID.String() }

// ListItems 返回一页访客可见的行程骨架；顺序与主人列表相同，游标绑定分享。
func (s *Service) ListItems(ctx context.Context, v Viewer, f ListFilters) (paging.Page[PublicItineraryItem], error) {
	var page paging.Page[PublicItineraryItem]
	limit, err := paging.Limit(f.Limit)
	if err != nil {
		return page, err
	}
	var after *itinerary.Position
	if f.Cursor != "" {
		var p itinerary.Position
		if err := s.d.Cursors.Decode(v.AccountID, listScope(v), f.Cursor, &p); err != nil {
			return page, paging.InvalidCursor()
		}
		after = &p
	}
	items, err := s.d.Itinerary.List(ctx, v.AccountID, v.TripID, itinerary.ListQuery{Limit: limit + 1, After: after})
	if err != nil {
		return page, shareError(err)
	}
	if len(items) > limit {
		last := items[limit-1]
		token, err := s.d.Cursors.Encode(v.AccountID, listScope(v), itinerary.Position{ScheduledOn: last.ScheduledOn, SortOrder: last.SortOrder, ID: last.ID})
		if err != nil {
			return page, shareError(err)
		}
		page.NextCursor = &token
		items = items[:limit]
	}
	page.Items = make([]PublicItineraryItem, 0, len(items))
	for _, it := range items {
		page.Items = append(page.Items, ToPublicItem(it))
	}
	return page, nil
}

func shareError(err error) error {
	if _, ok := apperr.As(err); ok {
		return err
	}
	return apperr.Internal(err)
}
