// Package queue 封装 River 客户端的构造与任务参数类型。
// 任务名称与参数放在这里，由投递方（业务写事务）与消费方（transport/river）共用；本包不导入 transport。
package queue

import (
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// NewInsertOnlyClient 创建只用于事务内入队的客户端（API 进程使用）。
// 不配置队列与 worker，因此不会消费任务，也不需要 Start。
func NewInsertOnlyClient(pool *pgxpool.Pool, logger *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger: logger,
	})
}

// NewWorkerClient 创建消费任务的客户端（worker 进程使用）。
func NewWorkerClient(pool *pgxpool.Pool, logger *slog.Logger, workers *river.Workers, maxWorkers int) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger: logger,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: maxWorkers},
		},
		Workers: workers,
	})
}
