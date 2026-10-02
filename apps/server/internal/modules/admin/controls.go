package admin

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
)

type SharingRestriction struct {
	TripID     uuid.UUID  `json:"trip_id"`
	AccountID  uuid.UUID  `json:"account_id"`
	Restricted bool       `json:"restricted"`
	Reason     string     `json:"reason"`
	ChangedAt  *time.Time `json:"changed_at"`
	Version    int64      `json:"version"`
}

type SharingControl struct {
	Restricted bool   `json:"restricted"`
	Reason     string `json:"reason"`
	Version    int64  `json:"version"`
}

func controlReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 500 {
		return "", apperr.BadRequest("ADMIN_REASON_REQUIRED", "请填写 1–500 字的操作原因")
	}
	return reason, nil
}

func (s *Service) ControlUser(ctx context.Context, sess Session, id uuid.UUID, action, reason string, info RequestInfo) error {
	a := s.event(&sess, "user."+action, "success", info)
	a.SubjectID, a.ResourceType, a.ResourceID = &id, "account", &id
	var err error
	a.Reason, err = controlReason(reason)
	if err == nil && action != "ban" && action != "unban" && action != "force-logout" {
		err = apperr.BadRequest("MALFORMED_REQUEST", "用户操作无效")
	}
	if err == nil {
		err = s.store.ControlUser(ctx, sess, id, action, a)
	}
	return s.controlResult(ctx, a, err)
}

func (s *Service) SharingRestriction(ctx context.Context, sess Session, id uuid.UUID, info RequestInfo) (SharingRestriction, error) {
	a := s.event(&sess, "trip.sharing.read", "success", info)
	a.SubjectID, a.ResourceType, a.ResourceID = nil, "trip", &id
	r, err := s.store.SharingRestriction(ctx, sess, id, a)
	if r.AccountID != uuid.Nil {
		a.SubjectID = &r.AccountID
	}
	return r, s.controlResult(ctx, a, err)
}

func (s *Service) ControlSharing(ctx context.Context, sess Session, id uuid.UUID, in SharingControl, info RequestInfo) (SharingRestriction, error) {
	action := "trip.sharing.release"
	if in.Restricted {
		action = "trip.sharing.restrict"
	}
	a := s.event(&sess, action, "success", info)
	a.SubjectID, a.ResourceType, a.ResourceID = nil, "trip", &id
	var err error
	in.Reason, err = controlReason(in.Reason)
	a.Reason = in.Reason
	if err == nil && in.Version < 0 {
		err = apperr.BadRequest("MALFORMED_REQUEST", "分享限制版本无效")
	}
	var r SharingRestriction
	if err == nil {
		r, err = s.store.ControlSharing(ctx, sess, id, in, a)
	}
	if r.AccountID != uuid.Nil {
		a.SubjectID = &r.AccountID
	}
	return r, s.controlResult(ctx, a, err)
}

func (s *Service) controlResult(ctx context.Context, a Audit, err error) error {
	if err == nil {
		return nil
	}
	if e, ok := apperr.As(err); ok && e.Code == "ADMIN_AUDIT_UNAVAILABLE" {
		return err
	}
	a.Result = "failure"
	if e, ok := apperr.As(err); ok && e.Status < 500 {
		a.Result = "denied"
	}
	if auditErr := s.store.Audit(ctx, a); auditErr != nil {
		return apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(auditErr)
	}
	if _, ok := apperr.As(err); ok {
		return err
	}
	return apperr.Internal(err)
}
