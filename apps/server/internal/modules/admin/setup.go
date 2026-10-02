package admin

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/account"
)

type SetupInput struct {
	Mode, Email, Nickname, Password string
}

// SetupCommand 仅在内部传递密码摘要或既有账号的验密版本，不进入公开响应及审计。
type SetupCommand struct {
	Create  bool
	Account account.Account
}

func (s *Service) SetupOpen(ctx context.Context) (bool, error) {
	return s.store.SetupOpen(ctx)
}

func (s *Service) Initialize(ctx context.Context, in SetupInput, info RequestInfo) error {
	open, err := s.SetupOpen(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	if !open {
		return apperr.NotFound()
	}
	for _, key := range []string{"setup-ip:" + info.IP, "setup-global"} {
		if ok, wait := s.limiter.Allow(key, 10, 15*time.Minute, s.clock.Now()); !ok {
			return s.setupFailure(ctx, info, apperr.RateLimited(wait))
		}
	}
	if in.Mode != "create" && in.Mode != "existing" {
		return s.setupFailure(ctx, info, apperr.BadRequest("MALFORMED_REQUEST", "请选择创建新账号或使用已有账号"))
	}
	email, key, err := account.NormalizeEmail(in.Email)
	if err != nil {
		return s.setupFailure(ctx, info, apperr.Validation(apperr.Field("email", "INVALID", err.Error())))
	}
	if err = account.ValidatePassword(in.Password); err != nil {
		return s.setupFailure(ctx, info, apperr.Validation(apperr.Field("password", "INVALID", err.Error())))
	}
	acc, _, err := s.store.Credentials(ctx, key)
	if err != nil {
		return apperr.Internal(err)
	}
	create := in.Mode == "create"
	if create {
		if acc.ID != uuid.Nil {
			return s.setupFailure(ctx, info, apperr.Conflicted("ACCOUNT_ALREADY_EXISTS", "该邮箱已注册，请选择使用已有账号"))
		}
		nickname := strings.TrimSpace(in.Nickname)
		if n := utf8.RuneCountInString(nickname); n < 1 || n > 64 {
			return s.setupFailure(ctx, info, apperr.Validation(apperr.Field("nickname", "INVALID", "昵称须为 1–64 个字符")))
		}
		hash, err := s.hasher.Hash(in.Password)
		if err != nil {
			return apperr.Internal(err)
		}
		now := s.clock.Now()
		acc = account.Account{ID: uuid.New(), Email: email, EmailKey: key, PasswordHash: hash,
			Nickname: nickname, DefaultTimezone: "Asia/Shanghai", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	} else {
		hash := security.DummyHash
		if acc.ID != uuid.Nil {
			hash = acc.PasswordHash
		}
		valid, err := s.hasher.Verify(hash, in.Password)
		if err != nil || !valid || acc.ID == uuid.Nil || acc.Status != "active" {
			return s.setupFailure(ctx, info, apperr.Unauthorized("ADMIN_INVALID_CREDENTIALS", "邮箱、密码不正确或账号状态不可用"))
		}
	}
	audit := s.event(nil, "setup.initialize", "success", info)
	audit.ActorID, audit.SubjectID = &acc.ID, &acc.ID
	audit.ResourceType, audit.ResourceID = "admin_principal", &acc.ID
	audit.Reason = "首次初始化超级管理员"
	audit.Details = map[string]any{"mode": in.Mode}
	return s.store.InitializeAdmin(ctx, SetupCommand{Create: create, Account: acc}, audit)
}

func (s *Service) setupFailure(ctx context.Context, info RequestInfo, err error) error {
	if auditErr := s.Record(ctx, nil, "setup.initialize", "denied", info); auditErr != nil {
		return auditErr
	}
	return err
}
