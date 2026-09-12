// Package account 是账号业务：身份（注册、登录、找回、复验）、会话（刷新、撤销）与个人资料。
// 本包不依赖 HTTP、pgx 或 sqlc；数据访问通过 ports.go 的接口由 PostgreSQL 适配器实现。
package account

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
)

// Purpose 是验证码用途。
type Purpose string

const (
	PurposeRegister      Purpose = "register"
	PurposeResetPassword Purpose = "reset_password"
)

// Valid 判断用途是否已知。
func (p Purpose) Valid() bool { return p == PurposeRegister || p == PurposeResetPassword }

// Account 是账号与个人资料的业务模型。password_hash 永不进入此结构以外的公开输出。
type Account struct {
	ID              uuid.UUID
	Email           string
	EmailKey        string
	PasswordHash    string
	Nickname        string
	AvatarAssetID   *uuid.UUID
	DefaultTimezone string
	Status          string
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Session 是登录会话。
type Session struct {
	ID                       uuid.UUID
	AccountID                uuid.UUID
	ClientKind               actor.ClientKind
	DeviceID                 uuid.UUID
	DeviceName               *string
	RefreshTokenHash         []byte
	PreviousRefreshTokenHash []byte
	PreviousRotatedAt        *time.Time
	CSRFTokenHash            []byte
	ReauthenticatedAt        *time.Time
	CreatedAt                time.Time
	LastSeenAt               time.Time
	ExpiresAt                time.Time
	RevokedAt                *time.Time
}

// Active 判断会话在 now 是否可用。
func (s Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && s.ExpiresAt.After(now)
}

// Challenge 是邮箱验证码挑战。
type Challenge struct {
	ID             uuid.UUID
	Purpose        Purpose
	EmailKey       string
	CodeMAC        []byte
	KeyID          string
	Attempts       int
	DeliveryStatus string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	ConsumedAt     *time.Time
	InvalidatedAt  *time.Time
}

// ClientInfo 是登录或注册时客户端的自述。
type ClientInfo struct {
	Kind       actor.ClientKind
	DeviceID   uuid.UUID
	DeviceName *string
}

// Tokens 是一次签发的令牌对。CSRFToken 只对网页会话非空。
type Tokens struct {
	AccessToken      string
	AccessExpiresIn  time.Duration
	RefreshToken     string
	RefreshExpiresAt time.Time
	CSRFToken        string
	SessionID        uuid.UUID
}

// AuthResult 是注册、登录、刷新的结果。
type AuthResult struct {
	Account Account
	Tokens  Tokens
}

// Policy 集中会话与验证码的时间参数，便于测试调整。
type Policy struct {
	AccessTokenTTL      time.Duration
	RefreshTokenTTL     time.Duration
	RefreshGrace        time.Duration
	ReauthWindow        time.Duration
	ChallengeTTL        time.Duration
	ChallengeResend     time.Duration
	ChallengePerHour    int
	ChallengeMaxAttempt int
	LoginFailPerWindow  int
	LoginFailWindow     time.Duration
}

// DefaultPolicy 是接口设计 3.1 与数据库表 3 的默认值。
func DefaultPolicy() Policy {
	return Policy{
		AccessTokenTTL:      15 * time.Minute,
		RefreshTokenTTL:     30 * 24 * time.Hour,
		RefreshGrace:        60 * time.Second,
		ReauthWindow:        5 * time.Minute,
		ChallengeTTL:        10 * time.Minute,
		ChallengeResend:     60 * time.Second,
		ChallengePerHour:    5,
		ChallengeMaxAttempt: 5,
		LoginFailPerWindow:  10,
		LoginFailWindow:     15 * time.Minute,
	}
}
