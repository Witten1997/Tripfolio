package config_test

import (
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"tripfolio/server/internal/config"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want dev", cfg.Env)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.DBMaxConns != 10 {
		t.Errorf("DBMaxConns = %d, want 10", cfg.DBMaxConns)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 15s", cfg.ShutdownTimeout)
	}
	if want := []string{"http://localhost:5173", "https://localhost"}; !slices.Equal(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, want)
	}
	if cfg.WorkerMaxJobs != 20 {
		t.Errorf("WorkerMaxJobs = %d, want 20", cfg.WorkerMaxJobs)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := config.Load(envFrom(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "TRIPFOLIO_DATABASE_URL") {
		t.Fatalf("want error mentioning TRIPFOLIO_DATABASE_URL, got %v", err)
	}
}

func TestLoadRejectsInvalidValuesTogether(t *testing.T) {
	_, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL":     "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_ENV":              "staging",
		"TRIPFOLIO_LOG_LEVEL":        "loud",
		"TRIPFOLIO_SHUTDOWN_TIMEOUT": "soon",
		"TRIPFOLIO_DB_MAX_CONNS":     "0",
		"TRIPFOLIO_CORS_ORIGINS":     "localhost:5173",
	}))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	for _, key := range []string{"ENV", "LOG_LEVEL", "SHUTDOWN_TIMEOUT", "DB_MAX_CONNS", "CORS_ORIGINS"} {
		if !strings.Contains(err.Error(), "TRIPFOLIO_"+key) {
			t.Errorf("error should mention %s, got: %v", key, err)
		}
	}
}

func TestLoadSplitsAndTrimsCORSOrigins(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_CORS_ORIGINS": " https://app.example.com , , https://localhost ",
		"TRIPFOLIO_LOG_LEVEL":    "WARN",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"https://app.example.com", "https://localhost"}; !slices.Equal(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, want)
	}
	if cfg.LogLevel != slog.LevelWarn {
		t.Errorf("LogLevel = %v, want warn", cfg.LogLevel)
	}
}

func TestObjectStoreAndGeoDefaultsAreUnconfiguredInDev(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 开发环境允许不配文件与地点服务，相关接口返回依赖不可用而不是启动失败。
	if cfg.ObjectStore.Configured() {
		t.Error("默认不应认为对象存储已配置")
	}
	if cfg.Geo.Configured() {
		t.Error("默认不应认为高德已配置")
	}
	// MinIO 需要路径寻址，非生产默认开启。
	if !cfg.ObjectStore.UsePathStyle {
		t.Error("非生产环境应默认使用路径寻址（MinIO 必需）")
	}
	if cfg.Geo.PerAccountPerMinute != 60 {
		t.Errorf("GeoPerAccountPerMinute = %d, want 60", cfg.Geo.PerAccountPerMinute)
	}
	if cfg.Geo.GlobalDailyLimit != 0 {
		t.Errorf("GeoGlobalDailyLimit = %d, want 0（不限制）", cfg.Geo.GlobalDailyLimit)
	}
	if cfg.Geo.CacheTTL != 24*time.Hour {
		t.Errorf("GeoCacheTTL = %v, want 24h", cfg.Geo.CacheTTL)
	}
}

func TestObjectStoreConfiguredRequiresAllFields(t *testing.T) {
	full := map[string]string{
		"TRIPFOLIO_DATABASE_URL":                  "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_OBJECTSTORE_ENDPOINT":          "http://localhost:9000",
		"TRIPFOLIO_OBJECTSTORE_BUCKET":            "tripfolio",
		"TRIPFOLIO_OBJECTSTORE_ACCESS_KEY_ID":     "ak",
		"TRIPFOLIO_OBJECTSTORE_SECRET_ACCESS_KEY": "sk",
	}
	cfg, err := config.Load(envFrom(full))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ObjectStore.Configured() {
		t.Fatal("配齐四项后应认为已配置")
	}

	// 缺任意一项都不算配置完成，避免半配状态下签出无效授权。
	for _, missing := range []string{
		"TRIPFOLIO_OBJECTSTORE_ENDPOINT",
		"TRIPFOLIO_OBJECTSTORE_BUCKET",
		"TRIPFOLIO_OBJECTSTORE_ACCESS_KEY_ID",
		"TRIPFOLIO_OBJECTSTORE_SECRET_ACCESS_KEY",
	} {
		partial := map[string]string{}
		for k, v := range full {
			if k != missing {
				partial[k] = v
			}
		}
		cfg, err := config.Load(envFrom(partial))
		if err != nil {
			t.Fatalf("缺 %s 时 dev 环境不应报错: %v", missing, err)
		}
		if cfg.ObjectStore.Configured() {
			t.Errorf("缺 %s 时不应认为已配置", missing)
		}
	}
}

func TestProdRequiresObjectStoreAndAmapKey(t *testing.T) {
	_, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_ENV":            "prod",
		"TRIPFOLIO_DATABASE_URL":   "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_KEYRING":        "k1=" + strings.Repeat("A", 44),
		"TRIPFOLIO_MAIL_DRIVER":    "smtp",
		"TRIPFOLIO_MAIL_SMTP_HOST": "smtpdm.aliyun.com",
		"TRIPFOLIO_MAIL_FROM":      "no-reply@example.com",
	}))
	if err == nil {
		t.Fatal("生产环境缺少对象存储与高德配置应报错")
	}
	if !strings.Contains(err.Error(), "OBJECTSTORE_ENDPOINT") {
		t.Errorf("错误应提到对象存储配置: %v", err)
	}
	if !strings.Contains(err.Error(), "AMAP_WEB_SERVICE_KEY") {
		t.Errorf("错误应提到高德 key: %v", err)
	}
}

func TestProdDefaultsToVirtualHostAddressing(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_ENV":                           "prod",
		"TRIPFOLIO_DATABASE_URL":                  "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_KEYRING":                       "k1=" + strings.Repeat("A", 44),
		"TRIPFOLIO_MAIL_DRIVER":                   "smtp",
		"TRIPFOLIO_MAIL_SMTP_HOST":                "smtpdm.aliyun.com",
		"TRIPFOLIO_MAIL_FROM":                     "no-reply@example.com",
		"TRIPFOLIO_OBJECTSTORE_ENDPOINT":          "https://oss-cn-hangzhou.aliyuncs.com",
		"TRIPFOLIO_OBJECTSTORE_BUCKET":            "tripfolio",
		"TRIPFOLIO_OBJECTSTORE_ACCESS_KEY_ID":     "ak",
		"TRIPFOLIO_OBJECTSTORE_SECRET_ACCESS_KEY": "sk",
		"TRIPFOLIO_AMAP_WEB_SERVICE_KEY":          "amap-key",
		"TRIPFOLIO_WEB_BASE_URL":                  "https://trip.example.com",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// OSS 用虚拟主机寻址，路径寻址会拼出���误的 URL。
	if cfg.ObjectStore.UsePathStyle {
		t.Error("生产环境应默认使用虚拟主机寻址")
	}
	if !cfg.Geo.Configured() {
		t.Error("配置了 key 后应认为高德已配置")
	}
}

func TestWebBaseURLDefaultsInDevAndRequiresHTTPSInProd(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebBaseURL != "http://localhost:5173" {
		t.Errorf("WebBaseURL = %q, want 开发缺省 http://localhost:5173", cfg.WebBaseURL)
	}

	cfg, err = config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_WEB_BASE_URL": "https://trip.example.com/",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebBaseURL != "https://trip.example.com" {
		t.Errorf("WebBaseURL = %q, want 去掉尾部斜杠", cfg.WebBaseURL)
	}

	_, err = config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_WEB_BASE_URL": "trip.example.com",
	}))
	if err == nil || !strings.Contains(err.Error(), "WEB_BASE_URL") {
		t.Errorf("缺少协议应报错: %v", err)
	}

	_, err = config.Load(envFrom(map[string]string{
		"TRIPFOLIO_ENV":            "prod",
		"TRIPFOLIO_DATABASE_URL":   "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_KEYRING":        "k1=" + strings.Repeat("A", 44),
		"TRIPFOLIO_MAIL_DRIVER":    "smtp",
		"TRIPFOLIO_MAIL_SMTP_HOST": "smtpdm.aliyun.com",
		"TRIPFOLIO_MAIL_FROM":      "no-reply@example.com",
		"TRIPFOLIO_WEB_BASE_URL":   "http://trip.example.com",
	}))
	if err == nil || !strings.Contains(err.Error(), "WEB_BASE_URL") {
		t.Errorf("生产环境非 https 分享地址应报错: %v", err)
	}
}
