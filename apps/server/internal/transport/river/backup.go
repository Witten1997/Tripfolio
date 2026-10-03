package riverjobs

import (
	"context"
	"errors"
	"github.com/riverqueue/river"
	"time"
	"tripfolio/server/internal/modules/backup"
)

type BackupWorker struct {
	river.WorkerDefaults[backup.Args]
	service *backup.Service
}

func (w *BackupWorker) Timeout(*river.Job[backup.Args]) time.Duration { return -1 }
func (w *BackupWorker) Work(ctx context.Context, job *river.Job[backup.Args]) error {
	if w.service == nil {
		return river.JobSnooze(snoozeUnconfigured)
	}
	err := w.service.Execute(ctx, job.Args.ID, job.Attempt >= job.MaxAttempts)
	if errors.Is(err, backup.ErrBusy) {
		return river.JobSnooze(30 * time.Second)
	}
	return err
}

type BackupScheduleWorker struct {
	river.WorkerDefaults[backup.TickArgs]
	service *backup.Service
}

func (w *BackupScheduleWorker) Work(ctx context.Context, _ *river.Job[backup.TickArgs]) error {
	if w.service == nil {
		return nil
	}
	return w.service.Schedule(ctx)
}
