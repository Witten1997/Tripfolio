package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/mail"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/bootstrap"
	"tripfolio/server/internal/config"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/transport/httpapi"
)

// captureMailer 记录验证码，代替真实邮件投递。
type captureMailer struct {
	mu    sync.Mutex
	codes []string
}

var sixDigits = regexp.MustCompile(`\b\d{6}\b`)

func (m *captureMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes = append(m.codes, sixDigits.FindString(msg.Text))
	return nil
}

func (m *captureMailer) last() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.codes[len(m.codes)-1]
}

type apiFixture struct {
	t        *testing.T
	server   *httptest.Server
	mailer   *captureMailer
	origin   string
	pool     *pgxpool.Pool
	services bootstrap.Services
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	url := testDatabaseURL(t)
	ctx := context.Background()
	cfg := config.Config{
		DatabaseURL: url, DBMaxConns: 4, WebBaseURL: "http://localhost:5173", CORSOrigins: []string{"http://localhost:5173"},
		PasswordHashConcurrency: 2, CookieSecure: false, Mail: config.MailConfig{Driver: "log"},
	}
	// 设置了对象存储端点时接入真实 MinIO，资产用例据此决定是否跳过。
	cfg.ObjectStore = testObjectStoreConfig()
	if err := bootstrap.RunMigrate(ctx, cfg, quietLogger(), []string{"up"}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgcore.NewPool(ctx, url, cfg.DBMaxConns)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	mailer := &captureMailer{}
	logger := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	services, err := bootstrap.BuildServices(pool, cfg, logger, mailer)
	if err != nil {
		t.Fatalf("BuildServices: %v", err)
	}
	router := httpapi.NewRouter(httpapi.Deps{
		Logger: logger, Metadata: metadata.Current(), Readiness: readinessFunc(func(context.Context) error { return nil }),
		CORSOrigins: cfg.CORSOrigins, Cookies: httpapi.CookieSettings{Secure: false},
		Identity: services.Identity, Sessions: services.Sessions, Profile: services.Profile, Categories: services.Categories,
		Trips: services.Trips, Itinerary: services.Itinerary, Packing: services.Packing, Todos: services.Todos,
		Ledger: services.Ledger, Statistics: services.Statistics, Assets: services.Assets, Shares: services.Shares, Geo: services.Geo,
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &apiFixture{t: t, server: srv, mailer: mailer, origin: cfg.CORSOrigins[0], pool: pool, services: services}
}

// testWriter 把服务端日志转发到 t.Log，便于定位 500。
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

type readinessFunc func(context.Context) error

func (f readinessFunc) Check(ctx context.Context) error { return f(ctx) }

type apiResponse struct {
	Status  int
	Body    map[string]any
	Raw     []byte
	Cookies []*http.Cookie
	Header  http.Header
}

type request struct {
	method, path string
	body         any
	token        string
	headers      map[string]string
	cookies      []*http.Cookie
}

func (f *apiFixture) do(r request) apiResponse {
	f.t.Helper()
	var buf bytes.Buffer
	if r.body != nil {
		if err := json.NewEncoder(&buf).Encode(r.body); err != nil {
			f.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(r.method, f.server.URL+"/api/v1"+r.path, &buf)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Origin", f.origin)
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	for _, c := range r.cookies {
		req.AddCookie(c)
	}
	resp, err := f.server.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw bytes.Buffer
	_, _ = raw.ReadFrom(resp.Body)
	out := apiResponse{Status: resp.StatusCode, Raw: raw.Bytes(), Cookies: resp.Cookies(), Header: resp.Header}
	if len(out.Raw) > 0 {
		_ = json.Unmarshal(out.Raw, &out.Body)
	}
	return out
}

func (r apiResponse) data() map[string]any {
	d, _ := r.Body["data"].(map[string]any)
	return d
}

func (r apiResponse) cookie(name string) *http.Cookie {
	for _, c := range r.Cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func expectStatus(t *testing.T, res apiResponse, want int, code string) {
	t.Helper()
	if res.Status != want {
		t.Fatalf("status = %d, want %d; body = %s", res.Status, want, res.Raw)
	}
	if code != "" && res.Body["code"] != code {
		t.Fatalf("code = %v, want %s; body = %s", res.Body["code"], code, res.Raw)
	}
}

func webClientBody() map[string]any {
	return map[string]any{"kind": "web", "device_id": uuid.NewString(), "device_name": "集成测试浏览器"}
}

func androidClientBody() map[string]any {
	return map[string]any{"kind": "android", "device_id": uuid.NewString(), "device_name": "集成测试手机"}
}

// registerWeb 走完整注册流程并返回注册响应（含 Cookie）。
func (f *apiFixture) registerWeb(email, password string) apiResponse {
	f.t.Helper()
	ch := f.do(request{method: http.MethodPost, path: "/auth/email-challenges", body: map[string]any{"purpose": "register", "email": email}})
	expectStatus(f.t, ch, http.StatusAccepted, "")
	res := f.do(request{method: http.MethodPost, path: "/auth/register", body: map[string]any{
		"challenge_id": ch.data()["challenge_id"], "email": email, "code": f.mailer.last(),
		"password": password, "nickname": "集成测试", "client": webClientBody(),
	}})
	expectStatus(f.t, res, http.StatusCreated, "")
	return res
}

func uniqueEmail() string {
	return fmt.Sprintf("it-%s@example.com", strings.Split(uuid.NewString(), "-")[0])
}

const strongETag1 = `"1"`

func TestHTTPRegisterLoginRefreshLogoutWeb(t *testing.T) {
	f := newAPIFixture(t)
	email, password := uniqueEmail(), "correct horse battery"

	reg := f.registerWeb(email, password)
	access, _ := reg.data()["access_token"].(string)
	if access == "" {
		t.Fatalf("注册响应缺少 access_token: %s", reg.Raw)
	}
	if _, has := reg.data()["refresh_token"]; has {
		t.Fatalf("网页会话不应在正文返回 refresh_token: %s", reg.Raw)
	}
	csrfBody, _ := reg.data()["csrf_token"].(string)
	refreshCookie, csrfCookie := reg.cookie("tripfolio_refresh"), reg.cookie("tripfolio_csrf")
	if refreshCookie == nil || csrfCookie == nil {
		t.Fatalf("注册响应应设置刷新与 CSRF Cookie，实际 %v", reg.Cookies)
	}
	if !refreshCookie.HttpOnly || csrfCookie.HttpOnly {
		t.Fatalf("刷新 Cookie 须 HttpOnly，CSRF Cookie 不能 HttpOnly")
	}
	if csrfCookie.Value != csrfBody {
		t.Fatal("CSRF Cookie 与正文 csrf_token 不一致")
	}

	// 受保护接口
	me := f.do(request{method: http.MethodGet, path: "/account", token: access})
	expectStatus(t, me, http.StatusOK, "")
	if me.data()["email"] != email || me.Header.Get("ETag") != strongETag1 {
		t.Fatalf("GET /account 返回不符: %s ETag=%s", me.Raw, me.Header.Get("ETag"))
	}
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/account"}), http.StatusUnauthorized, "AUTH_REQUIRED")

	// 预设分类已种入
	cats := f.do(request{method: http.MethodGet, path: "/expense-categories", token: access})
	expectStatus(t, cats, http.StatusOK, "")
	if list, _ := cats.Body["data"].([]any); len(list) != 6 {
		t.Fatalf("注册后应有 6 个预设分类，实际 %d", len(list))
	}

	// 网页刷新：缺 CSRF 头 → 403；来源不允许 → 403；正确 → 200 并轮换 Cookie
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/auth/refresh", body: map[string]any{}, cookies: []*http.Cookie{refreshCookie, csrfCookie}}),
		http.StatusForbidden, "CSRF_FAILED")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/auth/refresh", body: map[string]any{}, cookies: []*http.Cookie{refreshCookie, csrfCookie},
		headers: map[string]string{"X-CSRF-Token": csrfCookie.Value, "Origin": "https://evil.example"}}), http.StatusForbidden, "CSRF_FAILED")
	ref := f.do(request{method: http.MethodPost, path: "/auth/refresh", body: map[string]any{}, cookies: []*http.Cookie{refreshCookie, csrfCookie},
		headers: map[string]string{"X-CSRF-Token": csrfCookie.Value}})
	expectStatus(t, ref, http.StatusOK, "")
	newRefresh, newCSRF := ref.cookie("tripfolio_refresh"), ref.cookie("tripfolio_csrf")
	if newRefresh == nil || newRefresh.Value == refreshCookie.Value {
		t.Fatal("刷新后应轮换刷新 Cookie")
	}
	access2, _ := ref.data()["access_token"].(string)

	// 60 秒宽限：用前次刷新 Cookie 重试仍成功
	grace := f.do(request{method: http.MethodPost, path: "/auth/refresh", body: map[string]any{}, cookies: []*http.Cookie{refreshCookie, newCSRF},
		headers: map[string]string{"X-CSRF-Token": newCSRF.Value}})
	expectStatus(t, grace, http.StatusOK, "")
	latest, latestCSRF := grace.cookie("tripfolio_refresh"), grace.cookie("tripfolio_csrf")

	// 会话列表包含当前会话
	sessions := f.do(request{method: http.MethodGet, path: "/account/sessions", token: access2})
	expectStatus(t, sessions, http.StatusOK, "")
	if list, _ := sessions.Body["data"].([]any); len(list) != 1 || list[0].(map[string]any)["is_current"] != true {
		t.Fatalf("会话列表不符: %s", sessions.Raw)
	}

	// 退出：清 Cookie，之后访问令牌与刷新 Cookie 都失效
	out := f.do(request{method: http.MethodPost, path: "/auth/logout", token: access2})
	expectStatus(t, out, http.StatusNoContent, "")
	if c := out.cookie("tripfolio_refresh"); c == nil || c.MaxAge != -1 {
		t.Fatalf("退出应清除刷新 Cookie: %v", out.Cookies)
	}
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/account", token: access2}), http.StatusUnauthorized, "SESSION_EXPIRED")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/auth/refresh", body: map[string]any{}, cookies: []*http.Cookie{latest, latestCSRF},
		headers: map[string]string{"X-CSRF-Token": latestCSRF.Value}}), http.StatusUnauthorized, "SESSION_EXPIRED")

	// 重新登录（安卓客户端：刷新令牌在正文）
	login := f.do(request{method: http.MethodPost, path: "/auth/login", body: map[string]any{"email": email, "password": password, "client": androidClientBody()}})
	expectStatus(t, login, http.StatusOK, "")
	refreshToken, _ := login.data()["refresh_token"].(string)
	if refreshToken == "" || login.cookie("tripfolio_refresh") != nil {
		t.Fatalf("原生会话应在正文返回 refresh_token 且不设 Cookie: %s", login.Raw)
	}
	nativeRef := f.do(request{method: http.MethodPost, path: "/auth/refresh", body: map[string]any{"refresh_token": refreshToken}})
	expectStatus(t, nativeRef, http.StatusOK, "")
	if nativeRef.data()["refresh_token"] == refreshToken {
		t.Fatal("原生刷新应轮换 refresh_token")
	}
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/auth/login", body: map[string]any{"email": email, "password": "wrong password!!", "client": androidClientBody()}}),
		http.StatusUnauthorized, "INVALID_CREDENTIALS")
}

func TestHTTPResetPasswordRevokesSessionsAndProfilePatch(t *testing.T) {
	f := newAPIFixture(t)
	email, password := uniqueEmail(), "correct horse battery"
	reg := f.registerWeb(email, password)
	access, _ := reg.data()["access_token"].(string)

	// PATCH /account：缺 If-Match → 428；正确 → 200 且版本递增；版本过期 → 412
	patch := map[string]any{"nickname": "新昵称"}
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/account", token: access, body: patch,
		headers: map[string]string{"Idempotency-Key": uuid.NewString()}}), http.StatusPreconditionRequired, "VERSION_REQUIRED")
	ok := f.do(request{method: http.MethodPatch, path: "/account", token: access, body: patch,
		headers: map[string]string{"Idempotency-Key": uuid.NewString(), "If-Match": strongETag1}})
	expectStatus(t, ok, http.StatusOK, "")
	if d, _ := ok.data()["data"].(map[string]any); d["nickname"] != "新昵称" || d["version"] != "2" {
		t.Fatalf("PATCH /account 返回不符: %s", ok.Raw)
	}
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/account", token: access, body: patch,
		headers: map[string]string{"Idempotency-Key": uuid.NewString(), "If-Match": strongETag1}}), http.StatusPreconditionFailed, "VERSION_CONFLICT")

	// 找回密码：验证码错误 → 422；正确 → 204 并撤销全部会话
	ch := f.do(request{method: http.MethodPost, path: "/auth/email-challenges", body: map[string]any{"purpose": "reset_password", "email": email}})
	expectStatus(t, ch, http.StatusAccepted, "")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/auth/password-reset", body: map[string]any{
		"challenge_id": ch.data()["challenge_id"], "email": email, "code": "000000", "new_password": "another long password",
	}}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/auth/password-reset", body: map[string]any{
		"challenge_id": ch.data()["challenge_id"], "email": email, "code": f.mailer.last(), "new_password": "another long password",
	}}), http.StatusNoContent, "")
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/account", token: access}), http.StatusUnauthorized, "SESSION_EXPIRED")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/auth/login", body: map[string]any{"email": email, "password": password, "client": webClientBody()}}),
		http.StatusUnauthorized, "INVALID_CREDENTIALS")
	login := f.do(request{method: http.MethodPost, path: "/auth/login", body: map[string]any{"email": email, "password": "another long password", "client": webClientBody()}})
	expectStatus(t, login, http.StatusOK, "")

	// 未知邮箱找回：同样 202，不暴露存在性；同邮箱 60 秒内重复申请 → 429
	ch2 := f.do(request{method: http.MethodPost, path: "/auth/email-challenges", body: map[string]any{"purpose": "reset_password", "email": uniqueEmail()}})
	expectStatus(t, ch2, http.StatusAccepted, "")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/auth/email-challenges", body: map[string]any{"purpose": "reset_password", "email": email}}),
		http.StatusTooManyRequests, "RATE_LIMITED")
}

func TestHTTPCrossAccountIsNotFound(t *testing.T) {
	f := newAPIFixture(t)
	a := f.registerWeb(uniqueEmail(), "correct horse battery")
	b := f.registerWeb(uniqueEmail(), "correct horse battery")
	tokenA, _ := a.data()["access_token"].(string)
	tokenB, _ := b.data()["access_token"].(string)

	cats := f.do(request{method: http.MethodGet, path: "/expense-categories", token: tokenA})
	expectStatus(t, cats, http.StatusOK, "")
	first := cats.Body["data"].([]any)[0].(map[string]any)["id"].(string)
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/expense-categories/" + first, token: tokenA}), http.StatusOK, "")
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/expense-categories/" + first, token: tokenB}), http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// 撤销他人会话：静默成功，且对方会话仍有效
	sessA := f.do(request{method: http.MethodGet, path: "/account/sessions", token: tokenA})
	sidA := sessA.Body["data"].([]any)[0].(map[string]any)["id"].(string)
	expectStatus(t, f.do(request{method: http.MethodDelete, path: "/account/sessions/" + sidA, token: tokenB}), http.StatusNoContent, "")
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/account", token: tokenA}), http.StatusOK, "")
}
