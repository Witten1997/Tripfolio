package config

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"time"
)

type BackupConfig struct {
	Key, DatabaseURL, Directory string
	MaxBytes                    int64
	Timeout                     time.Duration
	AllowedHosts                []string
}

func loadBackup(get func(string, string) string) (BackupConfig, error) {
	cfg := BackupConfig{Key: get("BACKUP_KEY", ""), DatabaseURL: get("BACKUP_DATABASE_URL", ""), Directory: get("BACKUP_DIR", ""), AllowedHosts: splitList(get("BACKUP_WEBDAV_ALLOWED_HOSTS", ""))}
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
