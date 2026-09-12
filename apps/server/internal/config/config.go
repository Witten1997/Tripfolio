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

// MailConfig 是邮件投递配置；Driver 为 log 或 smtp。
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
	// WorkerMaxJobs 是 worker 默认队列的最大并发。
	WorkerMaxJobs int
	// Keyring 是签名与派生密钥配置："kid=base64,..."，第一个为当前密钥。
	Keyring string
	// CookieSecure 控制刷新 Cookie 是否带 Secure 与 __Host- 前缀；生产必须为 true。
	CookieSecure bool
	// PasswordHashConcurrency 是 Argon2id 的并发上限。
	PasswordHashConcurrency int
	// Mail 是邮件投递配置。
	Mail MailConfig
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

	secure := get("COOKIE_SECURE", "")
	if secure == "" {
		cfg.CookieSecure = cfg.Env == "prod"
	} else if b, err := strconv.ParseBool(secure); err != nil {
		errs = append(errs, fmt.Errorf("%sCOOKIE_SECURE 必须是 true 或 false", Prefix))
	} else {
		cfg.CookieSecure = b
	}

	cfg.Mail = MailConfig{
		Driver:       get("MAIL_DRIVER", "log"),
		SMTPHost:     get("MAIL_SMTP_HOST", ""),
		SMTPUsername: get("MAIL_SMTP_USERNAME", ""),
		SMTPPassword: get("MAIL_SMTP_PASSWORD", ""),
		From:         get("MAIL_FROM", ""),
		FromName:     get("MAIL_FROM_NAME", "Tripfolio"),
	}
	switch cfg.Mail.Driver {
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
		errs = append(errs, fmt.Errorf("%sMAIL_DRIVER 必须是 log 或 smtp", Prefix))
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
