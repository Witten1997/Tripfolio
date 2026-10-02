package admin

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/trip"
)

// TripEdits intentionally excludes notes and route configuration.
type TripEdits struct {
	Name         *string `json:"name,omitempty"`
	Destination  *string `json:"destination,omitempty"`
	StartDate    *string `json:"start_date,omitempty"`
	EndDate      *string `json:"end_date,omitempty"`
	Timezone     *string `json:"timezone,omitempty"`
	CurrencyCode *string `json:"currency_code,omitempty"`
	BudgetSet    bool    `json:"budget_set"`
	BudgetAmount *string `json:"budget_amount"`
}

func (e TripEdits) Patch() trip.Patch {
	return trip.Patch{Name: e.Name, Destination: e.Destination, StartDate: e.StartDate, EndDate: e.EndDate, Timezone: e.Timezone, CurrencyCode: e.CurrencyCode, BudgetSet: e.BudgetSet, BudgetAmount: e.BudgetAmount}
}

type TripMutation struct {
	Action   string     `json:"action"`
	Version  int64      `json:"version"`
	Reason   string     `json:"reason"`
	Changes  *TripEdits `json:"changes,omitempty"`
	Archived *bool      `json:"archived,omitempty"`
	Confirm  bool       `json:"confirm"`
}

type TripMutationResult struct {
	Trip     TripOverview `json:"trip"`
	Warnings []string     `json:"warnings"`
	JobID    *uuid.UUID   `json:"job_id"`
}

type TripLifecycleStore interface {
	MutateTrip(context.Context, Session, uuid.UUID, TripMutation, *Audit) (TripMutationResult, error)
}

func (s *Service) WithTripLifecycle(store TripLifecycleStore) *Service {
	s.tripLifecycle = store
	return s
}

func OverviewOf(r trip.Resource) TripOverview {
	return TripOverview{ID: r.ID, Name: r.Name, Destination: r.Destination, StartDate: string(r.StartDate), EndDate: string(r.EndDate), Timezone: r.Timezone, CurrencyCode: r.CurrencyCode, BudgetAmount: r.BudgetAmount, Version: int64(r.Version), ArchivedAt: r.ArchivedAt, DeletedAt: r.DeletedAt, PurgeAfterAt: r.PurgeAfterAt, PurgeRequestedAt: r.PurgeRequestedAt, UpdatedAt: r.UpdatedAt}
}

func (s *Service) MutateTrip(ctx context.Context, sess Session, id uuid.UUID, cmd TripMutation, info RequestInfo) (TripMutationResult, error) {
	a := s.event(&sess, "trip."+cmd.Action, "success", info)
	a.SubjectID = nil
	a.ResourceType, a.ResourceID = "trip", &id
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	a.Reason = cmd.Reason
	var err error
	var result TripMutationResult
	if cmd.Version < 1 || utf8.RuneCountInString(cmd.Reason) < 1 || utf8.RuneCountInString(cmd.Reason) > 500 {
		err = apperr.BadRequest("MALFORMED_REQUEST", "请提供有效版本及 1–500 字的操作原因")
	} else if (cmd.Action == "edit") != (cmd.Changes != nil) || (cmd.Action == "archive") != (cmd.Archived != nil) {
		err = apperr.BadRequest("MALFORMED_REQUEST", "操作参数不匹配")
	} else {
		switch cmd.Action {
		case "edit", "archive", "trash", "restore":
		case "purge", "retry":
			if !cmd.Confirm {
				err = apperr.BadRequest("MALFORMED_REQUEST", "请确认永久清理整趟旅行及其关联内容")
			}
			if sess.ReauthenticatedAt == nil || s.clock.Now().Before(*sess.ReauthenticatedAt) || s.clock.Now().Sub(*sess.ReauthenticatedAt) > ReauthWindow {
				err = apperr.Forbidden("ADMIN_REAUTH_REQUIRED", "请重新验证密码")
			}
		default:
			a.Action = "trip.invalid"
			err = apperr.BadRequest("MALFORMED_REQUEST", "旅行操作无效")
		}
	}
	if err == nil {
		if s.tripLifecycle == nil {
			err = apperr.New(503, "DEPENDENCY_UNAVAILABLE", "旅行管理暂不可用")
		} else {
			result, err = s.tripLifecycle.MutateTrip(ctx, sess, id, cmd, &a)
		}
	}
	if err != nil {
		a.Result, a.Details = "failure", nil
		// Never persist unbounded input or a private user resource in denial audits.
		if utf8.RuneCountInString(a.Reason) > 500 {
			a.Reason = ""
		}
		if auditErr := s.recordRead(ctx, a); auditErr != nil {
			return TripMutationResult{}, auditErr
		}
		if e, ok := apperr.As(err); ok && e.Status == 422 && len(e.Fields) > 0 {
			e.Detail = e.Fields[0].Message
		}
		return TripMutationResult{}, err
	}
	return result, nil
}

func RestoreAllowed(r TripOverview, now time.Time) bool {
	return r.DeletedAt != nil && r.PurgeRequestedAt == nil && r.PurgeAfterAt != nil && now.Before(*r.PurgeAfterAt)
}
