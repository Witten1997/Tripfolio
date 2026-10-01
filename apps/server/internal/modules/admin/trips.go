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
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/member"
	"tripfolio/server/internal/modules/travel/packing"
	"tripfolio/server/internal/modules/travel/todo"
	"tripfolio/server/internal/modules/travel/trip"
)

type Owner struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	Nickname string    `json:"nickname"`
}
type TripSummary struct {
	ID          uuid.UUID  `json:"id"`
	Owner       Owner      `json:"owner"`
	Name        string     `json:"name"`
	Destination string     `json:"destination"`
	StartDate   string     `json:"start_date"`
	EndDate     string     `json:"end_date"`
	Phase       string     `json:"phase"`
	ArchivedAt  *time.Time `json:"archived_at"`
	DeletedAt   *time.Time `json:"deleted_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
type TripFilter struct {
	AccountID                                       *uuid.UUID
	Query, Phase, Archived, Trash, DateFrom, DateTo string
	Page, PageSize                                  int
}
type TripPage struct {
	Data     []TripSummary `json:"data"`
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}
type TripDetail struct {
	Trip  trip.Resource `json:"trip"`
	Owner Owner         `json:"owner"`
	Phase trip.Phase    `json:"phase"`
}
type ContentFilter struct {
	Kind           string
	Page, PageSize int
}
type ContentPage struct {
	OwnerID   uuid.UUID            `json:"owner_id"`
	TripID    uuid.UUID            `json:"trip_id"`
	Kind      string               `json:"kind"`
	Itinerary []itinerary.Resource `json:"itinerary"`
	Packing   []packing.Resource   `json:"packing"`
	Todos     []todo.Resource      `json:"todos"`
	Members   []member.Resource    `json:"members"`
	Total     int64                `json:"total"`
	Page      int                  `json:"page"`
	PageSize  int                  `json:"page_size"`
}

func validPage(page, size int) bool { return page >= 1 && page <= 10000 && size >= 1 && size <= 100 }
func validDateRange(from, to string) bool {
	for _, value := range []string{from, to} {
		if value != "" {
			if _, err := time.Parse(time.DateOnly, value); err != nil {
				return false
			}
		}
	}
	return from == "" || to == "" || from <= to
}
func (s *Service) recordRead(ctx context.Context, a Audit) error {
	if err := s.store.Audit(ctx, a); err != nil {
		return apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(err)
	}
	return nil
}
func (s *Service) Trips(ctx context.Context, sess Session, f TripFilter, info RequestInfo) (TripPage, error) {
	f.Query = strings.TrimSpace(f.Query)
	if !validPage(f.Page, f.PageSize) || utf8.RuneCountInString(f.Query) > 120 || !validDateRange(f.DateFrom, f.DateTo) || (f.Phase != "" && !trip.Phase(f.Phase).Valid()) || !trip.ArchivedFilter(f.Archived).Valid() || (f.Trash != "exclude" && f.Trash != "only" && f.Trash != "all") {
		return TripPage{}, apperr.BadRequest("MALFORMED_REQUEST", "旅行筛选条件或分页参数无效")
	}
	result, err := s.store.Trips(ctx, f, s.clock.Now())
	if err != nil {
		return TripPage{}, apperr.Internal(err)
	}
	ids, owners := make([]uuid.UUID, 0, len(result.Data)), make([]uuid.UUID, 0, len(result.Data))
	for _, v := range result.Data {
		ids = append(ids, v.ID)
		owners = append(owners, v.Owner.ID)
	}
	a := s.event(&sess, "trip.list", "success", info)
	a.ResourceType = "trip"
	a.SubjectID = f.AccountID
	a.Details = map[string]any{"query_fingerprint": fmt.Sprintf("%x", sha256.Sum256([]byte(f.Query))), "account_id": f.AccountID, "phase": f.Phase, "archived": f.Archived, "trash": f.Trash, "date_from": f.DateFrom, "date_to": f.DateTo, "page": f.Page, "page_size": f.PageSize, "result_count": len(ids), "resource_ids": ids, "owner_ids": owners}
	if err = s.recordRead(ctx, a); err != nil {
		return TripPage{}, err
	}
	return result, nil
}
func (s *Service) Trip(ctx context.Context, sess Session, id uuid.UUID, info RequestInfo) (TripDetail, error) {
	result, found, err := s.store.Trip(ctx, id, s.clock.Now())
	if err != nil {
		return TripDetail{}, apperr.Internal(err)
	}
	a := s.event(&sess, "trip.read", "success", info)
	a.ResourceType = "trip"
	a.ResourceID = &id
	a.SubjectID = nil
	if found {
		a.SubjectID = &result.Owner.ID
	} else {
		a.Result = "failure"
	}
	if err = s.recordRead(ctx, a); err != nil {
		return TripDetail{}, err
	}
	if !found {
		return TripDetail{}, apperr.NotFound()
	}
	return result, nil
}
func (s *Service) TripContent(ctx context.Context, sess Session, id uuid.UUID, f ContentFilter, info RequestInfo) (ContentPage, error) {
	if !validPage(f.Page, f.PageSize) || (f.Kind != "itinerary" && f.Kind != "packing" && f.Kind != "todos" && f.Kind != "members") {
		return ContentPage{}, apperr.BadRequest("MALFORMED_REQUEST", "内容类型或分页参数无效")
	}
	result, found, err := s.store.TripContent(ctx, id, f)
	if err != nil {
		return ContentPage{}, apperr.Internal(err)
	}
	ids := []uuid.UUID{}
	for _, v := range result.Itinerary {
		ids = append(ids, v.ID)
	}
	for _, v := range result.Packing {
		ids = append(ids, v.ID)
	}
	for _, v := range result.Todos {
		ids = append(ids, v.ID)
	}
	for _, v := range result.Members {
		ids = append(ids, v.ID)
	}
	a := s.event(&sess, "trip.content.read", "success", info)
	a.ResourceType = "trip"
	a.ResourceID = &id
	a.SubjectID = nil
	if found {
		a.SubjectID = &result.OwnerID
	} else {
		a.Result = "failure"
	}
	a.Details = map[string]any{"kind": f.Kind, "page": f.Page, "page_size": f.PageSize, "resource_ids": ids, "result_count": len(ids)}
	if err = s.recordRead(ctx, a); err != nil {
		return ContentPage{}, err
	}
	if !found {
		return ContentPage{}, apperr.NotFound()
	}
	return result, nil
}
