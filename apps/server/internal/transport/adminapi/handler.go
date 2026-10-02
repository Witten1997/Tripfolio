package adminapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"tripfolio/server/internal/config"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/transport/adminapi/generated"
	"tripfolio/server/internal/transport/httpapi/middleware"
	"tripfolio/server/internal/transport/httpapi/problem"
)

type Options struct {
	BasePath string
	Runtime  func(context.Context) admin.RuntimeStatus
	Service  *admin.Service
	Secure   bool
	Origins  []string
	Logger   *slog.Logger
}

type Handler struct{ options Options }
type sessionKey struct{}

func NewHandler(o Options) http.Handler {
	if o.BasePath == "" {
		o.BasePath = (config.Config{}).AdminAPIPath()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	h := &Handler{options: o}
	r := chi.NewRouter()
	r.Use(h.authenticate)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { h.fail(w, r, apperr.NotFound()) })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		h.fail(w, r, apperr.New(405, "METHOD_NOT_ALLOWED", "请求方法不允许"))
	})
	return generated.HandlerWithOptions(h, generated.ChiServerOptions{BaseRouter: r, ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
		var actor *admin.Session
		if sess, ok := r.Context().Value(sessionKey{}).(admin.Session); ok {
			actor = &sess
		}
		h.reject(w, r, actor, "request.invalid", apperr.BadRequest("MALFORMED_REQUEST", "请求参数无效"))
	}})
}

func (h *Handler) cookieName() string {
	if h.options.Secure {
		return "__Host-tripfolio_admin"
	}
	return "tripfolio_admin"
}

func (h *Handler) token(r *http.Request) string {
	var token string
	for _, cookie := range r.Cookies() {
		if cookie.Name == h.cookieName() {
			if token != "" {
				return ""
			}
			token = cookie.Value
		}
	}
	return token
}

func (h *Handler) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	cookie := &http.Cookie{Name: h.cookieName(), Value: token, Path: "/", HttpOnly: true, Secure: h.options.Secure, SameSite: http.SameSiteStrictMode, Expires: expires}
	if token == "" {
		cookie.MaxAge = -1
	}
	http.SetCookie(w, cookie)
}

func clip(value string, n int) string {
	r := []rune(value)
	if len(r) > n {
		return string(r[:n])
	}
	return value
}

func requestInfo(r *http.Request) admin.RequestInfo {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	// 只记录连接来源，不信任客户端任意伪造的代理头。
	return admin.RequestInfo{RequestID: clip(middleware.RequestIDFrom(r.Context()), 128), IP: clip(ip, 64), UserAgent: clip(r.UserAgent(), 512)}
}

func (h *Handler) validOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Method == http.MethodGet && r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	for _, allowed := range h.options.Origins {
		if strings.EqualFold(strings.TrimRight(allowed, "/"), origin) {
			return true
		}
	}
	return false
}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if h.options.Service == nil {
			h.fail(w, r, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "管理后台尚未启用"))
			return
		}
		setup := strings.TrimRight(r.URL.Path, "/") == h.options.BasePath+"/setup"
		if setup {
			open, err := h.options.Service.SetupOpen(r.Context())
			if err != nil {
				h.fail(w, r, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "初始化状态暂不可用").WithCause(err))
				return
			}
			if !open {
				h.fail(w, r, apperr.NotFound())
				return
			}
		}
		if !h.validOrigin(r) {
			h.reject(w, r, nil, "request.origin", csrfError())
			return
		}
		if setup {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == h.options.BasePath+"/login" {
			next.ServeHTTP(w, r)
			return
		}
		token := h.token(r)
		sess, err := h.options.Service.Authenticate(r.Context(), token)
		if err != nil {
			h.reject(w, r, nil, "authentication", err)
			return
		}
		if r.Method != http.MethodGet && !admin.ValidCSRF(token, r.Header.Get("X-Admin-CSRF")) {
			h.reject(w, r, &sess, "request.csrf", csrfError())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	})
}

func csrfError() error {
	return apperr.Forbidden("ADMIN_CSRF_FAILED", "请求来源或安全验证已失效，请刷新后台页面后重试")
}

func current(r *http.Request) admin.Session { return r.Context().Value(sessionKey{}).(admin.Session) }

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := apperr.As(err)
	if !ok {
		e = apperr.Internal(err)
	}
	if e.Status >= 500 {
		h.options.Logger.Error("后台请求失败", "request_id", middleware.RequestIDFrom(r.Context()), "error", err)
	}
	for key, value := range e.Headers {
		w.Header().Set(key, value)
	}
	problem.Write(w, e.Status, e.Code, e.Detail, middleware.RequestIDFrom(r.Context()))
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request, sess *admin.Session, action string, err error) {
	if auditErr := h.options.Service.Record(r.Context(), sess, action, "denied", requestInfo(r)); auditErr != nil {
		err = auditErr
	}
	h.fail(w, r, err)
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, value any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		h.reject(w, r, nil, "request.invalid", apperr.New(415, "MALFORMED_REQUEST", "请使用 JSON 请求"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		h.reject(w, r, nil, "request.invalid", apperr.BadRequest("MALFORMED_REQUEST", "请求内容无效"))
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		h.reject(w, r, nil, "request.invalid", apperr.BadRequest("MALFORMED_REQUEST", "请求只能包含一个 JSON 对象"))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func identity(sess admin.Session, token string) generated.SessionEnvelope {
	return generated.SessionEnvelope{Data: generated.AdminIdentity{Id: sess.AccountID, Email: sess.Email, Nickname: sess.Nickname, Role: generated.SuperAdmin, SessionId: sess.ID, ExpiresAt: sess.ExpiresAt, CsrfToken: admin.CSRFToken(token)}}
}

func (h *Handler) AdminLogin(w http.ResponseWriter, r *http.Request) {
	var body generated.LoginRequest
	if !h.decode(w, r, &body) {
		return
	}
	if len(body.Password) == 0 || utf8.RuneCountInString(body.Password) > 1024 || len(body.Email) > 254 {
		h.reject(w, r, nil, "login", invalidLogin())
		return
	}
	sess, token, err := h.options.Service.Login(r.Context(), string(body.Email), body.Password, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.setCookie(w, token, sess.ExpiresAt)
	writeJSON(w, identity(sess, token))
}

func invalidLogin() error {
	return apperr.BadRequest("MALFORMED_REQUEST", "请输入有效的邮箱和密码")
}

func (h *Handler) AdminCurrent(w http.ResponseWriter, r *http.Request) {
	sess := current(r)
	if err := h.options.Service.Record(r.Context(), &sess, "session.current", "success", requestInfo(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, identity(sess, h.token(r)))
}

func (h *Handler) AdminLogout(w http.ResponseWriter, r *http.Request, _ generated.AdminLogoutParams) {
	sess := current(r)
	if err := h.options.Service.RevokeSession(r.Context(), sess, sess.ID, requestInfo(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	h.setCookie(w, "", time.Unix(0, 0))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AdminReauthenticate(w http.ResponseWriter, r *http.Request, _ generated.AdminReauthenticateParams) {
	var body generated.PasswordRequest
	if !h.decode(w, r, &body) {
		return
	}
	if len(body.Password) == 0 || utf8.RuneCountInString(body.Password) > 1024 {
		h.reject(w, r, nil, "reauthenticate", invalidLogin())
		return
	}
	if err := h.options.Service.Reauthenticate(r.Context(), current(r), body.Password, requestInfo(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AdminListSessions(w http.ResponseWriter, r *http.Request) {
	sess := current(r)
	items, err := h.options.Service.ListSessions(r.Context(), sess, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]generated.ManagedSession, 0, len(items))
	for _, item := range items {
		data = append(data, generated.ManagedSession{Id: item.ID, CreatedAt: item.CreatedAt, LastSeenAt: item.LastSeenAt, ExpiresAt: item.ExpiresAt, SourceIp: item.IP, UserAgent: item.UserAgent, Current: item.ID == sess.ID})
	}
	writeJSON(w, struct {
		Data []generated.ManagedSession `json:"data"`
	}{Data: data})
}

func (h *Handler) AdminRevokeSession(w http.ResponseWriter, r *http.Request, id uuid.UUID, _ generated.AdminRevokeSessionParams) {
	sess := current(r)
	if err := h.options.Service.RevokeSession(r.Context(), sess, id, requestInfo(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	if id == sess.ID {
		h.setCookie(w, "", time.Unix(0, 0))
	}
	w.WriteHeader(http.StatusNoContent)
}
