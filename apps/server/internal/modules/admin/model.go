// Package admin 管理独立的超级管理员资格、后台会话和审计。
package admin

import (
	"context"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/modules/account"
)

const (
	SessionLifetime = 8 * time.Hour
	SessionIdle     = 30 * time.Minute
	ReauthWindow    = 5 * time.Minute
)

type RequestInfo struct {
	RequestID string
	IP        string
	UserAgent string
}

type Audit struct {
	ID           uuid.UUID
	ActorID      *uuid.UUID
	SubjectID    *uuid.UUID
	SessionID    *uuid.UUID
	Action       string
	ResourceType string
	ResourceID   *uuid.UUID
	Result       string
	Reason       string
	Request      RequestInfo
	Details      map[string]any
	OccurredAt   time.Time
}

type Session struct {
	ExpectedAccountVersion int64
	ID                     uuid.UUID
	AccountID              uuid.UUID
	Email                  string
	Nickname               string
	CreatedAt              time.Time
	LastSeenAt             time.Time
	ExpiresAt              time.Time
	ReauthenticatedAt      *time.Time
	IP                     string
	UserAgent              string
}

type Store interface {
	SetupOpen(context.Context) (bool, error)
	InitializeAdmin(context.Context, SetupCommand, Audit) error
	ControlUser(context.Context, Session, uuid.UUID, string, Audit) error
	SharingRestriction(context.Context, Session, uuid.UUID, Audit) (SharingRestriction, error)
	ControlSharing(context.Context, Session, uuid.UUID, SharingControl, Audit) (SharingRestriction, error)
	Audits(context.Context, AuditFilter) (AuditPage, error)
	JobCounts(context.Context) (JobCounts, error)
	Jobs(context.Context, JobFilter) (JobPage, error)
	DeletionJobs(context.Context, JobFilter) (DeletionJobPage, error)
	Trips(context.Context, TripFilter, time.Time) (TripPage, error)
	Trip(context.Context, uuid.UUID, time.Time) (TripDetail, bool, error)
	Overview(context.Context, time.Time) (Overview, error)
	Users(context.Context, UserFilter) (UserPage, error)
	User(context.Context, uuid.UUID, time.Time) (UserDetail, bool, error)
	Credentials(context.Context, string) (account.Account, bool, error)
	OpenSession(context.Context, Session, []byte, string, Audit) (bool, error)
	Authenticate(context.Context, []byte, time.Time, time.Duration) (Session, bool, error)
	Audit(context.Context, Audit) error
	Sessions(context.Context, uuid.UUID, time.Time, time.Duration) ([]Session, error)
	RevokeSession(context.Context, uuid.UUID, uuid.UUID, Audit) error
	Reauthenticate(context.Context, uuid.UUID, uuid.UUID, string, Audit) (bool, error)
}
