package riverjobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"tripfolio/server/internal/modules/travel/trip"
)

// PurgeTripWorker 处理 trip.PurgeJobArgs。阶段化清理（撤销访问 → 删对象 → 删行 → 墓碑）在数据管理切片实现；
// 在此之前任务只记录日志并推迟重试，不把未清理的数据报告为完成。
type PurgeTripWorker struct {
	river.WorkerDefaults[trip.PurgeJobArgs]
	logger *slog.Logger
}

// Work 实现 river.Worker。
func (w *PurgeTripWorker) Work(ctx context.Context, job *river.Job[trip.PurgeJobArgs]) error {
	w.logger.WarnContext(ctx, "旅行永久清理任务尚未实现，推迟执行",
		"job_id", job.ID, "deletion_job_id", job.Args.JobID, "account_id", job.Args.AccountID, "trip_id", job.Args.TripID)
	return river.JobSnooze(time.Hour)
}
