package config_test

import (
	"os"
	"strings"
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

func TestLoadDotEnvToleratesBOMAndHidesSecrets(t *testing.T) {
	t.Chdir(t.TempDir())
	const key = "TRIPFOLIO_DOTENV_BOM_TEST"
	const secret = "sup3r-secret-value"
	t.Cleanup(func() { _ = os.Unsetenv(key) })
	_ = os.Unsetenv(key)

	// Windows 记事本与 PowerShell 写出的文件带 UTF-8 BOM：要能正常解析，而不是卡在一个看不懂的字符上。
	content := "\uFEFF" + key + "=" + secret + "\n"
	if err := os.WriteFile(config.DotEnvFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadDotEnv(os.Getenv)
	if err != nil || !loaded {
		t.Fatalf("带 BOM 的 .env 应能读取：loaded=%v err=%v", loaded, err)
	}
	if got := os.Getenv(key); got != secret {
		t.Fatalf("值 = %q，期望 %q", got, secret)
	}

	// 解析失败时不能把文件内容（含密钥）回显到日志里。
	_ = os.Unsetenv(key)
	bad := "\uFEFFTRIPFOLIO_OK=1\n密码=" + secret + "\n"
	if err := os.WriteFile(config.DotEnvFile, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = config.LoadDotEnv(os.Getenv)
	if err == nil {
		t.Fatal("非法键名应当报错")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("报错回显了密钥内容：%v", err)
	}
	if !strings.Contains(err.Error(), config.DotEnvFile) {
		t.Fatalf("报错应指出文件名：%v", err)
	}
}
