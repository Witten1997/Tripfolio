package bootstrap

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/db"
)

// dbReadiness 检查数据库可达且已迁移到当前程序需要的版本；API 与 worker 启动时和就绪探针都用它。
type dbReadiness struct {
	pool        *pgxpool.Pool
	wantVersion int64
}

func newDBReadiness(pool *pgxpool.Pool) (*dbReadiness, error) {
	want, err := db.LatestVersion()
	if err != nil {
		return nil, err
	}
	return &dbReadiness{pool: pool, wantVersion: want}, nil
}

// Check 实现 httpapi.Readiness。迁移版本表不存在时同样视为未就绪。
func (d *dbReadiness) Check(ctx context.Context) error {
	if err := d.pool.Ping(ctx); err != nil {
		return fmt.Errorf("数据库不可达: %w", err)
	}
	var got int64
	err := d.pool.QueryRow(ctx,
		`SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied`,
	).Scan(&got)
	if err != nil {
		return fmt.Errorf("读取迁移版本: %w", err)
	}
	if got < d.wantVersion {
		return fmt.Errorf("数据库迁移版本 %d 低于程序需要的 %d，请先执行 migrate up", got, d.wantVersion)
	}
	return nil
}
