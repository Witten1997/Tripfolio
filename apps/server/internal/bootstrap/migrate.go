package bootstrap

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"tripfolio/server/db"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/config"
)

// RunMigrate 执行迁移命令：up（默认，先 River 后业务）、status、down（回退最近一次业务迁移）。
// 业务迁移通过 PostgreSQL 会话锁串行执行，防止两个发布进程同时迁移。
func RunMigrate(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	command := "up"
	if len(args) > 0 {
		command = args[0]
	}

	pool, err := pgcore.NewMigrationPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch command {
	case "up":
		if err := migrateRiver(ctx, pool, logger); err != nil {
			return err
		}
		return migrateBusinessUp(ctx, pool, logger)
	case "status":
		return printBusinessStatus(ctx, pool, logger)
	case "down":
		return migrateBusinessDown(ctx, pool, logger)
	default:
		return fmt.Errorf("未知的 migrate 命令 %q，可用：up、status、down", command)
	}
}

func newGooseProvider(pool *pgxpool.Pool) (*goose.Provider, error) {
	fsys, err := fs.Sub(db.Migrations, db.MigrationsDir)
	if err != nil {
		return nil, fmt.Errorf("读取嵌入迁移: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("创建会话锁: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), fsys,
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 goose provider: %w", err)
	}
	return provider, nil
}

func migrateRiver(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: logger})
	if err != nil {
		return fmt.Errorf("创建 River 迁移器: %w", err)
	}
	result, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("River 迁移失败: %w", err)
	}
	logger.Info("River 迁移完成", "applied", len(result.Versions))
	return nil
}

func migrateBusinessUp(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	provider, err := newGooseProvider(pool)
	if err != nil {
		return err
	}
	defer provider.Close()
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("业务迁移失败: %w", err)
	}
	for _, r := range results {
		logger.Info("已应用业务迁移", "version", r.Source.Version, "path", r.Source.Path, "duration", r.Duration)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	logger.Info("业务迁移完成", "applied", len(results), "db_version", version)
	return nil
}

func migrateBusinessDown(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	provider, err := newGooseProvider(pool)
	if err != nil {
		return err
	}
	defer provider.Close()
	result, err := provider.Down(ctx)
	if err != nil {
		return fmt.Errorf("回退业务迁移失败: %w", err)
	}
	logger.Info("已回退业务迁移", "version", result.Source.Version, "path", result.Source.Path)
	return nil
}

func printBusinessStatus(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	provider, err := newGooseProvider(pool)
	if err != nil {
		return err
	}
	defer provider.Close()
	statuses, err := provider.Status(ctx)
	if err != nil {
		return fmt.Errorf("读取迁移状态失败: %w", err)
	}
	for _, s := range statuses {
		logger.Info("迁移状态", "version", s.Source.Version, "path", s.Source.Path, "state", s.State, "applied_at", s.AppliedAt)
	}
	return nil
}
