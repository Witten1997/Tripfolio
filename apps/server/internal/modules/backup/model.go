package backup

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrBusy = errors.New("BACKUP_BUSY")

type Config struct {
	DatabaseID  string `json:"database_id"`
	Enabled     bool   `json:"enabled"`
	Time        string `json:"time"`
	Retain      int    `json:"retain"`
	URL         string `json:"url"`
	Username    string `json:"username"`
	Secret      string `json:"secret"`
	Destination string `json:"destination"`
}

type Settings struct {
	RestoreReady     bool       `json:"restore_ready"`
	RestoreReadiness string     `json:"restore_readiness"`
	Enabled          bool       `json:"enabled"`
	Time             string     `json:"time"`
	Retain           int        `json:"retain"`
	URL              string     `json:"url"`
	Username         string     `json:"username"`
	PasswordSet      bool       `json:"password_set"`
	Version          int64      `json:"version"`
	NextAt           *time.Time `json:"next_at"`
	Ready            bool       `json:"ready"`
	Readiness        string     `json:"readiness"`
}

type Update struct {
	Enabled  bool   `json:"enabled"`
	Time     string `json:"time"`
	Retain   int    `json:"retain"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	Version  int64  `json:"version"`
	Reason   string `json:"reason"`
}

type Run struct {
	ID              uuid.UUID  `json:"id"`
	Trigger         string     `json:"trigger"`
	State           string     `json:"state"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at"`
	SnapshotAt      *time.Time `json:"snapshot_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	Size            int64      `json:"size_bytes"`
	SHA256          string     `json:"sha256"`
	ErrorCode       string     `json:"error_code"`
	CleanupError    string     `json:"cleanup_error"`
	RemoteDeletedAt *time.Time `json:"remote_deleted_at"`
	Config          Config     `json:"-"`
	Manifest        Manifest   `json:"-"`
}

type Page struct {
	Data     []Run `json:"data"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

type Manifest struct {
	Format        int       `json:"format"`
	ID            uuid.UUID `json:"id"`
	SnapshotAt    time.Time `json:"snapshot_at"`
	ServerVersion int       `json:"server_version"`
	GooseVersion  int64     `json:"goose_version"`
	RiverVersion  int64     `json:"river_version"`
	Schemas       []string  `json:"schemas"`
	ExcludedData  []string  `json:"excluded_data"`
	KeyID         string    `json:"key_id,omitempty"`
	Size          int64     `json:"size_bytes"`
	SHA256        string    `json:"sha256"`
}

type Args struct {
	ID uuid.UUID `json:"id"`
}

func (Args) Kind() string { return "database_backup" }

type TickArgs struct{}

func (TickArgs) Kind() string { return "database_backup_schedule" }

type Store interface {
	Schedule(context.Context) error
	LoadRun(context.Context, uuid.UUID) (Run, error)
	Acquire(context.Context) (context.Context, func(), error)
	Progress(context.Context, uuid.UUID, string, *Manifest) error
	Finish(context.Context, uuid.UUID, string, string) error
	Expired(context.Context, Config) ([]Run, error)
	Deleted(context.Context, uuid.UUID) error
	BeforeDelete(context.Context, uuid.UUID) error
	CleanupError(context.Context, uuid.UUID, string) error
}

func Next(now time.Time, at string) time.Time {
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	t, _ := time.Parse("15:04", at)
	day := now.In(zone)
	next := time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, zone)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC()
}
