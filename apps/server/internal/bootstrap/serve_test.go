package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tripfolio/server/internal/config"
)

// configStub 只填排查建议里用到的字段，避免测试依赖真实环境变量。
func configStub() config.Config {
	return config.Config{
		HTTPAddr:         ":8080",
		StartupDBTimeout: 60 * time.Second,
		ShutdownTimeout:  15 * time.Second,
		Mail:             config.MailConfig{Driver: "smtp"},
	}
}

func TestStartupExitCode(t *testing.T) {
	cases := []struct {
		phase Phase
		want  int
	}{
		{PhaseConfig, 2},
		{PhaseDatabase, 3},
		{PhaseMigrate, 4},
		{PhaseServices, 5},
		{PhaseWorker, 5},
		{PhaseHTTP, 6},
	}
	for _, tc := range cases {
		err := &StartupError{Phase: tc.phase, Cause: errors.New("boom")}
		if got := StartupExitCode(err); got != tc.want {
			t.Errorf("%s 退出码 = %d，期望 %d", tc.phase, got, tc.want)
		}
	}
	if got := StartupExitCode(errors.New("普通错误")); got != 1 {
		t.Errorf("非启动错误退出码 = %d，期望 1", got)
	}
	// 包装后仍要能识别阶段，便于在调用链里加一层上下文。
	wrapped := fmt.Errorf("外层: %w", &StartupError{Phase: PhaseMigrate, Cause: errors.New("x")})
	if got := StartupExitCode(wrapped); got != 4 {
		t.Errorf("包装后的退出码 = %d，期望 4", got)
	}
}

func TestReportStartupFailurePrintsActionableBlock(t *testing.T) {
	err := &StartupError{
		Phase: PhaseDatabase,
		Cause: errors.New("等待数据库 db:5432/tripfolio 超时（60s）：连接被拒绝"),
		Hints: []string{"确认连接串", "确认网络与安全组"},
	}
	var out bytes.Buffer
	ReportStartupFailure(&out, err)
	text := out.String()
	for _, want := range []string{
		"Tripfolio 启动失败", "阶段：连接数据库", "原因：", "连接被拒绝",
		"排查建议：", "1. 确认连接串", "2. 确认网络与安全组", "退出码：3",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("输出缺少 %q：\n%s", want, text)
		}
	}

	// 非 StartupError 也要给出原因与默认退出码，不能只打印一句「失败」。
	out.Reset()
	ReportStartupFailure(&out, errors.New("未知问题"))
	if !strings.Contains(out.String(), "未知问题") || !strings.Contains(out.String(), "退出码：1") {
		t.Errorf("普通错误输出不完整：\n%s", out.String())
	}
}

func TestSafeTargetHidesCredentials(t *testing.T) {
	got := safeTarget("postgres://tripfolio:sup3r-secret@db.example.com:5432/tripfolio?sslmode=disable")
	if got != "db.example.com:5432/tripfolio" {
		t.Errorf("safeTarget = %q", got)
	}
	if strings.Contains(got, "sup3r-secret") || strings.Contains(got, "tripfolio:") {
		t.Fatalf("日志目标里不应出现账号或密码：%q", got)
	}
	if got := safeTarget("这不是连接串"); got == "" || strings.Contains(got, "不是") {
		t.Errorf("无法解析时应返回固定说明，实际 %q", got)
	}
}

func TestHintsAreNotEmpty(t *testing.T) {
	groups := map[string][]string{
		"配置":   NewConfigError(errors.New("缺少 TRIPFOLIO_KEYRING")).Hints,
		"数据库":  databaseHints(configStub()),
		"迁移":   migrateHints(),
		"服务":   servicesHints(configStub()),
		"HTTP": httpHints(configStub()),
	}
	for name, hints := range groups {
		if len(hints) == 0 {
			t.Errorf("%s 阶段没有排查建议", name)
		}
		for _, hint := range hints {
			if strings.TrimSpace(hint) == "" {
				t.Errorf("%s 阶段存在空白建议", name)
			}
		}
	}
}
