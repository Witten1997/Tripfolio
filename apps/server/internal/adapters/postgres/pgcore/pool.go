// Package pgcore 提供连接池与事务设施。业务包不直接依赖它，而是通过各自 ports.go 声明的接口访问。
package pgcore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool 创建 API 与 worker 使用的连接池，并设置数据库设计 §1 约定的会话超时：
// lock_timeout 5s、statement_timeout 30s、idle_in_transaction_session_timeout 30s。
// 可同步写事务使用 READ COMMITTED（PostgreSQL 默认），只读聚合在事务内自行设置 REPEATABLE READ。
func NewPool(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析数据库连接串: %w", err)
	}
	cfg.MaxConns = maxConns
	// 数据库通常在远端（如 Supabase），新建连接要经历 TCP、TLS 与认证多次往返，落在请求路径上就是整秒的延迟。
	// 常驻至少 2 条连接并让健康检查在后台补足，使低频访问时请求也总能拿到热连接。
	cfg.MinConns = min(2, maxConns)
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnLifetimeJitter = 10 * time.Minute
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	params := cfg.ConnConfig.RuntimeParams
	params["application_name"] = "tripfolio"
	params["lock_timeout"] = "5s"
	params["statement_timeout"] = "30s"
	params["idle_in_transaction_session_timeout"] = "30s"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建连接池: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	return pool, nil
}

// NewMigrationPool 创建迁移专用的小连接池：不设 statement_timeout，避免大表 DDL 被误中断；
// 保留 lock_timeout，使等待锁的迁移快速失败而不是拖住业务。
func NewMigrationPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析数据库连接串: %w", err)
	}
	cfg.MaxConns = 2
	params := cfg.ConnConfig.RuntimeParams
	params["application_name"] = "tripfolio-migrate"
	params["lock_timeout"] = "30s"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建迁移连接池: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	return pool, nil
}
