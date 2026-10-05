package riverjobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"
	syncmodule "tripfolio/server/internal/modules/sync"
)

type SyncSnapshotWorker struct {
	river.WorkerDefaults[syncmodule.SnapshotArgs]
	service *syncmodule.Service
}

func (*SyncSnapshotWorker) Timeout(*river.Job[syncmodule.SnapshotArgs]) time.Duration {
	return 5 * time.Minute
}
func (w *SyncSnapshotWorker) Work(ctx context.Context, job *river.Job[syncmodule.SnapshotArgs]) error {
	if w.service == nil {
		return river.JobSnooze(snoozeUnconfigured)
	}
	return w.service.BuildSnapshot(ctx, job.Args.ID, job.Attempt >= job.MaxAttempts)
}
