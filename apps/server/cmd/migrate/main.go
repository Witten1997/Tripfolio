// Command migrate 在发布步骤执行数据库迁移：先 River 自有表，再业务表。
//
//	migrate up      应用全部未执行的迁移（默认）
//	migrate status  列出迁移状态
//	migrate down    回退最近一次业务迁移（仅开发环境使用）
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
		fmt.Fprintln(os.Stderr, "migrate: 读取 .env 失败:", err)
		os.Exit(2)
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate: 配置错误:", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if fromDotEnv {
		logger.Info("已读取工作目录 .env 补充配置（进程环境变量优先）")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bootstrap.RunMigrate(ctx, cfg, logger, os.Args[1:]); err != nil {
		logger.Error("migrate 失败", "error", err)
		os.Exit(1)
	}
}
