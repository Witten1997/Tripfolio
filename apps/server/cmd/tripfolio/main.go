// Command tripfolio 是后端唯一入口：前端产物已嵌入二进制，serve 会自动完成数据库迁移，
// 并在同一个进程里运行 HTTP 服务与后台任务。
//
//	tripfolio serve                    等待数据库 → 自动迁移 → 启动 HTTP 与后台任务（默认命令）
//	tripfolio migrate up|status|down   只跑迁移，供发布流程或排障使用
//	tripfolio healthcheck              容器健康检查：请求本机 /health/ready
//	tripfolio version                  打印版本
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tripfolio/server/internal/bootstrap"
	"tripfolio/server/internal/config"
)

// version 可在构建时注入：-ldflags "-X main.version=2026.09.15"。
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	command, rest := "serve", []string(nil)
	if len(args) > 0 {
		command, rest = args[0], args[1:]
	}

	switch command {
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	case "version", "-v", "--version":
		fmt.Printf("tripfolio %s\n", version)
		return 0
	case "healthcheck":
		// 健康检查不读配置：只要进程在监听就能判断，避免配置问题连带探针失败。
		return healthcheck(os.Stderr)
	case "serve", "migrate":
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n", command)
		usage(os.Stderr)
		return 2
	}

	fromDotEnv, err := config.LoadDotEnv(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取工作目录 .env 失败：", err)
		return 2
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		failure := bootstrap.NewConfigError(err)
		bootstrap.ReportStartupFailure(os.Stderr, failure)
		return bootstrap.StartupExitCode(failure)
	}
	logger := newLogger(cfg)
	if fromDotEnv {
		logger.Info("已读取工作目录 .env 补充配置（进程环境变量优先）")
	}
	// 配置提醒不阻塞启动，但要在日志里说清楚，避免「能跑但不理想」的组合被忽略。
	for _, warning := range cfg.Warnings {
		logger.Warn("配置提醒", "detail", warning)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch command {
	case "migrate":
		if err := bootstrap.RunMigrate(ctx, cfg, logger, rest); err != nil {
			logger.Error("migrate 失败", "error", err)
			fmt.Fprintf(os.Stderr, "migrate 失败：%v\n提示：先在数据库上确认账号有建表权限，再用 tripfolio migrate status 查看已应用版本。\n", err)
			return 1
		}
		return 0
	default:
		err := bootstrap.RunServe(ctx, cfg, logger)
		if err == nil {
			return 0
		}
		if errors.Is(err, context.Canceled) {
			logger.Info("启动过程中收到退出信号，已停止")
			return 0
		}
		logger.Error("启动失败", "error", err, "exit_code", bootstrap.StartupExitCode(err))
		bootstrap.ReportStartupFailure(os.Stderr, err)
		return bootstrap.StartupExitCode(err)
	}
}

// newLogger 在生产用 JSON（便于日志系统采集），其余环境用文本（本地与容器排障都更好读）。
func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.Env == "prod" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

// healthcheck 请求本机就绪探针，供容器 HEALTHCHECK 使用。
func healthcheck(stderr io.Writer) int {
	addr := os.Getenv(config.Prefix + "HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(stderr, "健康检查失败：TRIPFOLIO_HTTP_ADDR=%q 不是 主机:端口 形式\n", addr)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	target := fmt.Sprintf("http://%s/health/ready", net.JoinHostPort(host, port))
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(target)
	if err != nil {
		fmt.Fprintf(stderr, "健康检查失败：%v（地址 %s）\n", err, target)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "健康检查失败：%s 返回 %d（数据库可能不可达或结构未迁移）\n", target, resp.StatusCode)
		return 1
	}
	fmt.Println("ready")
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `tripfolio 单二进制：HTTP 接口、内嵌前端与后台任务同进程运行。

用法：
  tripfolio serve                    等待数据库 → 自动迁移 → 启动 HTTP 与后台任务（默认）
  tripfolio migrate up               只执行迁移（先 River 自有表，再业务表）
  tripfolio migrate status           查看各迁移的应用状态
  tripfolio migrate down             回退最近一次业务迁移（仅开发环境）
  tripfolio healthcheck              请求本机 /health/ready，供容器健康检查使用
  tripfolio version                  打印版本

常用环境变量（完整清单见 apps/server/.env.example）：
  TRIPFOLIO_DATABASE_URL         外部 PostgreSQL 连接串
  TRIPFOLIO_HTTP_ADDR            监听地址，默认 :8080
  TRIPFOLIO_AUTO_MIGRATE         是否在 serve 时自动迁移，默认 true
  TRIPFOLIO_STARTUP_DB_TIMEOUT   等待数据库可达的上限，默认 60s
  TRIPFOLIO_AMAP_JSCODE          高德 JS API 安全密钥，供 /_AMapService 代理使用

启动失败时会打印阶段、原因与排查建议，退出码：2 配置、3 数据库、4 迁移、5 装配与任务、6 HTTP 监听。
`)
}
