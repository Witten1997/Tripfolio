// Package deletion owns user cleanup requests and the deliberately narrow receipt capability.
package deletion

import (
	"context"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
)

type Job struct {
	ID             uuid.UUID  `json:"id"`
	Scope          string     `json:"scope"`
	Status         string     `json:"status"`
	Stage          string     `json:"stage"`
	ProcessedItems int64      `json:"processed_items"`
	TotalItems     *int64     `json:"total_items"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	ErrorCode      *string    `json:"error_code"`
	Retryable      bool       `json:"retryable"`
}
type Receipt struct {
	JobID     uuid.UUID `json:"job_id"`
	Token     string    `json:"receipt_token"`
	ExpiresAt time.Time `json:"receipt_expires_at"`
}
type Result struct {
	Result write.Result `json:"result"`
	Receipt
}
type Store interface {
	Request(context.Context, actor.Actor, uuid.UUID, int64) (Result, error)
	Renew(context.Context, actor.Actor, uuid.UUID) (Receipt, error)
	GetAccount(context.Context, uuid.UUID, string) (Job, error)
	GetTrip(context.Context, actor.Actor, uuid.UUID, bool) (Job, error)
	RetryTrip(context.Context, actor.Actor, uuid.UUID, uuid.UUID, int64) (write.Result, error)
	Reconcile(context.Context) error
}
type Service struct{ store Store }

func NewService(s Store) *Service { return &Service{store: s} }
func (s *Service) Request(ctx context.Context, a actor.Actor, operation uuid.UUID, version int64, confirm bool) (Result, error) {
	if !confirm {
		return Result{}, apperr.Validation(apperr.Field("confirm", "INVALID", "必须明确确认注销账号"))
	}
	return s.store.Request(ctx, a, operation, version)
}
func (s *Service) Renew(ctx context.Context, a actor.Actor, id uuid.UUID) (Receipt, error) {
	return s.store.Renew(ctx, a, id)
}
func (s *Service) GetAccount(ctx context.Context, id uuid.UUID, token string) (Job, error) {
	return s.store.GetAccount(ctx, id, token)
}
func (s *Service) GetTrip(ctx context.Context, a actor.Actor, id uuid.UUID, byTrip bool) (Job, error) {
	return s.store.GetTrip(ctx, a, id, byTrip)
}
func (s *Service) RetryTrip(ctx context.Context, a actor.Actor, id, operation uuid.UUID, version int64, confirm bool) (write.Result, error) {
	if !confirm {
		return write.Result{}, apperr.Validation(apperr.Field("confirm", "INVALID", "必须明确确认永久删除"))
	}
	return s.store.RetryTrip(ctx, a, id, operation, version)
}
func (s *Service) Reconcile(ctx context.Context) error { return s.store.Reconcile(ctx) }

type ReconcileArgs struct{}

func (ReconcileArgs) Kind() string { return "deletion_reconcile" }
