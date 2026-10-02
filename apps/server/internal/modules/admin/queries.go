package admin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
)

type SignupDay struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type Overview struct {
	Users         int64       `json:"users"`
	NewUsersToday int64       `json:"new_users_today"`
	Trips         int64       `json:"trips"`
	FailedJobs    int64       `json:"failed_jobs"`
	Signups       []SignupDay `json:"signups"`
	AsOf          time.Time   `json:"as_of"`
}

type User struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	Nickname     string     `json:"nickname"`
	Status       string     `json:"status"`
	IsSuperAdmin bool       `json:"is_super_admin"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
	TripCount    int64      `json:"trip_count"`
}

type UserSession struct {
	ID         uuid.UUID `json:"id"`
	ClientKind string    `json:"client_kind"`
	DeviceName string    `json:"device_name"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type UserDetail struct {
	Account  User          `json:"account"`
	Sessions []UserSession `json:"sessions"`
}

type UserFilter struct {
	Query, Status, RegisteredFrom, RegisteredTo string
	Page, PageSize                              int
}
type UserPage struct {
	Data     []User `json:"data"`
	Total    int64  `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

func (s *Service) Overview(ctx context.Context, sess Session, info RequestInfo) (Overview, error) {
	result, err := s.store.Overview(ctx, s.clock.Now())
	if err != nil {
		return Overview{}, apperr.Internal(err)
	}
	if err = s.Record(ctx, &sess, "overview.read", "success", info); err != nil {
		return Overview{}, err
	}
	return result, nil
}

func (s *Service) Users(ctx context.Context, sess Session, filter UserFilter, info RequestInfo) (UserPage, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	if filter.Page < 1 || filter.Page > 10000 || filter.PageSize < 1 || filter.PageSize > 100 || utf8.RuneCountInString(filter.Query) > 254 || (filter.Status != "" && filter.Status != "active" && filter.Status != "deleting" && filter.Status != "banned") {
		return UserPage{}, apperr.BadRequest("MALFORMED_REQUEST", "用户筛选条件或分页参数无效")
	}
	for _, date := range []string{filter.RegisteredFrom, filter.RegisteredTo} {
		if date != "" {
			if _, err := time.Parse(time.DateOnly, date); err != nil {
				return UserPage{}, apperr.BadRequest("MALFORMED_REQUEST", "注册日期无效")
			}
		}
	}
	if filter.RegisteredFrom != "" && filter.RegisteredTo != "" && filter.RegisteredFrom > filter.RegisteredTo {
		return UserPage{}, apperr.BadRequest("MALFORMED_REQUEST", "注册起始日期不能晚于结束日期")
	}
	result, err := s.store.Users(ctx, filter)
	if err != nil {
		return UserPage{}, apperr.Internal(err)
	}
	ids := make([]string, 0, len(result.Data))
	for _, user := range result.Data {
		ids = append(ids, user.ID.String())
	}
	a := s.event(&sess, "user.list", "success", info)
	a.ResourceType = "account"
	a.Details = map[string]any{"query_fingerprint": fmt.Sprintf("%x", sha256.Sum256([]byte(filter.Query))), "status": filter.Status, "page": filter.Page, "page_size": filter.PageSize, "result_count": len(ids), "resource_ids": ids}
	a.SubjectID = nil
	a.Details["registered_from"] = filter.RegisteredFrom
	a.Details["registered_to"] = filter.RegisteredTo
	if err = s.store.Audit(ctx, a); err != nil {
		return UserPage{}, apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(err)
	}
	return result, nil
}

func (s *Service) User(ctx context.Context, sess Session, id uuid.UUID, info RequestInfo) (UserDetail, error) {
	result, found, err := s.store.User(ctx, id, s.clock.Now())
	if err != nil {
		return UserDetail{}, apperr.Internal(err)
	}
	a := s.event(&sess, "user.read", "success", info)
	a.SubjectID = &id
	a.ResourceType = "account"
	a.ResourceID = &id
	if !found {
		a.Result = "failure"
	}
	if err = s.store.Audit(ctx, a); err != nil {
		return UserDetail{}, apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(err)
	}
	if !found {
		return UserDetail{}, apperr.NotFound()
	}
	return result, nil
}
