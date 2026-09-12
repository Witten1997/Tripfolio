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
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "api: 配置错误:", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bootstrap.RunAPI(ctx, cfg, logger); err != nil {
		logger.Error("api 退出", "error", err)
		os.Exit(1)
	}
}
