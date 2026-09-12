package account

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
)

// AvatarChecker 校验头像资产：必须属于本账号、scope=avatar 且未删除。由 assets 模块提供。
type AvatarChecker interface {
	AvatarUsable(ctx context.Context, accountID, assetID uuid.UUID) (bool, error)
}

// ProfileService 读取与修改个人资料。
type ProfileService struct {
	store   Store
	avatars AvatarChecker
	clock   clock.Clock
}

// NewProfileService 创建服务。avatars 可为 nil（此时不允许设置头像）。
func NewProfileService(store Store, avatars AvatarChecker, clk clock.Clock) *ProfileService {
	return &ProfileService{store: store, avatars: avatars, clock: clk}
}

// Get 返回本人资料。
func (s *ProfileService) Get(ctx context.Context, a actor.Actor) (Account, error) {
	acc, found, err := s.store.AccountByID(ctx, a.AccountID)
	if err != nil {
		return Account{}, apperr.Internal(err)
	}
	if !found {
		return Account{}, apperr.NotFound()
	}
	return acc, nil
}

// ProfilePatch 是资料局部更新；nil 表示缺省。AvatarAssetID 用 Set 区分显式 null。
type ProfilePatch struct {
	Nickname        *string
	DefaultTimezone *string
	AvatarSet       bool
	AvatarAssetID   *uuid.UUID
}

// Update 应用局部更新并返回新资料。资料变更不进入旅行同步流，只递增版本。
func (s *ProfileService) Update(ctx context.Context, a actor.Actor, baseVersion int64, patch ProfilePatch) (Account, []string, error) {
	acc, found, err := s.store.AccountByID(ctx, a.AccountID)
	if err != nil {
		return Account{}, nil, apperr.Internal(err)
	}
	if !found {
		return Account{}, nil, apperr.NotFound()
	}
	if acc.Version != baseVersion {
		return Account{}, nil, apperr.VersionConflict(&apperr.Conflict{
			EntityType: "account", EntityID: acc.ID, ExpectedVersion: baseVersion, CurrentVersion: acc.Version, Current: nil,
		})
	}
	nickname, tz, avatar := acc.Nickname, acc.DefaultTimezone, acc.AvatarAssetID
	var fields []apperr.FieldError
	if patch.Nickname != nil {
		nickname = strings.TrimSpace(*patch.Nickname)
		if n := utf8.RuneCountInString(nickname); n < 1 || n > 64 {
			fields = append(fields, apperr.Field("nickname", "INVALID", "昵称须为 1–64 个字符"))
		}
	}
	if patch.DefaultTimezone != nil {
		tz = *patch.DefaultTimezone
		if _, err := time.LoadLocation(tz); err != nil || tz == "" || tz == "Local" {
			fields = append(fields, apperr.Field("default_timezone", "INVALID", "不是有效的 IANA 时区"))
		}
	}
	if patch.AvatarSet {
		avatar = patch.AvatarAssetID
		if avatar != nil {
			if s.avatars == nil {
				fields = append(fields, apperr.Field("avatar_asset_id", "INVALID_REFERENCE", "当前不支持头像"))
			} else if ok, err := s.avatars.AvatarUsable(ctx, a.AccountID, *avatar); err != nil {
				return Account{}, nil, apperr.Internal(err)
			} else if !ok {
				fields = append(fields, apperr.Field("avatar_asset_id", "INVALID_REFERENCE", "头像资产不存在或不可用"))
			}
		}
	}
	if len(fields) > 0 {
		return Account{}, nil, apperr.Validation(fields...)
	}
	updated, err := s.store.UpdateProfile(ctx, a.AccountID, nickname, avatar, tz, s.clock.Now())
	if err != nil {
		return Account{}, nil, apperr.Internal(err)
	}
	return updated, nil, nil
}

// 供传输层构造 WriteResult 的资源类型名。
const EntityTypeAccount = "account"

// ResultRef 返回账号资源引用。
func ResultRef(acc Account) write.EntityRef {
	return write.Ref(EntityTypeAccount, acc.ID, acc.Version)
}
