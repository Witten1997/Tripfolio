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

func TestLoadStartupControls(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 默认自动迁移并给外部数据库 60 秒等待：单二进制部署最常见的就是数据库还没就绪。
	if !cfg.AutoMigrate {
		t.Error("AutoMigrate 默认应为 true")
	}
	if cfg.StartupDBTimeout != 60*time.Second {
		t.Errorf("StartupDBTimeout = %v, want 60s", cfg.StartupDBTimeout)
	}
	if cfg.StartupDBRetryInterval != 2*time.Second {
		t.Errorf("StartupDBRetryInterval = %v, want 2s", cfg.StartupDBRetryInterval)
	}
	if cfg.AMapJSCode != "" {
		t.Errorf("AMapJSCode = %q, want 空", cfg.AMapJSCode)
	}

	configured, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL":              "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_AMAP_JSCODE":               "jscode-from-env",
		"TRIPFOLIO_AUTO_MIGRATE":              "false",
		"TRIPFOLIO_STARTUP_DB_TIMEOUT":        "15s",
		"TRIPFOLIO_STARTUP_DB_RETRY_INTERVAL": "500ms",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if configured.AMapJSCode != "jscode-from-env" || configured.AutoMigrate {
		t.Errorf("AMapJSCode/AutoMigrate = %q/%v", configured.AMapJSCode, configured.AutoMigrate)
	}
	if configured.StartupDBTimeout != 15*time.Second || configured.StartupDBRetryInterval != 500*time.Millisecond {
		t.Errorf("超时与重试间隔 = %v/%v", configured.StartupDBTimeout, configured.StartupDBRetryInterval)
	}
}

func TestLoadRejectsInvalidStartupControls(t *testing.T) {
	_, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL":              "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_AUTO_MIGRATE":              "maybe",
		"TRIPFOLIO_STARTUP_DB_TIMEOUT":        "0s",
		"TRIPFOLIO_STARTUP_DB_RETRY_INTERVAL": "-1s",
	}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{
		"TRIPFOLIO_AUTO_MIGRATE", "TRIPFOLIO_STARTUP_DB_TIMEOUT", "TRIPFOLIO_STARTUP_DB_RETRY_INTERVAL",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息未包含 %s：%v", want, err)
		}
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

func TestProdStillRequiresAmapKey(t *testing.T) {
	_, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_ENV":            "prod",
		"TRIPFOLIO_DATABASE_URL":   "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_KEYRING":        "k1=" + strings.Repeat("A", 44),
		"TRIPFOLIO_WEB_BASE_URL":   "https://trip.example.com",
		"TRIPFOLIO_MAIL_DRIVER":    "smtp",
		"TRIPFOLIO_MAIL_SMTP_HOST": "smtpdm.aliyun.com",
		"TRIPFOLIO_MAIL_FROM":      "no-reply@example.com",
	}))
	if err == nil {
		t.Fatal("生产环境缺少高德配置应报错")
	}
	if strings.Contains(err.Error(), "OBJECTSTORE_") {
		t.Errorf("对象存储未配置不应阻塞启动: %v", err)
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

func TestWebBaseURLDefaultsInDevAndAcceptsPlainHTTPInProd(t *testing.T) {
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

	// 纯 http 部署是自托管的常见形态，不再要求额外的开关；代价（分享链接是 http、
	// 刷新 Cookie 不带 Secure）通过启动告警说明。
	prodHTTP, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_ENV":                  "prod",
		"TRIPFOLIO_DATABASE_URL":         "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_KEYRING":              "k1=" + strings.Repeat("A", 44),
		"TRIPFOLIO_MAIL_DRIVER":          "disabled",
		"TRIPFOLIO_WEB_BASE_URL":         "http://trip.example.com:61118",
		"TRIPFOLIO_AMAP_WEB_SERVICE_KEY": "amap-key",
	}))
	if err != nil {
		t.Fatalf("prod 配 http 站点地址应可启动：%v", err)
	}
	if prodHTTP.CookieSecure {
		t.Error("http 站点应自动关闭 Secure Cookie")
	}
	if !slices.ContainsFunc(prodHTTP.Warnings, func(w string) bool { return strings.Contains(w, "http") }) {
		t.Errorf("应告警说明 http 部署的代价：%v", prodHTTP.Warnings)
	}
}

// 站点地址决定了刷新 Cookie 是否带 Secure：http 不会被浏览器回传，带 Secure 就会静默失效。
// 纯 http 部署不再需要任何开关：Secure 跟随站点协议自动关闭，只留一条告警。
// 反过来，站点是 http 却硬把 Secure 打开会得到明确提醒——浏览器不会回传这种 Cookie。
func TestCookieSecureFollowsSiteScheme(t *testing.T) {
	base := map[string]string{
		"TRIPFOLIO_ENV":                  "prod",
		"TRIPFOLIO_DATABASE_URL":         "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_KEYRING":              "k1=" + strings.Repeat("A", 44),
		"TRIPFOLIO_MAIL_DRIVER":          "disabled",
		"TRIPFOLIO_AMAP_WEB_SERVICE_KEY": "amap-key",
		"TRIPFOLIO_WEB_BASE_URL":         "http://trip.example.com:61118",
	}

	// 默认（无 COOKIE_SECURE）：http 自动关闭并告警。
	cfg, err := config.Load(envFrom(base))
	if err != nil {
		t.Fatalf("http 站点无需额外开关即可启动：%v", err)
	}
	if cfg.CookieSecure {
		t.Error("http 站点应自动关闭 Secure Cookie")
	}
	if !slices.ContainsFunc(cfg.Warnings, func(w string) bool { return strings.Contains(w, "http") }) {
		t.Errorf("缺少 http 部署告警: %v", cfg.Warnings)
	}

	// http 站点上强行打开 Secure：放行但必须提醒登录会失效。
	base["TRIPFOLIO_COOKIE_SECURE"] = "true"
	forced, err := config.Load(envFrom(base))
	if err != nil {
		t.Fatalf("显式设置 COOKIE_SECURE 仍应可启动：%v", err)
	}
	if !forced.CookieSecure {
		t.Error("显式 COOKIE_SECURE=true 应生效")
	}
	if !slices.ContainsFunc(forced.Warnings, func(w string) bool { return strings.Contains(w, "Secure") }) {
		t.Errorf("http + Secure 应告警登录会失效: %v", forced.Warnings)
	}

	// 站点是 https：prod 默认开启，且没有 http 相关告警。
	delete(base, "TRIPFOLIO_COOKIE_SECURE")
	base["TRIPFOLIO_WEB_BASE_URL"] = "https://trip.example.com"
	secure, err := config.Load(envFrom(base))
	if err != nil {
		t.Fatalf("https 站点应可启动：%v", err)
	}
	if !secure.CookieSecure {
		t.Error("https + prod 应默认开启 Secure Cookie")
	}
	if len(secure.Warnings) != 0 {
		t.Errorf("规范的 https 配置不该有告警: %v", secure.Warnings)
	}
}

func TestNonProdEnvironmentIsWarned(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.ContainsFunc(cfg.Warnings, func(w string) bool { return strings.Contains(w, "TRIPFOLIO_ENV=dev") }) {
		t.Errorf("dev 环境应提示切到 prod: %v", cfg.Warnings)
	}
	// 生产 + https 时不该出现「忘了切 prod」这类提醒。
	prod, err := config.Load(envFrom(map[string]string{
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
	if !prod.CookieSecure {
		t.Error("prod + https 时 CookieSecure 应为 true")
	}
	if len(prod.Warnings) != 0 {
		t.Errorf("规范的 prod 配置不该有告警: %v", prod.Warnings)
	}
}
