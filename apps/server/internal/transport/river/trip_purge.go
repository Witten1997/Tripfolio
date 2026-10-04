package riverjobs

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"log/slog"
	"time"
	"tripfolio/server/internal/modules/deletion"
	"tripfolio/server/internal/modules/travel/trip"
)

type TripPurger interface {
	Purge(context.Context, trip.PurgeJobArgs) error
}
type PurgeTripWorker struct {
	river.WorkerDefaults[trip.PurgeJobArgs]
	logger  *slog.Logger
	service TripPurger
}

func (w *PurgeTripWorker) Work(ctx context.Context, job *river.Job[trip.PurgeJobArgs]) error {
	if w.service == nil {
		return river.JobSnooze(snoozeUnconfigured)
	}
	err := w.service.Purge(ctx, job.Args)
	var busy *trip.PurgeBusy
	if errors.As(err, &busy) {
		return river.JobSnooze(5 * time.Second)
	}
	var waiting *trip.PurgeWaiting
	if errors.As(err, &waiting) {
		return river.JobSnooze(max(time.Until(waiting.Until), time.Second))
	}
	if err != nil {
		w.logger.ErrorContext(ctx, "旅行清理未完成", "deletion_job_id", job.Args.JobID, "error", err)
		if job.Args.TripID == uuid.Nil {
			return err
		}
		return river.JobCancel(err)
	}
	return nil
}

type DeletionReconcileWorker struct {
	river.WorkerDefaults[deletion.ReconcileArgs]
	service *deletion.Service
}

func (w *DeletionReconcileWorker) Work(ctx context.Context, _ *river.Job[deletion.ReconcileArgs]) error {
	if w.service == nil {
		return river.JobSnooze(snoozeUnconfigured)
	}
	return w.service.Reconcile(ctx)
}
