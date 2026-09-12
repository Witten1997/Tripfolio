package account

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store 是账号包需要的持久化能力；PostgreSQL 适配器实现。所有方法在调用方给定的事务或连接上执行。
type Store interface {
	AccountByEmailKey(ctx context.Context, emailKey string) (Account, bool, error)
	AccountByID(ctx context.Context, id uuid.UUID) (Account, bool, error)
	AccountExists(ctx context.Context, emailKey string) (bool, error)
	// CreateAccount 在一个事务中创建账号、同步状态与预设账单分类。
	CreateAccount(ctx context.Context, a Account) (Account, error)
	UpdatePassword(ctx context.Context, id uuid.UUID, hash string, now time.Time) error
	UpdateProfile(ctx context.Context, id uuid.UUID, nickname string, avatarAssetID *uuid.UUID, timezone string, now time.Time) (Account, error)
	SetAccountStatus(ctx context.Context, id uuid.UUID, status string, now time.Time) error

	CreateSession(ctx context.Context, s Session) (Session, error)
	SessionByID(ctx context.Context, id uuid.UUID) (Session, bool, error)
	// RotateSessionTx 在行锁下执行 fn，fn 返回要写回的会话；用于刷新令牌。
	RotateSessionTx(ctx context.Context, id uuid.UUID, fn func(s Session) (SessionRotation, error)) (Session, error)
	TouchSession(ctx context.Context, id uuid.UUID, now time.Time) error
	SetReauthenticated(ctx context.Context, id uuid.UUID, now time.Time) error
	RevokeSession(ctx context.Context, id uuid.UUID, now time.Time) error
	RevokeAccountSessions(ctx context.Context, accountID uuid.UUID, now time.Time) error
	RevokeOtherSessions(ctx context.Context, accountID, keep uuid.UUID, now time.Time) error
	ListActiveSessions(ctx context.Context, accountID uuid.UUID, now time.Time) ([]Session, error)

	// IssueChallengeTx 串行化同一邮箱、用途的申请：检查发送间隔与小时次数，作废旧挑战并创建新挑战。
	IssueChallengeTx(ctx context.Context, c Challenge, resendAfter time.Duration, perHour int) (Challenge, error)
	// VerifyChallengeTx 在行锁下执行 fn；fn 返回 consume=true 表示本次验证成功并消费挑战，
	// 返回 false 表示失败并累计尝试次数。计数与消费必须提交，不随外层业务失败回滚。
	VerifyChallengeTx(ctx context.Context, id uuid.UUID, fn func(c Challenge) (consume bool, err error)) error
	SetChallengeDelivery(ctx context.Context, id uuid.UUID, status string) error
}

// SessionRotation 描述刷新时对会话的写入。
type SessionRotation struct {
	// Kind 为 rotate（正常轮换）或 reissue（宽限期内重发，前次摘要与轮换时间不变）。
	Kind                     string
	RefreshTokenHash         []byte
	PreviousRefreshTokenHash []byte
	PreviousRotatedAt        *time.Time
	CSRFTokenHash            []byte
	LastSeenAt               time.Time
}

// ErrChallengeTooSoon 与 ErrChallengeQuotaExceeded 由 IssueChallengeTx 返回，服务层统一映射为外部通用响应。
type limitError string

func (e limitError) Error() string { return string(e) }

const (
	ErrChallengeTooSoon       limitError = "challenge requested too soon"
	ErrChallengeQuotaExceeded limitError = "challenge hourly quota exceeded"
)
