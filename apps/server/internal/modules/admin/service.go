package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/adapters/ratelimit"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/modules/account"
)

type Service struct {
	store   Store
	hasher  *security.PasswordHasher
	limiter *ratelimit.Limiter
	clock   clock.Clock
}

func NewService(store Store, hasher *security.PasswordHasher, clk clock.Clock) *Service {
	return &Service{store: store, hasher: hasher, limiter: ratelimit.New(), clock: clk}
}

func (s *Service) event(sess *Session, action, result string, info RequestInfo) Audit {
	a := Audit{ID: uuid.New(), Action: action, Result: result, Request: info, OccurredAt: s.clock.Now()}
	if sess != nil {
		a.ActorID = &sess.AccountID
		a.SubjectID = &sess.AccountID
		a.SessionID = &sess.ID
	}
	return a
}

func (s *Service) Record(ctx context.Context, sess *Session, action, result string, info RequestInfo) error {
	if err := s.store.Audit(ctx, s.event(sess, action, result, info)); err != nil {
		return apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(err)
	}
	return nil
}

func (s *Service) Login(ctx context.Context, email, password string, info RequestInfo) (Session, string, error) {
	invalid := apperr.Unauthorized("ADMIN_INVALID_CREDENTIALS", "邮箱、密码不正确或账号没有后台权限")
	_, key, normalizeErr := account.NormalizeEmail(email)
	if normalizeErr != nil {
		key = "invalid"
	}
	for _, k := range []string{"admin-login-ip:" + info.IP, "admin-login-email:" + key} {
		if ok, wait := s.limiter.Allow(k, 10, 15*time.Minute, s.clock.Now()); !ok {
			if err := s.Record(ctx, nil, "login.rate_limited", "denied", info); err != nil {
				return Session{}, "", err
			}
			return Session{}, "", apperr.RateLimited(wait)
		}
	}
	acc, enabled, err := s.store.Credentials(ctx, key)
	if err != nil {
		return Session{}, "", apperr.Internal(err)
	}
	hash := security.DummyHash
	if acc.ID != uuid.Nil {
		hash = acc.PasswordHash
	}
	valid, verifyErr := s.hasher.Verify(hash, password)
	if normalizeErr != nil || verifyErr != nil || !valid || !enabled || acc.Status != "active" {
		a := s.event(nil, "login", "denied", info)
		a.Details = map[string]any{"email_fingerprint": fmt.Sprintf("%x", sha256.Sum256([]byte(key)))}
		if err := s.store.Audit(ctx, a); err != nil {
			return Session{}, "", apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(err)
		}
		return Session{}, "", invalid
	}
	secret, err := security.RandomToken()
	if err != nil {
		return Session{}, "", apperr.Internal(err)
	}
	now := s.clock.Now()
	sess := Session{ID: uuid.New(), AccountID: acc.ID, Email: acc.Email, Nickname: acc.Nickname, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(SessionLifetime), ReauthenticatedAt: &now, IP: info.IP, UserAgent: info.UserAgent}
	created, err := s.store.OpenSession(ctx, sess, security.Digest(secret), hash, s.event(&sess, "login", "success", info))
	if err != nil {
		return Session{}, "", apperr.Internal(err)
	}
	if !created {
		if err := s.Record(ctx, nil, "login", "denied", info); err != nil {
			return Session{}, "", err
		}
		return Session{}, "", invalid
	}
	return sess, secret, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if len(token) != 43 {
		return Session{}, apperr.Unauthorized("ADMIN_SESSION_EXPIRED", "请登录管理后台")
	}
	sess, ok, err := s.store.Authenticate(ctx, security.Digest(token), s.clock.Now(), SessionIdle)
	if err != nil {
		return Session{}, apperr.Internal(err)
	}
	if !ok {
		return Session{}, apperr.Unauthorized("ADMIN_SESSION_EXPIRED", "后台登录已失效，请重新登录")
	}
	return sess, nil
}

// CSRFToken 从仅服务端可读的随机会话凭证派生；不会暴露会话凭证本身。
func CSRFToken(token string) string {
	sum := sha256.Sum256([]byte("tripfolio-admin-csrf:" + token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func ValidCSRF(token, value string) bool {
	return len(token) == 43 && subtle.ConstantTimeCompare([]byte(CSRFToken(token)), []byte(value)) == 1
}

func (s *Service) ListSessions(ctx context.Context, sess Session, info RequestInfo) ([]Session, error) {
	items, err := s.store.Sessions(ctx, sess.AccountID, s.clock.Now(), SessionIdle)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err = s.Record(ctx, &sess, "session.list", "success", info); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) RevokeSession(ctx context.Context, sess Session, id uuid.UUID, info RequestInfo) error {
	a := s.event(&sess, "session.revoke", "success", info)
	a.ResourceType = "admin_session"
	a.ResourceID = &id
	if err := s.store.RevokeSession(ctx, sess.AccountID, id, a); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func (s *Service) Reauthenticate(ctx context.Context, sess Session, password string, info RequestInfo) error {
	if ok, wait := s.limiter.Allow("admin-reauth:"+sess.AccountID.String(), 5, 5*time.Minute, s.clock.Now()); !ok {
		if err := s.Record(ctx, &sess, "reauthenticate.rate_limited", "denied", info); err != nil {
			return err
		}
		return apperr.RateLimited(wait)
	}
	_, key, err := account.NormalizeEmail(sess.Email)
	if err != nil {
		return apperr.Internal(err)
	}
	acc, enabled, err := s.store.Credentials(ctx, key)
	if err != nil {
		return apperr.Internal(err)
	}
	valid := false
	if enabled && acc.ID == sess.AccountID && acc.Status == "active" {
		valid, err = s.hasher.Verify(acc.PasswordHash, password)
	}
	if err != nil || !valid {
		if err := s.Record(ctx, &sess, "reauthenticate", "denied", info); err != nil {
			return err
		}
		return apperr.Unauthorized("ADMIN_PASSWORD_INVALID", "密码不正确，请重新输入")
	}
	updated, err := s.store.Reauthenticate(ctx, sess.AccountID, sess.ID, acc.PasswordHash, s.event(&sess, "reauthenticate", "success", info))
	if err != nil {
		return apperr.Internal(err)
	}
	if !updated {
		return apperr.Unauthorized("ADMIN_SESSION_EXPIRED", "后台登录已失效，请重新登录")
	}
	return nil
}
