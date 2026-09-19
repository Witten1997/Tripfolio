package account

import (
	"context"
	"crypto/hmac"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
)

// SessionService 管理会话生命周期：开启、刷新（含 60 秒宽限）、撤销、列表与请求期校验。
type SessionService struct {
	store  Store
	tokens *security.TokenIssuer
	clock  clock.Clock
	policy Policy
	cache  *authCache
}

// NewSessionService 创建服务。
func NewSessionService(store Store, tokens *security.TokenIssuer, clk clock.Clock, policy Policy) *SessionService {
	return &SessionService{store: store, tokens: tokens, clock: clk, policy: policy, cache: newAuthCache(policy.AuthCacheTTL)}
}

// InvalidateSession 使指定会话的鉴权缓存失效；会话状态被改写后调用。
func (s *SessionService) InvalidateSession(id uuid.UUID) { s.cache.remove(id) }

// InvalidateAccount 使账号下全部会话的鉴权缓存失效；批量撤销会话或账号状态变化后调用。
func (s *SessionService) InvalidateAccount(accountID uuid.UUID) { s.cache.removeAccount(accountID) }

// Open 创建会话并签发令牌对。网页会话同时签发 CSRF 令牌。
func (s *SessionService) Open(ctx context.Context, accountID uuid.UUID, client ClientInfo) (Tokens, error) {
	now := s.clock.Now()
	sessionID := uuid.New()
	secret, err := security.RandomToken()
	if err != nil {
		return Tokens{}, apperr.Internal(err)
	}
	var csrf string
	var csrfHash []byte
	if client.Kind == actor.ClientWeb {
		if csrf, err = security.RandomToken(); err != nil {
			return Tokens{}, apperr.Internal(err)
		}
		csrfHash = security.Digest(csrf)
	}
	session := Session{
		ID: sessionID, AccountID: accountID, ClientKind: client.Kind, DeviceID: client.DeviceID, DeviceName: client.DeviceName,
		RefreshTokenHash: security.Digest(secret), CSRFTokenHash: csrfHash,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(s.policy.RefreshTokenTTL),
	}
	if _, err := s.store.CreateSession(ctx, session); err != nil {
		return Tokens{}, apperr.Internal(err)
	}
	access, err := s.tokens.Issue(accountID, sessionID, now)
	if err != nil {
		return Tokens{}, apperr.Internal(err)
	}
	return Tokens{
		AccessToken: access, AccessExpiresIn: s.policy.AccessTokenTTL,
		RefreshToken: formatRefreshToken(sessionID, secret), RefreshExpiresAt: session.ExpiresAt,
		CSRFToken: csrf, SessionID: sessionID,
	}, nil
}

func formatRefreshToken(sessionID uuid.UUID, secret string) string {
	return sessionID.String() + "." + secret
}

// ParseRefreshToken 拆出会话 ID 与 secret。
func ParseRefreshToken(raw string) (uuid.UUID, string, bool) {
	idPart, secret, ok := strings.Cut(strings.TrimSpace(raw), ".")
	if !ok || secret == "" {
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(idPart)
	if err != nil {
		return uuid.Nil, "", false
	}
	return id, secret, true
}

// Refresh 按接口设计 3.1 轮换：命中当前摘要 → 轮换；命中前次摘要且在宽限期内 → 重发新令牌对；
// 命中前次摘要但超出宽限期 → 视为复用，撤销会话；其他 → 拒绝。
func (s *SessionService) Refresh(ctx context.Context, rawRefresh string) (AuthResult, error) {
	expired := apperr.Unauthorized("SESSION_EXPIRED", "登录已失效，请重新登录")
	sessionID, secret, ok := ParseRefreshToken(rawRefresh)
	if !ok {
		return AuthResult{}, expired
	}
	now := s.clock.Now()
	presented := security.Digest(secret)
	newSecret, err := security.RandomToken()
	if err != nil {
		return AuthResult{}, apperr.Internal(err)
	}
	var newCSRF string
	var reuse bool

	updated, err := s.store.RotateSessionTx(ctx, sessionID, func(sess Session) (SessionRotation, error) {
		if !sess.Active(now) {
			return SessionRotation{}, expired
		}
		var csrfHash []byte
		if sess.ClientKind == actor.ClientWeb {
			c, err := security.RandomToken()
			if err != nil {
				return SessionRotation{}, err
			}
			newCSRF, csrfHash = c, security.Digest(c)
		}
		newHash := security.Digest(newSecret)
		switch {
		case hmac.Equal(presented, sess.RefreshTokenHash):
			rotatedAt := now
			return SessionRotation{Kind: "rotate", RefreshTokenHash: newHash, PreviousRefreshTokenHash: sess.RefreshTokenHash,
				PreviousRotatedAt: &rotatedAt, CSRFTokenHash: csrfHash, LastSeenAt: now}, nil
		case sess.PreviousRefreshTokenHash != nil && hmac.Equal(presented, sess.PreviousRefreshTokenHash):
			if sess.PreviousRotatedAt != nil && now.Sub(*sess.PreviousRotatedAt) <= s.policy.RefreshGrace {
				return SessionRotation{Kind: "reissue", RefreshTokenHash: newHash, PreviousRefreshTokenHash: sess.PreviousRefreshTokenHash,
					PreviousRotatedAt: sess.PreviousRotatedAt, CSRFTokenHash: csrfHash, LastSeenAt: now}, nil
			}
			reuse = true
			return SessionRotation{}, expired
		default:
			return SessionRotation{}, expired
		}
	})
	if err != nil {
		if reuse {
			_ = s.store.RevokeSession(ctx, sessionID, now)
			s.cache.remove(sessionID)
		}
		if _, ok := apperr.As(err); ok {
			return AuthResult{}, err
		}
		return AuthResult{}, apperr.Internal(err)
	}
	acc, found, err := s.store.AccountByID(ctx, updated.AccountID)
	if err != nil {
		return AuthResult{}, apperr.Internal(err)
	}
	if !found || acc.Status != "active" {
		return AuthResult{}, apperr.Forbidden("ACCOUNT_DELETING", "账号正在注销")
	}
	access, err := s.tokens.Issue(updated.AccountID, updated.ID, now)
	if err != nil {
		return AuthResult{}, apperr.Internal(err)
	}
	return AuthResult{Account: acc, Tokens: Tokens{
		AccessToken: access, AccessExpiresIn: s.policy.AccessTokenTTL,
		RefreshToken: formatRefreshToken(updated.ID, newSecret), RefreshExpiresAt: updated.ExpiresAt,
		CSRFToken: newCSRF, SessionID: updated.ID,
	}}, nil
}

// Authenticate 校验访问令牌并核对会话与账号状态，返回 Actor。
// 令牌本身有签名，解析不查库；会话与账号状态优先取进程内缓存，未命中时用一条 JOIN 查询读取并回填。
func (s *SessionService) Authenticate(ctx context.Context, accessToken string) (actor.Actor, error) {
	now := s.clock.Now()
	claims, err := s.tokens.Parse(accessToken, now)
	if err != nil {
		return actor.Actor{}, apperr.Unauthorized("SESSION_EXPIRED", "登录已失效，请重新登录")
	}
	if a, touch, ok := s.cache.lookup(claims.SessionID, now); ok && a.AccountID == claims.AccountID {
		if touch {
			s.touchAsync(claims.SessionID, now)
		}
		return a, nil
	}
	sess, acc, found, err := s.store.SessionWithAccount(ctx, claims.SessionID)
	if err != nil {
		return actor.Actor{}, apperr.Internal(err)
	}
	if !found || sess.AccountID != claims.AccountID || !sess.Active(now) {
		return actor.Actor{}, apperr.Unauthorized("SESSION_EXPIRED", "登录已失效，请重新登录")
	}
	a := actor.Actor{
		AccountID: acc.ID, SessionID: sess.ID, ClientKind: sess.ClientKind,
		AccountStatus: acc.Status, ReauthenticatedAt: sess.ReauthenticatedAt,
	}
	if s.cache.store(sess.ID, a, sess.ExpiresAt, sess.LastSeenAt, now) {
		s.touchAsync(sess.ID, now)
	}
	return a, nil
}

// touchAsync 在请求之外写回 last_seen_at：它只是活跃度记录，不值得让请求多等一次网络往返。
func (s *SessionService) touchAsync(id uuid.UUID, now time.Time) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.store.TouchSession(ctx, id, now)
	}()
}

// VerifyCSRF 校验网页会话的双重提交令牌：请求头值的摘要必须等于会话保存的摘要。
func (s *SessionService) VerifyCSRF(ctx context.Context, sessionID uuid.UUID, headerToken string) error {
	sess, found, err := s.store.SessionByID(ctx, sessionID)
	if err != nil {
		return apperr.Internal(err)
	}
	if !found || sess.ClientKind != actor.ClientWeb || headerToken == "" ||
		!hmac.Equal(security.Digest(headerToken), sess.CSRFTokenHash) {
		return apperr.Forbidden("CSRF_FAILED", "跨站请求校验失败")
	}
	return nil
}

// Logout 撤销当前会话；重复撤销仍成功。
func (s *SessionService) Logout(ctx context.Context, sessionID uuid.UUID) error {
	if err := s.store.RevokeSession(ctx, sessionID, s.clock.Now()); err != nil {
		return apperr.Internal(err)
	}
	s.cache.remove(sessionID)
	return nil
}

// List 返回本人的有效会话。
func (s *SessionService) List(ctx context.Context, a actor.Actor) ([]Session, error) {
	sessions, err := s.store.ListActiveSessions(ctx, a.AccountID, s.clock.Now())
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return sessions, nil
}

// Revoke 撤销本人指定会话；不存在或非本人也返回成功，不暴露存在性。
func (s *SessionService) Revoke(ctx context.Context, a actor.Actor, sessionID uuid.UUID) error {
	sess, found, err := s.store.SessionByID(ctx, sessionID)
	if err != nil {
		return apperr.Internal(err)
	}
	if !found || sess.AccountID != a.AccountID {
		return nil
	}
	if err := s.store.RevokeSession(ctx, sessionID, s.clock.Now()); err != nil {
		return apperr.Internal(err)
	}
	s.cache.remove(sessionID)
	return nil
}

// ErrNoSession 供适配器在会话不存在时返回，Refresh 将其映射为 SESSION_EXPIRED。
var ErrNoSession = errors.New("session not found")
