package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"

	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/adapters/queue"
	"tripfolio/server/internal/config"
	riverjobs "tripfolio/server/internal/transport/river"
)

// RunWorker 启动 River 消费进程并阻塞到 ctx 结束，退出时在 ShutdownTimeout 内等待在途任务完成。
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := pgcore.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	readiness, err := newDBReadiness(pool)
	if err != nil {
		return err
	}
	if err := readiness.Check(ctx); err != nil {
		return fmt.Errorf("启动检查失败: %w", err)
	}

	// worker 与 API 共用同一套装配：校验器需要写事务、对象存储与图片处理，全部来自 BuildServices。
	services, err := BuildServices(pool, cfg, logger, nil)
	if err != nil {
		return err
	}
	workers := river.NewWorkers()
	deps := riverjobs.Deps{Logger: logger}
	// 未配置对象存储时 AssetVerifier 为 nil 指针；保持接口为 nil，让 worker 走推迟分支而不是解引用。
	if services.AssetVerifier != nil {
		deps.AssetVerifier = services.AssetVerifier
	}
	riverjobs.RegisterWorkers(workers, deps)
	client, err := queue.NewWorkerClient(pool, logger, workers, cfg.WorkerMaxJobs)
	if err != nil {
		return fmt.Errorf("创建 worker 客户端: %w", err)
	}
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("启动 worker: %w", err)
	}
	logger.Info("worker 已启动", "env", cfg.Env, "max_jobs", cfg.WorkerMaxJobs)

	<-ctx.Done()
	logger.Info("收到退出信号，等待在途任务完成", "timeout", cfg.ShutdownTimeout)
	stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := client.Stop(stopCtx); err != nil {
		logger.Warn("在超时内未能优雅停止，取消剩余任务", "error", err)
		return client.StopAndCancel(context.Background())
	}
	logger.Info("worker 已停止")
	return nil
}
