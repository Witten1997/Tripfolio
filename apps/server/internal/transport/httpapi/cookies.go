package httpapi

import (
	"net/http"
	"strings"
	"time"

	"tripfolio/server/internal/foundation/apperr"
)

// CookieSettings 控制网页端刷新凭证与 CSRF Cookie 的名称与属性（接口设计 3.1）。
// Secure 为 true 时使用 __Host- 前缀（要求 Secure、Path=/、无 Domain）；本地 http 开发时关闭。
type CookieSettings struct {
	Secure bool
}

func (c CookieSettings) refreshName() string {
	if c.Secure {
		return "__Host-tripfolio_refresh"
	}
	return "tripfolio_refresh"
}

func (c CookieSettings) csrfName() string {
	if c.Secure {
		return "__Host-tripfolio_csrf"
	}
	return "tripfolio_csrf"
}

// refreshCookie 构造 HttpOnly 的刷新凭证 Cookie。
func (c CookieSettings) refreshCookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name: c.refreshName(), Value: value, Path: "/", HttpOnly: true, Secure: c.Secure,
		SameSite: http.SameSiteLaxMode, Expires: expires,
	}
}

// csrfCookie 构造可被脚本读取的 CSRF Cookie。
func (c CookieSettings) csrfCookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name: c.csrfName(), Value: value, Path: "/", HttpOnly: false, Secure: c.Secure,
		SameSite: http.SameSiteLaxMode, Expires: expires,
	}
}

// clearCookies 返回让浏览器删除两枚 Cookie 的响应头值。
func (c CookieSettings) clearCookies() []*http.Cookie {
	expired := time.Unix(0, 0)
	return []*http.Cookie{
		{Name: c.refreshName(), Value: "", Path: "/", HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteLaxMode, Expires: expired, MaxAge: -1},
		{Name: c.csrfName(), Value: "", Path: "/", HttpOnly: false, Secure: c.Secure, SameSite: http.SameSiteLaxMode, Expires: expired, MaxAge: -1},
	}
}

// readRefreshCookie 读取刷新凭证；不存在返回空串。
func (c CookieSettings) readRefreshCookie(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ck, err := r.Cookie(c.refreshName()); err == nil {
		return ck.Value
	}
	return ""
}

// readCSRFCookie 读取 CSRF Cookie。
func (c CookieSettings) readCSRFCookie(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ck, err := r.Cookie(c.csrfName()); err == nil {
		return ck.Value
	}
	return ""
}

// checkOrigin 校验浏览器请求的来源：Origin 存在时必须在允许列表内（接口设计 3.1）。
func checkOrigin(r *http.Request, allowed []string) error {
	if r == nil {
		return nil
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	for _, o := range allowed {
		if strings.EqualFold(strings.TrimRight(o, "/"), strings.TrimRight(origin, "/")) {
			return nil
		}
	}
	return apperr.Forbidden("CSRF_FAILED", "请求来源不被允许")
}

// withCookies 包装任意 Visit 响应，在写出前设置 Cookie。
type withCookies[T any] struct {
	inner   T
	cookies []*http.Cookie
	visit   func(T, http.ResponseWriter) error
}

func (c withCookies[T]) apply(w http.ResponseWriter) error {
	for _, ck := range c.cookies {
		http.SetCookie(w, ck)
	}
	return c.visit(c.inner, w)
}
