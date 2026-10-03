package config

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type BackupConfig struct {
	Password, Key, DatabaseURL, Directory string
	MaxBytes                              int64
	Timeout                               time.Duration
}

func loadBackup(get func(string, string) string, password string) (BackupConfig, error) {
	cfg := BackupConfig{Password: password, Key: get("BACKUP_KEY", ""), DatabaseURL: get("BACKUP_DATABASE_URL", ""), Directory: get("BACKUP_DIR", "")}
	if password != "" && (utf8.RuneCountInString(password) < 8 || utf8.RuneCountInString(password) > 312 || strings.TrimSpace(password) == "" || strings.ContainsAny(password, "\r\n\x00")) {
		return cfg, fmt.Errorf("TRIPFOLIO_BACKUP_PASSWORD 必须为 8–312 个字符，不能全为空白或包含换行")
	}
	if cfg.Key != "" {
		key, err := base64.StdEncoding.DecodeString(cfg.Key)
		if err != nil || len(key) != 32 {
			return cfg, fmt.Errorf("TRIPFOLIO_BACKUP_KEY 必须是 32 字节随机密钥的 base64 编码，可用 tripfolio backup keygen 生成")
		}
	}
	size, err := strconv.ParseInt(get("BACKUP_MAX_MIB", "5120"), 10, 64)
	if err != nil || size < 1 || size > 65536 {
		return cfg, fmt.Errorf("TRIPFOLIO_BACKUP_MAX_MIB 必须为 1–65536")
	}
	cfg.MaxBytes = size << 20
	cfg.Timeout, err = time.ParseDuration(get("BACKUP_TIMEOUT", "1h"))
	if err != nil || cfg.Timeout < time.Minute || cfg.Timeout > 24*time.Hour {
		return cfg, fmt.Errorf("TRIPFOLIO_BACKUP_TIMEOUT 必须介于 1m 与 24h")
	}
	return cfg, nil
}
