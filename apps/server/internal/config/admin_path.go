package config

import (
	"fmt"
	"regexp"
	"strings"
)

const DefaultAdminPath = "/wahaha"

var adminPathPattern = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ParseAdminPath 只允许单层路径，拒绝编码、路由参数及已有页面和 API 命名空间。
func ParseAdminPath(raw string) (string, error) {
	if raw == "" {
		return DefaultAdminPath, nil
	}
	value := strings.TrimSuffix(strings.TrimSpace(raw), "/")
	if !adminPathPattern.MatchString(value) {
		return "", fmt.Errorf("%sADMIN_PATH 必须是 / 开头的单层路径，名称为 1–64 位字母、数字、下划线或连字符，且以字母或数字开头", Prefix)
	}
	switch strings.ToLower(value) {
	case "/admin", "/api", "/health", "/assets", "/auth", "/metadata", "/public", "/geo",
		"/account", "/accounts", "/categories", "/expense-categories", "/trips", "/dashboard",
		"/login", "/register", "/reset-password", "/personal-settings", "/change-password",
		"/login-devices", "/recycle-bin", "/themes", "/s", "/dev", "/sync", "/files", "/packing-library":
		return "", fmt.Errorf("%sADMIN_PATH 与已有页面或接口路径冲突", Prefix)
	}
	return value, nil
}

func (c Config) AdminAPIPath() string {
	if c.AdminPath == "" {
		return "/api/v1" + DefaultAdminPath
	}
	return "/api/v1" + c.AdminPath
}
