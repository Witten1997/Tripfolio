package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
		"后台任务": workerHints(nil),
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

// 后台任务的失败原因与装配无关：排查建议要按报错形状分流——锁等待会撞上我们自己的
// lock_timeout（5 秒、SQLSTATE 55P03），而满 10 秒的 context deadline exceeded 说明数据库
// 这 10 秒没响应，两者处理方式完全不同。
func TestWorkerHintsExplainQueueSettingsTimeout(t *testing.T) {
	hints := workerHints([]string{"数据库探测正常，往返耗时 3ms"})
	text := strings.Join(hints, "\n")
	for _, want := range []string{
		"数据库探测正常", "10 秒", "55P03", "lock_timeout", "context deadline exceeded",
		"pg_stat_activity", "pg_terminate_backend",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("后台任务排查建议缺少 %q：\n%s", want, text)
		}
	}
	// 现场信息要排在建议之前：先看到事实，再看怎么做。
	if len(hints) < 2 || !strings.Contains(hints[0], "现场信息") || !strings.Contains(hints[1], "数据库探测正常") {
		t.Errorf("现场信息应紧跟在标题之后：%v", hints)
	}
}

func TestRetryStartRetriesThenSucceeds(t *testing.T) {
	var calls int
	boom := errors.New("producer.StartWorkContext timed out after 10s")
	err := retryStart(context.Background(), 3, time.Millisecond, discardLogger(), func(context.Context) error {
		calls++
		if calls < 3 {
			return boom
		}
		return nil
	})
	if err != nil {
		t.Fatalf("第 3 次成功后不应报错：%v", err)
	}
	if calls != 3 {
		t.Errorf("调用次数 = %d，期望 3", calls)
	}
}

// 重试耗尽后必须把 River 的原始错误原样抛出：那句 10 秒超时是排障的关键信息。
func TestRetryStartReturnsLastError(t *testing.T) {
	boom := errors.New("producer.StartWorkContext timed out after 10s")
	var calls int
	err := retryStart(context.Background(), 3, time.Millisecond, discardLogger(), func(context.Context) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("错误 = %v，期望原始错误", err)
	}
	if calls != 3 {
		t.Errorf("调用次数 = %d，期望 3", calls)
	}
}

// 退出信号到达时不再继续重试，也不额外等待退避。
func TestRetryStartStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	boom := errors.New("producer.StartWorkContext timed out after 10s")
	var calls int
	started := time.Now()
	err := retryStart(ctx, 3, time.Hour, discardLogger(), func(context.Context) error {
		calls++
		cancel()
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("错误 = %v，期望原始错误", err)
	}
	if calls != 1 {
		t.Errorf("取消后不应再重试，调用次数 = %d", calls)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("取消后应立即返回，实际耗时 %v", elapsed)
	}
}

// 探测本身失败也不能把原始报错顶掉：现场信息只是附加说明。
func TestWorkerDiagnosisSurvivesUnusablePool(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://user:pass@127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatalf("构造连接池失败：%v", err)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	notes := workerDiagnosis(ctx, pool)
	if len(notes) == 0 {
		t.Fatal("连不上数据库时也应给出探测结论")
	}
	if !strings.Contains(strings.Join(notes, "\n"), "数据库探测失败") {
		t.Errorf("应说明探测失败：%v", notes)
	}
	if notes := workerDiagnosis(ctx, nil); notes != nil {
		t.Errorf("没有连接池时应返回空，实际 %v", notes)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
