// Command api 启动 HTTP 服务：读取配置、装配依赖、监听信号并优雅退出。
// 业务规则不放在这里，见 internal/bootstrap 与 internal/modules。
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
	fromDotEnv, err := config.LoadDotEnv(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "api: 读取 .env 失败:", err)
		os.Exit(2)
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "api: 配置错误:", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if fromDotEnv {
		logger.Info("已读取工作目录 .env 补充配置（进程环境变量优先）")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bootstrap.RunAPI(ctx, cfg, logger); err != nil {
		logger.Error("api 退出", "error", err)
		os.Exit(1)
	}
}
