package account

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/types"
)

// AccountResource 是 GET /account 的规范表示（接口设计 3.1）。password_hash 与 email_key 不在其中。
type AccountResource struct {
	ID              uuid.UUID     `json:"id"`
	Email           string        `json:"email"`
	Nickname        string        `json:"nickname"`
	AvatarAssetID   *uuid.UUID    `json:"avatar_asset_id"`
	DefaultTimezone string        `json:"default_timezone"`
	Status          string        `json:"status"`
	Version         types.Version `json:"version"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

// Resource 把业务模型转为对外表示。
func (a Account) Resource() AccountResource {
	return AccountResource{
		ID: a.ID, Email: a.Email, Nickname: a.Nickname, AvatarAssetID: a.AvatarAssetID, DefaultTimezone: a.DefaultTimezone,
		Status: a.Status, Version: types.Version(a.Version), CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(),
	}
}

// SessionResource 是会话列表项；不含任何令牌材料。
type SessionResource struct {
	ID         uuid.UUID        `json:"id"`
	ClientKind actor.ClientKind `json:"client_kind"`
	DeviceName *string          `json:"device_name"`
	CreatedAt  time.Time        `json:"created_at"`
	LastSeenAt time.Time        `json:"last_seen_at"`
	ExpiresAt  time.Time        `json:"expires_at"`
	IsCurrent  bool             `json:"is_current"`
}

// Resource 把会话转为对外表示；current 标记是否为发起请求的会话。
func (s Session) Resource(current uuid.UUID) SessionResource {
	return SessionResource{
		ID: s.ID, ClientKind: s.ClientKind, DeviceName: s.DeviceName, CreatedAt: s.CreatedAt.UTC(),
		LastSeenAt: s.LastSeenAt.UTC(), ExpiresAt: s.ExpiresAt.UTC(), IsCurrent: s.ID == current,
	}
}
