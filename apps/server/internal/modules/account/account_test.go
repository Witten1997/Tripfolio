package account_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/adapters/mail"
	"tripfolio/server/internal/adapters/ratelimit"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/modules/account"
)

type captureMailer struct {
	mu    sync.Mutex
	codes []string
	fail  bool
}

var codeRe = regexp.MustCompile(`\b\d{6}\b`)

func (m *captureMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return io.ErrUnexpectedEOF
	}
	m.codes = append(m.codes, codeRe.FindString(msg.Text))
	return nil
}

func (m *captureMailer) last() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.codes[len(m.codes)-1]
}

type fixture struct {
	store    *account.MemoryStore
	identity *account.IdentityService
	sessions *account.SessionService
	profile  *account.ProfileService
	mailer   *captureMailer
	clock    *clock.Fake
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithLogger(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newFixtureWithLogger(t *testing.T, logger *slog.Logger) *fixture {
	t.Helper()
	kr, err := security.ParseKeyring("k1=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC))
	store := account.NewMemoryStore()
	policy := account.DefaultPolicy()
	sessions := account.NewSessionService(store, security.NewTokenIssuer(kr, policy.AccessTokenTTL), clk, policy)
	mailer := &captureMailer{}
	identity := account.NewIdentityService(account.IdentityDeps{
		Store: store, Sessions: sessions, Hasher: security.NewPasswordHasher(2), Keyring: kr, Mailer: mailer,
		Limiter: ratelimit.New(), Clock: clk, Policy: policy, Logger: logger,
	})
	return &fixture{store: store, identity: identity, sessions: sessions, profile: account.NewProfileService(store, nil, clk), mailer: mailer, clock: clk}
}

func TestRequestEmailChallengeLogsVerificationCode(t *testing.T) {
	var logs bytes.Buffer
	f := newFixtureWithLogger(t, slog.New(slog.NewTextHandler(&logs, nil)))
	ctx := context.Background()

	for _, purpose := range []account.Purpose{account.PurposeRegister, account.PurposeResetPassword} {
		result, err := f.identity.RequestEmailChallenge(ctx, purpose, string(purpose)+"@example.com", "1.2.3.4")
		if err != nil {
			t.Fatalf("request %s challenge: %v", purpose, err)
		}
		output := logs.String()
		if !strings.Contains(output, "purpose="+string(purpose)) {
			t.Errorf("log missing purpose %s: %s", purpose, output)
		}
		if !strings.Contains(output, "challenge_id="+result.ChallengeID.String()) {
			t.Errorf("log missing challenge ID %s: %s", result.ChallengeID, output)
		}
		if !strings.Contains(output, "code="+f.mailer.last()) {
			t.Errorf("log missing verification code for %s: %s", purpose, output)
		}
	}
}

func webClient() account.ClientInfo {
	return account.ClientInfo{Kind: actor.ClientWeb, DeviceID: uuid.New()}
}

func (f *fixture) register(t *testing.T, email string) account.AuthResult {
	t.Helper()
	ctx := context.Background()
	ch, err := f.identity.RequestEmailChallenge(ctx, account.PurposeRegister, email, "1.2.3.4")
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	res, err := f.identity.Register(ctx, account.RegisterInput{
		ChallengeID: ch.ChallengeID, Email: email, Code: f.mailer.last(), Password: "correct horse battery", Nickname: "测试", Client: webClient(),
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return res
}

func TestRegisterLoginAndAuthenticate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.register(t, " User@Example.COM ")
	if res.Account.Email != "User@Example.COM" || res.Account.EmailKey != "user@example.com" {
		t.Fatalf("email normalization: %+v", res.Account)
	}
	if res.Tokens.CSRFToken == "" || res.Tokens.RefreshToken == "" || res.Tokens.AccessToken == "" {
		t.Fatalf("tokens incomplete: %+v", res.Tokens)
	}
	a, err := f.sessions.Authenticate(ctx, res.Tokens.AccessToken)
	if err != nil || a.AccountID != res.Account.ID || a.ClientKind != actor.ClientWeb {
		t.Fatalf("authenticate: %+v, %v", a, err)
	}

	login, err := f.identity.Login(ctx, "user@example.com", "correct horse battery", account.ClientInfo{Kind: actor.ClientAndroid, DeviceID: uuid.New()}, "9.9.9.9")
	if err != nil || login.Tokens.CSRFToken != "" {
		t.Fatalf("android login: %v, csrf=%q", err, login.Tokens.CSRFToken)
	}
	if _, err := f.identity.Login(ctx, "user@example.com", "wrong password!", webClient(), "9.9.9.9"); err == nil {
		t.Fatal("wrong password must fail")
	}
	if _, err := f.identity.Login(ctx, "nobody@example.com", "whatever password", webClient(), "9.9.9.9"); err == nil {
		t.Fatal("unknown account must fail with the same error")
	}
}

func TestLoginStillWorksWithMailDisabled(t *testing.T) {
	f := newFixture(t)
	registered := f.register(t, "user@example.com")
	identity := account.NewIdentityService(account.IdentityDeps{
		Store: f.store, Sessions: f.sessions, Hasher: security.NewPasswordHasher(2),
		Limiter: ratelimit.New(), Clock: f.clock, Policy: account.DefaultPolicy(),
	})
	login, err := identity.Login(context.Background(), "user@example.com", "correct horse battery", webClient(), "127.0.0.1")
	if err != nil {
		t.Fatalf("邮件禁用不应影响已有账号登录: %v", err)
	}
	if login.Account.ID != registered.Account.ID || login.Tokens.AccessToken == "" {
		t.Fatal("登录应返回已有账号与有效令牌")
	}
}

func TestRegisterRejectsWrongCodeAndDuplicate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ch, _ := f.identity.RequestEmailChallenge(ctx, account.PurposeRegister, "a@example.com", "ip")
	_, err := f.identity.Register(ctx, account.RegisterInput{ChallengeID: ch.ChallengeID, Email: "a@example.com", Code: "000000", Password: "correct horse battery", Nickname: "x", Client: webClient()})
	if e, ok := apperr.As(err); !ok || e.Code != "VALIDATION_FAILED" {
		t.Fatalf("wrong code: %v", err)
	}
	c, _ := f.store.Challenge(ch.ChallengeID)
	if c.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", c.Attempts)
	}
	f.register(t, "b@example.com")
	f.clock.Advance(61 * time.Second)
	ch2, err := f.identity.RequestEmailChallenge(ctx, account.PurposeRegister, "b@example.com", "ip")
	if err != nil {
		t.Fatalf("second challenge: %v", err)
	}
	_, err = f.identity.Register(ctx, account.RegisterInput{ChallengeID: ch2.ChallengeID, Email: "b@example.com", Code: f.mailer.last(), Password: "correct horse battery", Nickname: "x", Client: webClient()})
	if e, ok := apperr.As(err); !ok || e.Code != "ACCOUNT_ALREADY_EXISTS" {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestChallengeResendLimits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.identity.RequestEmailChallenge(ctx, account.PurposeRegister, "c@example.com", "ip"); err != nil {
		t.Fatal(err)
	}
	_, err := f.identity.RequestEmailChallenge(ctx, account.PurposeRegister, "c@example.com", "ip")
	if e, ok := apperr.As(err); !ok || e.Code != "RATE_LIMITED" {
		t.Fatalf("resend within 60s must be rate limited: %v", err)
	}
	f.clock.Advance(61 * time.Second)
	if _, err := f.identity.RequestEmailChallenge(ctx, account.PurposeRegister, "c@example.com", "ip"); err != nil {
		t.Fatalf("resend after 60s: %v", err)
	}
}

func TestRefreshRotationGraceAndReuse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.register(t, "r@example.com")
	t1 := res.Tokens.RefreshToken

	r2, err := f.sessions.Refresh(ctx, t1)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	t2 := r2.Tokens.RefreshToken
	if t2 == t1 || r2.Tokens.CSRFToken == "" {
		t.Fatal("refresh must rotate and reissue csrf for web")
	}

	// 响应丢失后 30 秒内用旧令牌重试：签发新令牌对，不撤销；前次摘要与宽限起点保持不变
	f.clock.Advance(30 * time.Second)
	r3, err := f.sessions.Refresh(ctx, t1)
	if err != nil {
		t.Fatalf("retry within grace: %v", err)
	}
	if _, err := f.sessions.Authenticate(ctx, r3.Tokens.AccessToken); err != nil {
		t.Fatalf("access token from reissue must work: %v", err)
	}

	// 宽限期外再用 t1：仍是“前次摘要”，视为复用，会话撤销
	f.clock.Advance(2 * time.Minute)
	if _, err := f.sessions.Refresh(ctx, t1); err == nil {
		t.Fatal("reuse outside grace must be rejected")
	}
	sess, _ := f.store.Session(res.Tokens.SessionID)
	if sess.RevokedAt == nil {
		t.Fatal("session must be revoked after reuse detection")
	}
	if _, err := f.sessions.Refresh(ctx, r3.Tokens.RefreshToken); err == nil {
		t.Fatal("newest refresh token must be dead after revocation")
	}
	if _, err := f.sessions.Authenticate(ctx, res.Tokens.AccessToken); err == nil {
		t.Fatal("access token of revoked session must be rejected")
	}

	// 未知 secret（既非当前也非前次）只拒绝，不撤销别的会话
	other := f.register(t, "r2@example.com")
	if _, err := f.sessions.Refresh(ctx, other.Tokens.SessionID.String()+".garbage"); err == nil {
		t.Fatal("unknown secret must be rejected")
	}
	if s2, _ := f.store.Session(other.Tokens.SessionID); s2.RevokedAt != nil {
		t.Fatal("unknown secret must not revoke the session")
	}
}

func TestRefreshRejectsGarbageAndUnknownSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, bad := range []string{"", "not-a-token", uuid.New().String() + ".secret"} {
		if _, err := f.sessions.Refresh(ctx, bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestResetPasswordRevokesAllSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.register(t, "p@example.com")
	ch, _ := f.identity.RequestEmailChallenge(ctx, account.PurposeResetPassword, "p@example.com", "ip")
	if err := f.identity.ResetPassword(ctx, ch.ChallengeID, "p@example.com", f.mailer.last(), "brand new password 1"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := f.sessions.Authenticate(ctx, res.Tokens.AccessToken); err == nil {
		t.Fatal("old session must be revoked")
	}
	if _, err := f.identity.Login(ctx, "p@example.com", "brand new password 1", webClient(), "ip"); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	// 不存在的账号：验证码有效时也返回成功，不暴露存在性
	ch2, _ := f.identity.RequestEmailChallenge(ctx, account.PurposeResetPassword, "ghost@example.com", "ip")
	if err := f.identity.ResetPassword(ctx, ch2.ChallengeID, "ghost@example.com", f.mailer.last(), "brand new password 1"); err != nil {
		t.Fatalf("reset for unknown account must look successful: %v", err)
	}
}

func TestReauthenticateAndChangePassword(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.register(t, "q@example.com")
	a, _ := f.sessions.Authenticate(ctx, res.Tokens.AccessToken)
	if a.RecentlyAuthenticated(f.clock.Now(), 5*time.Minute) {
		t.Fatal("fresh session must not count as recently authenticated")
	}
	if err := f.identity.Reauthenticate(ctx, a, "wrong password!"); err == nil {
		t.Fatal("wrong password must fail")
	}
	if err := f.identity.Reauthenticate(ctx, a, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	a, _ = f.sessions.Authenticate(ctx, res.Tokens.AccessToken)
	if !a.RecentlyAuthenticated(f.clock.Now(), 5*time.Minute) {
		t.Fatal("should be recently authenticated")
	}
	other, _ := f.identity.Login(ctx, "q@example.com", "correct horse battery", webClient(), "ip")
	if err := f.identity.ChangePassword(ctx, a, "correct horse battery", "another strong password"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sessions.Authenticate(ctx, other.Tokens.AccessToken); err == nil {
		t.Fatal("other sessions must be revoked after password change")
	}
	if _, err := f.sessions.Authenticate(ctx, res.Tokens.AccessToken); err != nil {
		t.Fatal("current session must survive password change")
	}
}

func TestProfileUpdateVersionAndValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.register(t, "u@example.com")
	a, _ := f.sessions.Authenticate(ctx, res.Tokens.AccessToken)
	nick := "新昵称"
	updated, _, err := f.profile.Update(ctx, a, 1, account.ProfilePatch{Nickname: &nick})
	if err != nil || updated.Nickname != "新昵称" || updated.Version != 2 {
		t.Fatalf("update: %+v, %v", updated, err)
	}
	if _, _, err := f.profile.Update(ctx, a, 1, account.ProfilePatch{Nickname: &nick}); err == nil {
		t.Fatal("stale version must conflict")
	}
	bad := "Mars/Olympus"
	if _, _, err := f.profile.Update(ctx, a, 2, account.ProfilePatch{DefaultTimezone: &bad}); err == nil {
		t.Fatal("invalid timezone must be rejected")
	}
}

func TestLoginRateLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.register(t, "l@example.com")
	var last error
	for i := 0; i < 11; i++ {
		_, last = f.identity.Login(ctx, "l@example.com", "wrong password!", webClient(), "ip")
	}
	if e, ok := apperr.As(last); !ok || e.Code != "RATE_LIMITED" {
		t.Fatalf("11th failed login must be rate limited: %v", last)
	}
}
