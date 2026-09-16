package riverjobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"tripfolio/server/internal/modules/travel/routeplan"
)

type RouteRecalculator interface {
	Recalculate(context.Context, routeplan.RecalculateJobArgs) error
}

type RouteRecalculateWorker struct {
	river.WorkerDefaults[routeplan.RecalculateJobArgs]
	service RouteRecalculator
	logger  *slog.Logger
}

func (w *RouteRecalculateWorker) Work(ctx context.Context, job *river.Job[routeplan.RecalculateJobArgs]) error {
	if w.service == nil {
		w.logger.WarnContext(ctx, "路线重算服务未装配，推迟任务", "job_id", job.ID, "trip_id", job.Args.TripID)
		return river.JobSnooze(snoozeUnconfigured)
	}
	if err := w.service.Recalculate(ctx, job.Args); err != nil {
		w.logger.WarnContext(ctx, "旅行路线重算失败", "job_id", job.ID, "trip_id", job.Args.TripID,
			"revision", job.Args.Revision, "attempt", job.Attempt, "error", err)
		return err
	}
	return nil
}
