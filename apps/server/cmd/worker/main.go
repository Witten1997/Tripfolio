// Command worker 启动 River 消费进程，与 API 共用同一份配置与业务代码。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tripfolio/server/internal/bootstrap"
	"tripfolio/server/internal/config"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker: 配置错误:", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bootstrap.RunWorker(ctx, cfg, logger); err != nil {
		logger.Error("worker 退出", "error", err)
		os.Exit(1)
	}
}
