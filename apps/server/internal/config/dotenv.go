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
	if _, err := os.Stat(DotEnvFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("检查 %s: %w", DotEnvFile, err)
	}
	if err := godotenv.Load(DotEnvFile); err != nil {
		return false, fmt.Errorf("读取 %s: %w", DotEnvFile, err)
	}
	return true, nil
}
