// Package config 从环境变量读取运行配置并在启动时校验。
// 业务服务通过构造函数接收 Config，不在别处读取环境变量。
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Prefix 是全部环境变量的公共前缀。
const Prefix = "TRIPFOLIO_"

// MailConfig 是邮件投递配置；Driver 为 disabled、log 或 smtp。
type MailConfig struct {
	Driver       string
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	From         string
	FromName     string
	ImplicitTLS  bool
}

// ObjectStoreConfig 是私有对象存储配置（S3 兼容）。
// 开发用 MinIO：Endpoint=http://localhost:9000、UsePathStyle=true。
// 生产用阿里云 OSS：Endpoint=https://oss-cn-hangzhou.aliyuncs.com、UsePathStyle=false。
type ObjectStoreConfig struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
}

// Configured 表示是否已配置对象存储；未配置时文件相关接口返回依赖不可用。
func (c ObjectStoreConfig) Configured() bool {
	return c.Endpoint != "" && c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// GeoConfig 是高德 Web 服务代理配置。Key 只在服务端使用，不下发给客户端。
type GeoConfig struct {
	AmapKey             string
	PerAccountPerMinute int
	GlobalDailyLimit    int
	CacheTTL            time.Duration
	Timeout             time.Duration
}

// Configured 表示是否已配置高德；未配置时地点接口返回依赖不可用。
func (c GeoConfig) Configured() bool { return c.AmapKey != "" }

// Config 是 API、worker 与 migrate 共用的运行配置。
type Config struct {
	// Env 为 dev、test 或 prod，只影响日志与调试行为，不改变业务规则。
	Env string
	// HTTPAddr 是 API 监听地址，例如 ":8080"。
	HTTPAddr string
	// DatabaseURL 是 PostgreSQL 连接串（pgx 格式）。
	DatabaseURL string
	// DBMaxConns 是连接池上限。
	DBMaxConns int32
	// LogLevel 是 slog 级别。
	LogLevel slog.Level
	// ShutdownTimeout 是收到退出信号后等待在途请求或任务的时长。
	ShutdownTimeout time.Duration
	// CORSOrigins 是允许的浏览器来源：网页域与 Capacitor 来源。
	CORSOrigins []string
	// WebBaseURL 是网页站点根地址（无尾部斜杠），用于拼接分享链接等需要回到网页的完整地址。
	WebBaseURL string
	// WorkerMaxJobs 是 worker 默认队列的最大并发。
	WorkerMaxJobs int
	// Keyring 是签名与派生密钥配置："kid=base64,..."，第一个为当前密钥。
	Keyring string
	// CookieSecure 控制刷新 Cookie 是否带 Secure 与 __Host- 前缀：跟随站点协议自动决定
	// （https 在 prod 下默认开启，http 一律关闭），可用 TRIPFOLIO_COOKIE_SECURE 显式覆盖。
	CookieSecure bool
	// PasswordHashConcurrency 是 Argon2id 的并发上限。
	PasswordHashConcurrency int
	// Mail 是邮件投递配置。
	Mail MailConfig
	// ObjectStore 是私有对象存储配置。
	ObjectStore ObjectStoreConfig
	// Geo 是高德地点服务代理配置。
	Geo GeoConfig
	// AMapJSCode 是高德 JS API 安全密钥，由进程内的 /_AMapService 代理覆盖进查询串。
	// 它与 Web 服务 Key 是两套凭证：这个用在浏览器侧请求的代理上，不下发给前端。
	AMapJSCode string
	// AutoMigrate 控制 serve 启动时是否先执行数据库迁移。
	AutoMigrate bool
	// StartupDBTimeout 是启动时等待数据库可达的时长，超时即失败退出。
	StartupDBTimeout time.Duration
	// StartupDBRetryInterval 是等待数据库时的重试间隔。
	StartupDBRetryInterval time.Duration
	// Warnings 是不阻塞启动的配置提醒，由入口以 WARN 级别打印：
	// 用于「能跑但不理想」的组合，例如没有 TLS 的部署、以及还没切到 prod 的形态。
	Warnings []string
}

// Load 读取环境变量。getenv 通常传 os.Getenv，测试可传 map 查找函数。
// 全部校验错误合并返回，便于一次修完。
func Load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(Prefix + key)); v != "" {
			return v
		}
		return def
	}

	var errs []error
	cfg := Config{
		Env:         get("ENV", "dev"),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
		DatabaseURL: get("DATABASE_URL", ""),
		Keyring:     get("KEYRING", ""),
	}

	switch cfg.Env {
	case "dev", "test", "prod":
	default:
		errs = append(errs, fmt.Errorf("%sENV 必须是 dev、test 或 prod，当前为 %q", Prefix, cfg.Env))
	}

	if cfg.DatabaseURL == "" {
		errs = append(errs, fmt.Errorf("%sDATABASE_URL 不能为空", Prefix))
	}

	if n, err := strconv.ParseInt(get("DB_MAX_CONNS", "10"), 10, 32); err != nil || n < 1 {
		errs = append(errs, fmt.Errorf("%sDB_MAX_CONNS 必须是正整数", Prefix))
	} else {
		cfg.DBMaxConns = int32(n)
	}

	if level, err := parseLogLevel(get("LOG_LEVEL", "info")); err != nil {
		errs = append(errs, err)
	} else {
		cfg.LogLevel = level
	}

	if d, err := time.ParseDuration(get("SHUTDOWN_TIMEOUT", "15s")); err != nil || d <= 0 {
		errs = append(errs, fmt.Errorf("%sSHUTDOWN_TIMEOUT 必须是正的时长，例如 15s", Prefix))
	} else {
		cfg.ShutdownTimeout = d
	}

	cfg.CORSOrigins = splitList(get("CORS_ORIGINS", "http://localhost:5173,https://localhost"))
	for _, origin := range cfg.CORSOrigins {
		if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
			errs = append(errs, fmt.Errorf("%sCORS_ORIGINS 中的 %q 必须以 http:// 或 https:// 开头", Prefix, origin))
		}
	}

	// COOKIE_SECURE 只在显式设置时才覆盖下面的自动判定，非法值仍然是配置错误。
	var cookieSecureSet, cookieSecureValue bool
	if raw := get("COOKIE_SECURE", ""); raw != "" {
		if b, err := strconv.ParseBool(raw); err != nil {
			errs = append(errs, fmt.Errorf("%sCOOKIE_SECURE 必须是 true 或 false", Prefix))
		} else {
			cookieSecureSet, cookieSecureValue = true, b
		}
	}

	cfg.WebBaseURL = strings.TrimRight(get("WEB_BASE_URL", "http://localhost:5173"), "/")
	if !strings.HasPrefix(cfg.WebBaseURL, "http://") && !strings.HasPrefix(cfg.WebBaseURL, "https://") {
		errs = append(errs, fmt.Errorf("%sWEB_BASE_URL 必须以 http:// 或 https:// 开头", Prefix))
	}

	// 刷新 Cookie 的 Secure 与 __Host- 前缀跟随站点协议自动决定：https 用 Secure（prod 默认），
	// http 一律关闭。纯 http 部署不再要求额外的开关——自托管里 http 很常见，把「没有 TLS」当成
	// 配置错误只会把人挡在门外；需要强调的是取舍本身，所以只告警不报错。
	// TRIPFOLIO_COOKIE_SECURE 仍可显式覆盖（例如前面已有 HTTPS 代理但地址暂时写成 http）。
	insecureSite := strings.HasPrefix(cfg.WebBaseURL, "http://")
	switch {
	case cookieSecureSet:
		cfg.CookieSecure = cookieSecureValue
		if cfg.CookieSecure && insecureSite {
			cfg.Warnings = append(cfg.Warnings,
				"站点地址是 http 却显式设置了 TRIPFOLIO_COOKIE_SECURE=true：浏览器不会回传带 Secure 的 Cookie，"+
					"登录会表现为「登录后又变回未登录」；请去掉该变量或把站点改成 https")
		}
	case insecureSite:
		cfg.CookieSecure = false
		if cfg.Env == "prod" {
			cfg.Warnings = append(cfg.Warnings,
				"站点地址是 http：分享链接为 http、刷新 Cookie 不以 Secure 传输，仅适合内网或个人自用；"+
					"对外请在前面加一层 HTTPS 反向代理并把 TRIPFOLIO_WEB_BASE_URL 改成 https")
		}
	default:
		cfg.CookieSecure = cfg.Env == "prod"
	}

	if n, err := strconv.Atoi(get("WORKER_MAX_JOBS", "20")); err != nil || n < 1 {
		errs = append(errs, fmt.Errorf("%sWORKER_MAX_JOBS 必须是正整数", Prefix))
	} else {
		cfg.WorkerMaxJobs = n
	}

	if n, err := strconv.Atoi(get("PASSWORD_HASH_CONCURRENCY", "4")); err != nil || n < 1 {
		errs = append(errs, fmt.Errorf("%sPASSWORD_HASH_CONCURRENCY 必须是正整数", Prefix))
	} else {
		cfg.PasswordHashConcurrency = n
	}

	if cfg.Keyring == "" && cfg.Env == "prod" {
		errs = append(errs, fmt.Errorf("%sKEYRING 在生产环境必须设置", Prefix))
	}

	// 非 prod 会放宽若干校验（邮件可用 log 驱动、高德缺失只告警），
	// 部署时忘了切 prod 会静默跑在宽松模式下，这里明确提醒。
	if cfg.Env != "prod" {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(
			"当前 TRIPFOLIO_ENV=%s：邮件允许 log 驱动、高德 key 缺失只告警、Cookie 不带 Secure；正式部署请设为 prod",
			cfg.Env))
	}

	mailDriverDefault := "log"
	if cfg.Env == "prod" {
		mailDriverDefault = "disabled"
	}
	cfg.Mail = MailConfig{
		Driver:       get("MAIL_DRIVER", mailDriverDefault),
		SMTPHost:     get("MAIL_SMTP_HOST", ""),
		SMTPUsername: get("MAIL_SMTP_USERNAME", ""),
		SMTPPassword: get("MAIL_SMTP_PASSWORD", ""),
		From:         get("MAIL_FROM", ""),
		FromName:     get("MAIL_FROM_NAME", "Tripfolio"),
	}
	switch cfg.Mail.Driver {
	case "disabled":
		// 邮件可选；未启用时验证码接口返回依赖不可用。
	case "log":
		if cfg.Env == "prod" {
			errs = append(errs, fmt.Errorf("%sMAIL_DRIVER 在生产环境不能是 log", Prefix))
		}
	case "smtp":
		if cfg.Mail.SMTPHost == "" || cfg.Mail.From == "" {
			errs = append(errs, fmt.Errorf("%sMAIL_SMTP_HOST 与 %sMAIL_FROM 在 smtp 驱动下必填", Prefix, Prefix))
		}
		if n, err := strconv.Atoi(get("MAIL_SMTP_PORT", "465")); err != nil || n < 1 || n > 65535 {
			errs = append(errs, fmt.Errorf("%sMAIL_SMTP_PORT 必须是有效端口", Prefix))
		} else {
			cfg.Mail.SMTPPort = n
		}
		if b, err := strconv.ParseBool(get("MAIL_SMTP_IMPLICIT_TLS", "true")); err != nil {
			errs = append(errs, fmt.Errorf("%sMAIL_SMTP_IMPLICIT_TLS 必须是 true 或 false", Prefix))
		} else {
			cfg.Mail.ImplicitTLS = b
		}
	default:
		errs = append(errs, fmt.Errorf("%sMAIL_DRIVER 必须是 disabled、log 或 smtp", Prefix))
	}

	cfg.ObjectStore = ObjectStoreConfig{
		Endpoint:        get("OBJECTSTORE_ENDPOINT", ""),
		Region:          get("OBJECTSTORE_REGION", "us-east-1"),
		Bucket:          get("OBJECTSTORE_BUCKET", ""),
		AccessKeyID:     get("OBJECTSTORE_ACCESS_KEY_ID", ""),
		SecretAccessKey: get("OBJECTSTORE_SECRET_ACCESS_KEY", ""),
	}
	// MinIO 必须用路径寻址，OSS 与 AWS 用虚拟主机寻址；默认跟随是否为生产环境。
	if style := get("OBJECTSTORE_USE_PATH_STYLE", ""); style == "" {
		cfg.ObjectStore.UsePathStyle = cfg.Env != "prod"
	} else if b, err := strconv.ParseBool(style); err != nil {
		errs = append(errs, fmt.Errorf("%sOBJECTSTORE_USE_PATH_STYLE 必须是 true 或 false", Prefix))
	} else {
		cfg.ObjectStore.UsePathStyle = b
	}
	// 所有环境都允许不接对象存储；未配齐时不创建客户端，文件授权接口返回依赖不可用。

	cfg.Geo = GeoConfig{AmapKey: get("AMAP_WEB_SERVICE_KEY", "")}
	if n, err := strconv.Atoi(get("GEO_PER_ACCOUNT_PER_MINUTE", "60")); err != nil || n < 1 {
		errs = append(errs, fmt.Errorf("%sGEO_PER_ACCOUNT_PER_MINUTE 必须是正整数", Prefix))
	} else {
		cfg.Geo.PerAccountPerMinute = n
	}
	// 0 表示不设全局日上限；设置后用于保护高德免费配额。
	if n, err := strconv.Atoi(get("GEO_GLOBAL_DAILY_LIMIT", "0")); err != nil || n < 0 {
		errs = append(errs, fmt.Errorf("%sGEO_GLOBAL_DAILY_LIMIT 必须是非负整数", Prefix))
	} else {
		cfg.Geo.GlobalDailyLimit = n
	}
	if d, err := time.ParseDuration(get("GEO_CACHE_TTL", "24h")); err != nil || d <= 0 {
		errs = append(errs, fmt.Errorf("%sGEO_CACHE_TTL 必须是正的时长，例如 24h", Prefix))
	} else {
		cfg.Geo.CacheTTL = d
	}
	if d, err := time.ParseDuration(get("GEO_TIMEOUT", "5s")); err != nil || d <= 0 {
		errs = append(errs, fmt.Errorf("%sGEO_TIMEOUT 必须是正的时长，例如 5s", Prefix))
	} else {
		cfg.Geo.Timeout = d
	}
	if cfg.Env == "prod" && !cfg.Geo.Configured() {
		errs = append(errs, fmt.Errorf("%sAMAP_WEB_SERVICE_KEY 在生产环境必填", Prefix))
	}

	// 高德 JS API 安全密钥：未配置时代理返回 503、页面没有底图，因此不阻塞启动，只由启动日志提示。
	cfg.AMapJSCode = get("AMAP_JSCODE", "")

	if b, err := strconv.ParseBool(get("AUTO_MIGRATE", "true")); err != nil {
		errs = append(errs, fmt.Errorf("%sAUTO_MIGRATE 必须是 true 或 false", Prefix))
	} else {
		cfg.AutoMigrate = b
	}
	if d, err := time.ParseDuration(get("STARTUP_DB_TIMEOUT", "60s")); err != nil || d <= 0 {
		errs = append(errs, fmt.Errorf("%sSTARTUP_DB_TIMEOUT 必须是正的时长，例如 60s", Prefix))
	} else {
		cfg.StartupDBTimeout = d
	}
	if d, err := time.ParseDuration(get("STARTUP_DB_RETRY_INTERVAL", "2s")); err != nil || d <= 0 {
		errs = append(errs, fmt.Errorf("%sSTARTUP_DB_RETRY_INTERVAL 必须是正的时长，例如 2s", Prefix))
	} else {
		cfg.StartupDBRetryInterval = d
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("%sLOG_LEVEL 必须是 debug、info、warn 或 error，当前为 %q", Prefix, s)
}

// splitList 按逗号切分并去掉空白与空项。
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
