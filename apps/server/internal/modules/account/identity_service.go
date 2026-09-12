package account

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"tripfolio/server/internal/adapters/mail"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
)

const challengePurpose = "challenge"

// Limiter 是登录与验证码限流的最小接口。
type Limiter interface {
	Allow(key string, limit int, window time.Duration, now time.Time) (bool, time.Duration)
}

// IdentityService 处理注册、登录、找回密码与密码复验。
type IdentityService struct {
	store    Store
	sessions *SessionService
	hasher   *security.PasswordHasher
	keyring  *security.Keyring
	mailer   mail.Mailer
	limiter  Limiter
	clock    clock.Clock
	policy   Policy
	logger   *slog.Logger
	// seedCategories 在注册事务内种入预设分类，由 finance 包提供实现。
	seedCategories func(ctx context.Context, accountID uuid.UUID, now time.Time) error
}

// IdentityDeps 是构造依赖。
type IdentityDeps struct {
	Store          Store
	Sessions       *SessionService
	Hasher         *security.PasswordHasher
	Keyring        *security.Keyring
	Mailer         mail.Mailer
	Limiter        Limiter
	Clock          clock.Clock
	Policy         Policy
	Logger         *slog.Logger
	SeedCategories func(ctx context.Context, accountID uuid.UUID, now time.Time) error
}

// NewIdentityService 创建服务。
func NewIdentityService(d IdentityDeps) *IdentityService {
	return &IdentityService{
		store: d.Store, sessions: d.Sessions, hasher: d.Hasher, keyring: d.Keyring, mailer: d.Mailer,
		limiter: d.Limiter, clock: d.Clock, policy: d.Policy, logger: d.Logger, seedCategories: d.SeedCategories,
	}
}

// NormalizeEmail 返回展示用邮箱与规范化键（去首尾空格、小写）。P0 不支持国际化本地部分。
func NormalizeEmail(raw string) (email, key string, err error) {
	email = strings.TrimSpace(raw)
	if email == "" || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		return "", "", errors.New("邮箱格式不正确")
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 || !strings.Contains(email[at+1:], ".") {
		return "", "", errors.New("邮箱格式不正确")
	}
	for _, r := range email {
		if r > 127 {
			return "", "", errors.New("邮箱暂不支持非 ASCII 字符")
		}
	}
	return email, strings.ToLower(email), nil
}

// ValidatePassword 按接口设计 3.1：10–128 个 Unicode 字符，不裁剪、不归一化，字节总长受限。
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < 10 || n > 128 {
		return errors.New("密码长度须为 10–128 个字符")
	}
	if len(password) > 512 {
		return errors.New("密码过长")
	}
	return nil
}

// ChallengeResult 是验证码申请的对外结果，不区分账号是否存在。
type ChallengeResult struct {
	ChallengeID       uuid.UUID
	ExpiresInSeconds  int
	RetryAfterSeconds int
}

// RequestEmailChallenge 生成验证码、投递邮件并返回挑战 ID。限流与作废旧挑战在存储事务内完成；
// 邮件发送在事务外，失败只更新投递状态。任何邮箱都返回同样的响应。
func (s *IdentityService) RequestEmailChallenge(ctx context.Context, purpose Purpose, rawEmail string, clientIP string) (ChallengeResult, error) {
	if !purpose.Valid() {
		return ChallengeResult{}, apperr.Validation(apperr.Field("purpose", "INVALID", "用途无效"))
	}
	email, key, err := NormalizeEmail(rawEmail)
	if err != nil {
		return ChallengeResult{}, apperr.Validation(apperr.Field("email", "INVALID", err.Error()))
	}
	now := s.clock.Now()
	if ok, wait := s.limiter.Allow("challenge-ip:"+clientIP, 20, time.Hour, now); !ok {
		return ChallengeResult{}, apperr.RateLimited(wait)
	}

	code, err := security.SixDigitCode()
	if err != nil {
		return ChallengeResult{}, apperr.Internal(err)
	}
	id := uuid.New()
	kid, mac, err := s.keyring.MAC(challengePurpose, challengeMACInput(id, code))
	if err != nil {
		return ChallengeResult{}, apperr.Internal(err)
	}
	challenge, err := s.store.IssueChallengeTx(ctx, Challenge{
		ID: id, Purpose: purpose, EmailKey: key, CodeMAC: mac, KeyID: kid,
		DeliveryStatus: "pending", CreatedAt: now, ExpiresAt: now.Add(s.policy.ChallengeTTL),
	}, s.policy.ChallengeResend, s.policy.ChallengePerHour)
	switch {
	case errors.Is(err, ErrChallengeTooSoon):
		return ChallengeResult{}, apperr.RateLimited(s.policy.ChallengeResend)
	case errors.Is(err, ErrChallengeQuotaExceeded):
		return ChallengeResult{}, apperr.RateLimited(time.Hour)
	case err != nil:
		return ChallengeResult{}, apperr.Internal(err)
	}

	subject, text := challengeMail(purpose, code, s.policy.ChallengeTTL)
	status := "sent"
	if err := s.mailer.Send(ctx, mail.Message{To: email, Subject: subject, Text: text}); err != nil {
		status = "failed"
		s.logger.ErrorContext(ctx, "验证码邮件投递失败", "error", err, "challenge_id", id)
	}
	if err := s.store.SetChallengeDelivery(ctx, challenge.ID, status); err != nil {
		s.logger.WarnContext(ctx, "更新投递状态失败", "error", err)
	}
	return ChallengeResult{
		ChallengeID:       challenge.ID,
		ExpiresInSeconds:  int(s.policy.ChallengeTTL / time.Second),
		RetryAfterSeconds: int(s.policy.ChallengeResend / time.Second),
	}, nil
}

func challengeMACInput(id uuid.UUID, code string) []byte {
	return []byte(id.String() + ":" + code)
}

func challengeMail(purpose Purpose, code string, ttl time.Duration) (subject, text string) {
	minutes := int(ttl / time.Minute)
	switch purpose {
	case PurposeRegister:
		return "Tripfolio 注册验证码", fmt.Sprintf("您的注册验证码是 %s，%d 分钟内有效。如非本人操作请忽略本邮件。", code, minutes)
	default:
		return "Tripfolio 找回密码验证码", fmt.Sprintf("您的找回密码验证码是 %s，%d 分钟内有效。如非本人操作请忽略本邮件。", code, minutes)
	}
}

// verifyChallenge 校验挑战：用途、邮箱、有效期、尝试次数与 HMAC。失败计数独立提交。
func (s *IdentityService) verifyChallenge(ctx context.Context, id uuid.UUID, purpose Purpose, emailKey, code string) error {
	now := s.clock.Now()
	invalid := apperr.Validation(apperr.Field("code", "INVALID", "验证码无效或已过期"))
	if len(code) != 6 {
		return invalid
	}
	err := s.store.VerifyChallengeTx(ctx, id, func(c Challenge) (bool, error) {
		if c.Purpose != purpose || c.EmailKey != emailKey || c.ConsumedAt != nil || c.InvalidatedAt != nil ||
			!c.ExpiresAt.After(now) || c.Attempts >= s.policy.ChallengeMaxAttempt {
			return false, invalid
		}
		if !s.keyring.VerifyMAC(c.KeyID, challengePurpose, challengeMACInput(c.ID, code), c.CodeMAC) {
			return false, invalid
		}
		return true, nil
	})
	if err != nil {
		if _, ok := apperr.As(err); ok {
			return err
		}
		return invalid
	}
	return nil
}

// RegisterInput 是注册命令。
type RegisterInput struct {
	ChallengeID uuid.UUID
	Email       string
	Code        string
	Password    string
	Nickname    string
	Client      ClientInfo
}

// Register 验证邮箱后创建账号、同步状态、预设分类与首个会话。
func (s *IdentityService) Register(ctx context.Context, in RegisterInput) (AuthResult, error) {
	email, key, err := NormalizeEmail(in.Email)
	if err != nil {
		return AuthResult{}, apperr.Validation(apperr.Field("email", "INVALID", err.Error()))
	}
	if err := ValidatePassword(in.Password); err != nil {
		return AuthResult{}, apperr.Validation(apperr.Field("password", "INVALID", err.Error()))
	}
	nickname := strings.TrimSpace(in.Nickname)
	if n := utf8.RuneCountInString(nickname); n < 1 || n > 64 {
		return AuthResult{}, apperr.Validation(apperr.Field("nickname", "INVALID", "昵称须为 1–64 个字符"))
	}
	if !in.Client.Kind.Valid() {
		return AuthResult{}, apperr.Validation(apperr.Field("client.kind", "INVALID", "客户端类型无效"))
	}
	if err := s.verifyChallenge(ctx, in.ChallengeID, PurposeRegister, key, in.Code); err != nil {
		return AuthResult{}, err
	}
	if exists, err := s.store.AccountExists(ctx, key); err != nil {
		return AuthResult{}, apperr.Internal(err)
	} else if exists {
		return AuthResult{}, apperr.Conflicted("ACCOUNT_ALREADY_EXISTS", "该邮箱已注册，请直接登录")
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return AuthResult{}, apperr.Internal(err)
	}
	now := s.clock.Now()
	created, err := s.store.CreateAccount(ctx, Account{
		ID: uuid.New(), Email: email, EmailKey: key, PasswordHash: hash, Nickname: nickname,
		DefaultTimezone: "Asia/Shanghai", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		if strings.Contains(err.Error(), "accounts_email_key_unique") {
			return AuthResult{}, apperr.Conflicted("ACCOUNT_ALREADY_EXISTS", "该邮箱已注册，请直接登录")
		}
		return AuthResult{}, apperr.Internal(err)
	}
	tokens, err := s.sessions.Open(ctx, created.ID, in.Client)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{Account: created, Tokens: tokens}, nil
}

// Login 用邮箱密码登录。账号不存在时也执行一次哈希比较，使耗时一致；失败计数按邮箱与 IP 限流。
func (s *IdentityService) Login(ctx context.Context, rawEmail, password string, client ClientInfo, clientIP string) (AuthResult, error) {
	invalid := apperr.Unauthorized("INVALID_CREDENTIALS", "邮箱或密码不正确")
	_, key, err := NormalizeEmail(rawEmail)
	if err != nil {
		return AuthResult{}, invalid
	}
	if !client.Kind.Valid() {
		return AuthResult{}, apperr.Validation(apperr.Field("client.kind", "INVALID", "客户端类型无效"))
	}
	now := s.clock.Now()
	for _, k := range []string{"login-email:" + key, "login-ip:" + clientIP} {
		if ok, wait := s.limiter.Allow(k, s.policy.LoginFailPerWindow, s.policy.LoginFailWindow, now); !ok {
			return AuthResult{}, apperr.RateLimited(wait)
		}
	}
	acc, found, err := s.store.AccountByEmailKey(ctx, key)
	if err != nil {
		return AuthResult{}, apperr.Internal(err)
	}
	hash := security.DummyHash
	if found {
		hash = acc.PasswordHash
	}
	ok, err := s.hasher.Verify(hash, password)
	if err != nil || !ok || !found {
		return AuthResult{}, invalid
	}
	if acc.Status != "active" {
		return AuthResult{}, apperr.Forbidden("ACCOUNT_DELETING", "账号正在注销")
	}
	tokens, err := s.sessions.Open(ctx, acc.ID, client)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{Account: acc, Tokens: tokens}, nil
}

// ResetPassword 用验证码重置密码并撤销全部会话。账号不存在时返回与成功相同的结果。
func (s *IdentityService) ResetPassword(ctx context.Context, challengeID uuid.UUID, rawEmail, code, newPassword string) error {
	_, key, err := NormalizeEmail(rawEmail)
	if err != nil {
		return apperr.Validation(apperr.Field("email", "INVALID", err.Error()))
	}
	if err := ValidatePassword(newPassword); err != nil {
		return apperr.Validation(apperr.Field("new_password", "INVALID", err.Error()))
	}
	if err := s.verifyChallenge(ctx, challengeID, PurposeResetPassword, key, code); err != nil {
		return err
	}
	acc, found, err := s.store.AccountByEmailKey(ctx, key)
	if err != nil {
		return apperr.Internal(err)
	}
	if !found {
		return nil
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return apperr.Internal(err)
	}
	now := s.clock.Now()
	if err := s.store.UpdatePassword(ctx, acc.ID, hash, now); err != nil {
		return apperr.Internal(err)
	}
	if err := s.store.RevokeAccountSessions(ctx, acc.ID, now); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// Reauthenticate 验证当前密码并把会话标记为近期认证。
func (s *IdentityService) Reauthenticate(ctx context.Context, a actor.Actor, password string) error {
	acc, found, err := s.store.AccountByID(ctx, a.AccountID)
	if err != nil {
		return apperr.Internal(err)
	}
	if !found {
		return apperr.Unauthorized("SESSION_EXPIRED", "")
	}
	ok, err := s.hasher.Verify(acc.PasswordHash, password)
	if err != nil || !ok {
		return apperr.Unauthorized("INVALID_CREDENTIALS", "密码不正确")
	}
	if err := s.store.SetReauthenticated(ctx, a.SessionID, s.clock.Now()); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// ChangePassword 在登录态修改密码（评审 G1）：验证当前密码，撤销其他会话，保留当前会话。
func (s *IdentityService) ChangePassword(ctx context.Context, a actor.Actor, current, next string) error {
	if err := ValidatePassword(next); err != nil {
		return apperr.Validation(apperr.Field("new_password", "INVALID", err.Error()))
	}
	acc, found, err := s.store.AccountByID(ctx, a.AccountID)
	if err != nil {
		return apperr.Internal(err)
	}
	if !found {
		return apperr.Unauthorized("SESSION_EXPIRED", "")
	}
	ok, err := s.hasher.Verify(acc.PasswordHash, current)
	if err != nil || !ok {
		return apperr.Unauthorized("INVALID_CREDENTIALS", "当前密码不正确")
	}
	hash, err := s.hasher.Hash(next)
	if err != nil {
		return apperr.Internal(err)
	}
	now := s.clock.Now()
	if err := s.store.UpdatePassword(ctx, a.AccountID, hash, now); err != nil {
		return apperr.Internal(err)
	}
	if err := s.store.RevokeOtherSessions(ctx, a.AccountID, a.SessionID, now); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// constantTimeEqual 供测试与其他包比较摘要。
func constantTimeEqual(a, b []byte) bool { return hmac.Equal(a, b) }
