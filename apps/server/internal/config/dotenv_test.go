package config_test

import (
	"os"
	"testing"

	"tripfolio/server/internal/config"
)

func TestLoadDotEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	const key = "TRIPFOLIO_DOTENV_TEST"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	// 无文件：不报错、不读取
	if loaded, err := config.LoadDotEnv(os.Getenv); err != nil || loaded {
		t.Fatalf("missing file: loaded=%v err=%v", loaded, err)
	}

	if err := os.WriteFile(config.DotEnvFile, []byte("# 注释\n"+key+"=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if loaded, err := config.LoadDotEnv(os.Getenv); err != nil || !loaded || os.Getenv(key) != "from-file" {
		t.Fatalf("load file: loaded=%v err=%v value=%q", loaded, err, os.Getenv(key))
	}

	// 已存在的环境变量优先
	_ = os.Unsetenv(key)
	t.Setenv(key, "from-process")
	if loaded, err := config.LoadDotEnv(os.Getenv); err != nil || !loaded || os.Getenv(key) != "from-process" {
		t.Fatalf("process env should win: loaded=%v err=%v value=%q", loaded, err, os.Getenv(key))
	}

	// 生产环境不读取
	_ = os.Unsetenv(key)
	t.Setenv("TRIPFOLIO_ENV", "prod")
	if loaded, err := config.LoadDotEnv(os.Getenv); err != nil || loaded || os.Getenv(key) != "" {
		t.Fatalf("prod must skip: loaded=%v err=%v value=%q", loaded, err, os.Getenv(key))
	}

	// 文件格式错误：报错
	t.Setenv("TRIPFOLIO_ENV", "dev")
	if err := os.WriteFile(config.DotEnvFile, []byte("not a valid line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadDotEnv(os.Getenv); err == nil {
		t.Fatal("malformed file should fail")
	}
}
