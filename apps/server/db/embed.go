// Package db 嵌入 Goose 业务迁移文件，供 migrate 入口与就绪检查使用。
// 迁移文件是模式的唯一手工来源；sqlc 也从同一目录读取表结构。
package db

import (
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// Migrations 包含 migrations/ 下的全部 SQL 迁移。
//
//go:embed migrations/*.sql
var Migrations embed.FS

// MigrationsDir 是 Migrations 内的目录名。
const MigrationsDir = "migrations"

// LatestVersion 返回嵌入迁移中的最大版本号（文件名前缀，例如 00001_accounts.sql → 1）。
// API 与 worker 启动时用它核对数据库已迁移到当前程序需要的版本。
func LatestVersion() (int64, error) {
	entries, err := fs.ReadDir(Migrations, MigrationsDir)
	if err != nil {
		return 0, fmt.Errorf("读取嵌入迁移目录: %w", err)
	}
	var latest int64
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return 0, fmt.Errorf("迁移文件名 %q 缺少版本前缀", name)
		}
		v, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("迁移文件名 %q 的版本前缀无效", name)
		}
		if v > latest {
			latest = v
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("没有找到任何迁移文件")
	}
	return latest, nil
}
