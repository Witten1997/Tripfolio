package riverjobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"tripfolio/server/internal/adapters/queue"
)

// PingWorker 处理 queue.PingArgs：只记录一条日志，用于验证事务入队与消费链路。
type PingWorker struct {
	river.WorkerDefaults[queue.PingArgs]
	logger *slog.Logger
}

// Work 实现 river.Worker。
func (w *PingWorker) Work(ctx context.Context, job *river.Job[queue.PingArgs]) error {
	w.logger.InfoContext(ctx, "ping 任务已执行", "job_id", job.ID, "message", job.Args.Message, "attempt", job.Attempt)
	return nil
}
