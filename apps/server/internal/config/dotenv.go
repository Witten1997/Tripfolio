package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// DotEnvFile 是开发环境启动时自动读取的配置文件，位于进程工作目录（通常是 apps/server）。
const DotEnvFile = ".env"

// LoadDotEnv 把工作目录下的 .env 补充进进程环境变量，供随后的 Load 读取。
// 规则：已存在的环境变量优先，文件不覆盖；TRIPFOLIO_ENV=prod 时不读取，生产配置只由部署平台注入；
// 文件不存在不是错误。返回是否读取了文件，便于入口打印一行提示。
func LoadDotEnv(getenv func(string) string) (bool, error) {
	if strings.TrimSpace(getenv(Prefix+"ENV")) == "prod" {
		return false, nil
	}
	data, err := os.ReadFile(DotEnvFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("读取 %s: %w", DotEnvFile, err)
	}
	// Windows 记事本与 PowerShell 写出的文件常带 UTF-8 BOM；不剥掉会失败在一个看不懂的字符上。
	content := strings.TrimPrefix(string(data), "\uFEFF")
	values, err := godotenv.Unmarshal(content)
	if err != nil {
		return false, fmt.Errorf("解析 %s 失败：%w", DotEnvFile, sanitizeDotEnvError(err))
	}
	for key, value := range values {
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return false, fmt.Errorf("设置环境变量 %s: %w", key, err)
			}
		}
	}
	return true, nil
}

// sanitizeDotEnvError 去掉解析错误里回显的原始行内容：.env 里是密钥，
// 排查需要的是「哪一类语法问题」，不是把密码抄进容器日志。
func sanitizeDotEnvError(err error) error {
	message := err.Error()
	if idx := strings.Index(message, " near "); idx >= 0 {
		message = message[:idx]
	}
	if len(message) > 120 {
		message = message[:120] + "…"
	}
	return fmt.Errorf("%s（为避免泄露密钥，不回显文件内容；请检查该行的键名与等号）", message)
}
