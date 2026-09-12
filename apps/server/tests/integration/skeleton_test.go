// Package integration 在真实 PostgreSQL 上验证迁移、生成的查询与 River 事务入队。
// 需要环境变量 TRIPFOLIO_TEST_DATABASE_URL（见 apps/server/.env.example）；未设置时跳过。
// 测试会重建目标库的 public schema，只能指向测试库。
package integration

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/adapters/queue"
	"tripfolio/server/internal/bootstrap"
	"tripfolio/server/internal/config"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TRIPFOLIO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TRIPFOLIO_TEST_DATABASE_URL 未设置，跳过集成测试")
	}
	return url
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func resetSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatalf("重建 schema: %v", err)
	}
}

func tableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`,
		table,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("查询表 %s: %v", table, err)
	}
	return exists
}

func TestMigrateUpIsIdempotent(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()
	cfg := config.Config{DatabaseURL: url}

	pool, err := pgcore.NewMigrationPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	resetSchema(t, ctx, pool)

	for i := 0; i < 2; i++ {
		if err := bootstrap.RunMigrate(ctx, cfg, quietLogger(), []string{"up"}); err != nil {
			t.Fatalf("第 %d 次 migrate up: %v", i+1, err)
		}
	}
	for _, table := range []string{"accounts", "account_sync_state", "goose_db_version", "river_job"} {
		if !tableExists(t, ctx, pool, table) {
			t.Errorf("表 %s 应存在", table)
		}
	}
	if err := bootstrap.RunMigrate(ctx, cfg, quietLogger(), []string{"status"}); err != nil {
		t.Fatalf("migrate status: %v", err)
	}
	if err := bootstrap.RunMigrate(ctx, cfg, quietLogger(), []string{"bogus"}); err == nil {
		t.Fatal("未知命令应返回错误")
	}
}

func TestAccountQueriesAndTransactionalEnqueue(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()
	if err := bootstrap.RunMigrate(ctx, config.Config{DatabaseURL: url}, quietLogger(), []string{"up"}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	pool, err := pgcore.NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := dbgen.New(pool)

	id := uuid.New()
	emailKey := "it-" + id.String() + "@example.com"
	created, err := q.CreateAccount(ctx, dbgen.CreateAccountParams{
		ID:              id,
		Email:           "IT-" + id.String() + "@Example.com",
		EmailKey:        emailKey,
		EmailVerifiedAt: time.Now(),
		PasswordHash:    "$argon2id$v=19$m=65536,t=3,p=1$c2FsdA$aGFzaA",
		Nickname:        "集成测试",
		DefaultTimezone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if created.Version != 1 || created.Status != "active" {
		t.Fatalf("默认值不符: version=%d status=%s", created.Version, created.Status)
	}
	if err := q.CreateAccountSyncState(ctx, id); err != nil {
		t.Fatalf("CreateAccountSyncState: %v", err)
	}
	got, err := q.GetAccountByEmailKey(ctx, emailKey)
	if err != nil || got.ID != id {
		t.Fatalf("GetAccountByEmailKey: %v, id=%v", err, got.ID)
	}
	state, err := q.GetAccountSyncState(ctx, id)
	if err != nil || state.LastSeq != 0 {
		t.Fatalf("GetAccountSyncState: %v, last_seq=%d", err, state.LastSeq)
	}

	client, err := queue.NewInsertOnlyClient(pool, quietLogger())
	if err != nil {
		t.Fatalf("NewInsertOnlyClient: %v", err)
	}
	countPing := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = 'ping'`).Scan(&n); err != nil {
			t.Fatalf("count river_job: %v", err)
		}
		return n
	}
	if _, err := pool.Exec(ctx, `DELETE FROM river_job WHERE kind = 'ping'`); err != nil {
		t.Fatal(err)
	}

	// 事务回滚：任务不能出现在队列里
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.InsertTx(ctx, tx, queue.PingArgs{Message: "rolled back"}, nil); err != nil {
		t.Fatalf("InsertTx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countPing(); n != 0 {
		t.Fatalf("回滚后 river_job 中仍有 %d 条 ping 任务", n)
	}

	// 事务提交：任务与业务数据一起可见
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.InsertTx(ctx, tx, queue.PingArgs{Message: "committed"}, nil); err != nil {
		t.Fatalf("InsertTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countPing(); n != 1 {
		t.Fatalf("提交后 river_job 中应有 1 条 ping 任务，实际 %d", n)
	}
}
