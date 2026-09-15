package bootstrap

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/config"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/account"
)

func TestBuildProdServicesWithoutMailAndObjectStore(t *testing.T) {
	env := map[string]string{
		"TRIPFOLIO_ENV":                  "prod",
		"TRIPFOLIO_DATABASE_URL":         "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_KEYRING":              "k1=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		"TRIPFOLIO_WEB_BASE_URL":         "https://trip.example.com",
		"TRIPFOLIO_AMAP_WEB_SERVICE_KEY": "amap-key",
	}
	cfg, err := config.Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	// 装配不访问数据库；验证码请求也应在写入挑战之前返回不可用。
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	services, err := BuildServices(pool, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("生产环境未配置邮件和对象存储时应能完成装配: %v", err)
	}
	if services.ObjectStore != nil || services.AssetVerifier != nil || services.Assets == nil {
		t.Fatal("未配置对象存储时应保留资产服务并禁用存储和校验器")
	}
	for _, purpose := range []account.Purpose{account.PurposeRegister, account.PurposeResetPassword} {
		_, err := services.Identity.RequestEmailChallenge(context.Background(), purpose, "user@example.com", "127.0.0.1")
		if e, ok := apperr.As(err); !ok || e.Code != "DEPENDENCY_UNAVAILABLE" || e.Status != 503 {
			t.Fatalf("邮件禁用时应返回 503 DEPENDENCY_UNAVAILABLE: %v", err)
		}
	}
}
