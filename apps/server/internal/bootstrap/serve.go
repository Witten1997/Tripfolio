package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/adapters/queue"
	"tripfolio/server/internal/config"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/transport/httpapi"
	riverjobs "tripfolio/server/internal/transport/river"
	"tripfolio/server/internal/web"
)

// Phase 是启动阶段。日志、失败提示与退出码都按它区分，部署后可以直接定位卡在哪一步。
type Phase string

const (
	PhaseConfig   Phase = "读取配置"
	PhaseDatabase Phase = "连接数据库"
	PhaseMigrate  Phase = "数据库迁移"
	PhaseServices Phase = "装配服务"
	PhaseWorker   Phase = "启动后台任务"
	PhaseHTTP     Phase = "启动 HTTP 服务"
)

// StartupError 把启动失败绑定到阶段并附上排查建议：
// 容器部署后最常见的就是「起来就退出」，这里保证 stderr 上有一份能照着做的东西。
type StartupError struct {
	Phase Phase
	Cause error
	Hints []string
}

func (e *StartupError) Error() string { return fmt.Sprintf("%s失败：%v", e.Phase, e.Cause) }

func (e *StartupError) Unwrap() error { return e.Cause }

// StartupExitCode 把启动失败映射为可区分的退出码，便于部署脚本判断失败环节。
func StartupExitCode(err error) int {
	var startup *StartupError
	if !errors.As(err, &startup) {
		return 1
	}
	switch startup.Phase {
	case PhaseConfig:
		return 2
	case PhaseDatabase:
		return 3
	case PhaseMigrate:
		return 4
	case PhaseServices, PhaseWorker:
		return 5
	case PhaseHTTP:
		return 6
	default:
		return 1
	}
}

// ReportStartupFailure 向 w 打印一份中文排查块。它不经过日志级别过滤，
// 因此即使把 TRIPFOLIO_LOG_LEVEL 调到 error，部署失败的原因也不会丢。
func ReportStartupFailure(w io.Writer, err error) {
	fmt.Fprintln(w, "================ Tripfolio 启动失败 ================")
	var startup *StartupError
	if errors.As(err, &startup) {
		fmt.Fprintf(w, "阶段：%s\n", startup.Phase)
		fmt.Fprintf(w, "原因：%v\n", startup.Cause)
		if len(startup.Hints) > 0 {
			fmt.Fprintln(w, "排查建议：")
			for i, hint := range startup.Hints {
				fmt.Fprintf(w, "  %d. %s\n", i+1, hint)
			}
		}
	} else {
		fmt.Fprintf(w, "原因：%v\n", err)
	}
	fmt.Fprintf(w, "退出码：%d（2 配置、3 数据库、4 迁移、5 装配与任务、6 HTTP 监听）\n", StartupExitCode(err))
	fmt.Fprintln(w, "提示：同一次启动的日志带 phase 字段，可看到卡住的步骤；改完 .env 后重启容器即可重试。")
	fmt.Fprintln(w, "===================================================")
}

// NewConfigError 把配置校验失败包装成带排查建议的启动错误。
func NewConfigError(err error) *StartupError {
	return &StartupError{Phase: PhaseConfig, Cause: err, Hints: []string{
		"按上面的报错逐个补齐环境变量，变量含义与示例见 apps/server/.env.example 与 docker/.env.example",
		"生产环境（TRIPFOLIO_ENV=prod）要求签名密钥与高德 Web 服务 key；站点地址 http／https 都行（http 会自动关闭 Secure Cookie 并给出告警），邮件与对象存储可不配置",
		"不使用邮件时将 TRIPFOLIO_MAIL_DRIVER 留空或设为 disabled；显式使用 smtp 时须补齐邮件配置",
		"容器部署时确认变量确实传进了容器：docker compose config 可以看到最终生效值（注意不要外传输出，含凭证）",
	}}
}

// RunServe 是单二进制入口：等待数据库 → 自动迁移 → 同进程内启动 HTTP 服务与后台 worker。
// 前端产物已嵌入二进制，静态资源与 /_AMapService 代理由进程内的 web 处理器提供。
func RunServe(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	logger.Info("正在启动 tripfolio",
		"phase", string(PhaseConfig), "env", cfg.Env, "addr", cfg.HTTPAddr,
		"auto_migrate", cfg.AutoMigrate, "db_wait_timeout", cfg.StartupDBTimeout.String(),
		"database", safeTarget(cfg.DatabaseURL), "objectstore", cfg.ObjectStore.Endpoint,
		"web_base_url", cfg.WebBaseURL, "cors_origins", cfg.CORSOrigins)
	if web.MissingFrontend() {
		logger.Warn("二进制内没有前端产物，页面请求会返回说明页；接口与分享链接不受影响",
			"phase", string(PhaseConfig), "hint", "打包时先构建前端，见 scripts/package.sh 或 docker/Dockerfile")
	}
	if cfg.AMapJSCode == "" {
		logger.Warn("未配置 TRIPFOLIO_AMAP_JSCODE，地图底图与样式不可用（搜索与选点不受影响）",
			"phase", string(PhaseConfig))
	}
	if !cfg.ObjectStore.Configured() {
		logger.Warn("未配置对象存储，文件相关接口将返回依赖不可用", "phase", string(PhaseConfig))
	}
	if !cfg.Geo.Configured() {
		logger.Warn("未配置高德 Web 服务 key，地点接口将返回依赖不可用", "phase", string(PhaseConfig))
	}

	// 迁移用独立连接池：不设 statement_timeout，避免大表 DDL 被会话超时中断。
	migrationPool, err := waitForDatabase(ctx, cfg, logger)
	if err != nil {
		return &StartupError{Phase: PhaseDatabase, Cause: err, Hints: databaseHints(cfg)}
	}
	migrateErr := migrateBeforeServe(ctx, cfg, migrationPool, logger)
	migrationPool.Close()
	if migrateErr != nil {
		return &StartupError{Phase: PhaseMigrate, Cause: migrateErr, Hints: migrateHints()}
	}

	pool, err := pgcore.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return &StartupError{Phase: PhaseDatabase, Cause: err, Hints: databaseHints(cfg)}
	}
	defer pool.Close()

	services, err := BuildServices(pool, cfg, logger, nil)
	if err != nil {
		return &StartupError{Phase: PhaseServices, Cause: err, Hints: servicesHints(cfg)}
	}

	worker, err := startWorker(ctx, pool, cfg, services, logger)
	if err != nil {
		return &StartupError{Phase: PhaseWorker, Cause: err, Hints: workerHints(workerDiagnosis(ctx, pool))}
	}
	defer stopWorker(worker, cfg, logger)

	webHandler, err := web.NewHandler(web.Options{Logger: logger, JSCode: cfg.AMapJSCode})
	if err != nil {
		return &StartupError{Phase: PhaseServices, Cause: fmt.Errorf("组装前端资源与高德代理: %w", err), Hints: servicesHints(cfg)}
	}
	readiness, err := newDBReadiness(pool)
	if err != nil {
		return &StartupError{Phase: PhaseServices, Cause: err, Hints: servicesHints(cfg)}
	}

	router := httpapi.NewRouter(httpapi.Deps{
		Logger: logger, Metadata: metadata.Current(), Readiness: readiness, CORSOrigins: cfg.CORSOrigins,
		Cookies:  httpapi.CookieSettings{Secure: cfg.CookieSecure},
		Identity: services.Identity, Sessions: services.Sessions, Profile: services.Profile, Categories: services.Categories,
		Trips: services.Trips, Itinerary: services.Itinerary, Packing: services.Packing, Todos: services.Todos,
		Ledger: services.Ledger, Statistics: services.Statistics, Assets: services.Assets, Geo: services.Geo, Shares: services.Shares,
		Web: webHandler,
	})
	logger.Info("HTTP 服务准备就绪",
		"phase", string(PhaseHTTP), "addr", cfg.HTTPAddr, "cors_origins", cfg.CORSOrigins,
		"cookie_secure", cfg.CookieSecure, "mail_driver", cfg.Mail.Driver, "frontend_embedded", !web.MissingFrontend())
	srv := httpapi.NewServer(cfg.HTTPAddr, router)
	if err := httpapi.Serve(ctx, srv, cfg.ShutdownTimeout, logger); err != nil {
		return &StartupError{Phase: PhaseHTTP, Cause: err, Hints: httpHints(cfg)}
	}
	logger.Info("已优雅退出", "phase", string(PhaseHTTP))
	return nil
}

// waitForDatabase 在超时内反复尝试连接：容器与外部数据库不一定同时就绪，
// 直接失败会让「数据库还在启动」看起来像配置错误。
func waitForDatabase(ctx context.Context, cfg config.Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(cfg.StartupDBTimeout)
	for attempt := 1; ; attempt++ {
		logger.Info("正在连接数据库", "phase", string(PhaseDatabase), "attempt", attempt,
			"target", safeTarget(cfg.DatabaseURL), "timeout", cfg.StartupDBTimeout.String())
		pool, err := pgcore.NewMigrationPool(ctx, cfg.DatabaseURL)
		if err == nil {
			logger.Info("数据库连接成功", "phase", string(PhaseDatabase), "attempt", attempt, "target", safeTarget(cfg.DatabaseURL))
			return pool, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("等待数据库时收到退出信号：%w", ctx.Err())
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待数据库 %s 超时（%s，共尝试 %d 次）：%w",
				safeTarget(cfg.DatabaseURL), cfg.StartupDBTimeout, attempt, err)
		}
		logger.Warn("数据库暂时不可用，稍后重试", "phase", string(PhaseDatabase), "attempt", attempt,
			"retry_in", cfg.StartupDBRetryInterval.String(), "error", err)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("等待数据库时收到退出信号：%w", ctx.Err())
		case <-time.After(cfg.StartupDBRetryInterval):
		}
	}
}

// migrateBeforeServe 在启动时完成迁移。跳过迁移时仍然核对版本，
// 否则新程序配旧结构会在运行期才暴露问题。
func migrateBeforeServe(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) error {
	if !cfg.AutoMigrate {
		readiness, err := newDBReadiness(pool)
		if err != nil {
			return err
		}
		if err := readiness.Check(ctx); err != nil {
			return fmt.Errorf("TRIPFOLIO_AUTO_MIGRATE=false 已跳过迁移，但数据库结构不满足要求：%w", err)
		}
		logger.Info("已跳过自动迁移，数据库结构版本符合要求", "phase", string(PhaseMigrate))
		return nil
	}
	logger.Info("开始数据库迁移", "phase", string(PhaseMigrate))
	if err := migrateRiver(ctx, pool, logger); err != nil {
		return err
	}
	return migrateBusinessUp(ctx, pool, logger)
}

func startWorker(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, services Services, logger *slog.Logger) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	deps := riverjobs.Deps{Logger: logger}
	// 未配置对象存储时 AssetVerifier 为 nil 指针；保持接口为 nil，让 worker 走推迟分支而不是解引用。
	if services.AssetVerifier != nil {
		deps.AssetVerifier = services.AssetVerifier
	}
	riverjobs.RegisterWorkers(workers, deps)

	// 每次尝试都新建客户端：构造不碰数据库，而失败过的客户端交给下一个尝试复用没有意义。
	var client *river.Client[pgx.Tx]
	err := retryStart(ctx, workerStartAttempts, workerStartBackoff, logger, func(attemptCtx context.Context) error {
		created, err := queue.NewWorkerClient(pool, logger, workers, cfg.WorkerMaxJobs)
		if err != nil {
			return fmt.Errorf("创建任务客户端: %w", err)
		}
		if err := created.Start(attemptCtx); err != nil {
			return err
		}
		client = created
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("启动任务消费: %w", err)
	}
	logger.Info("后台任务已启动", "phase", string(PhaseWorker), "max_jobs", cfg.WorkerMaxJobs)
	return client, nil
}

// workerStartAttempts 与 workerStartBackoff 是启动任务消费的重试策略。
// River 拉取队列设置的超时是它内部写死的 10 秒（producer.StartWorkContext），而这一步是对
// river_queue 的 INSERT ... ON CONFLICT (name) DO UPDATE：远端或跨网数据库抖动一下、或被同一行的
// 行锁挡住一会儿，单次尝试就会失败。容器因此退出再由编排重启的代价远大于原地重试几次。
const (
	workerStartAttempts = 3
	workerStartBackoff  = 2 * time.Second
)

// workerDiagnosisTimeout 是失败后现场取证的预算：取证只是为了让报错更具体，不能拖慢失败退出。
const workerDiagnosisTimeout = 5 * time.Second

// retryStart 反复调用 start 直到成功、用尽尝试次数或收到退出信号。返回最后一次的原始错误，
// 不包装——调用方要保留 River 自己的措辞（例如 10 秒超时）以便排障。
func retryStart(
	ctx context.Context,
	attempts int,
	backoff time.Duration,
	logger *slog.Logger,
	start func(context.Context) error,
) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}
		lastErr = start(ctx)
		if lastErr == nil {
			if attempt > 1 {
				logger.Info("任务消费启动成功", "phase", string(PhaseWorker), "attempt", attempt)
			}
			return nil
		}
		if attempt == attempts {
			break
		}
		logger.Warn("任务消费启动失败，稍后重试", "phase", string(PhaseWorker),
			"attempt", attempt, "attempts", attempts, "error", lastErr)
		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(backoff * time.Duration(attempt)):
		}
	}
	return lastErr
}

// workerDiagnosis 在任务消费重试都失败后现场取证，把「数据库到底怎么了」写进失败块：
// River 卡住的那条语句要拿 river_queue 的行锁，所以「库本身很慢」和「别的事务占着锁」会表现成
// 同一句超时，只有探测才能区分。探测自身失败也不能影响报错，因此这里从不返回错误。
func workerDiagnosis(ctx context.Context, pool *pgxpool.Pool) []string {
	if pool == nil {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workerDiagnosisTimeout)
	defer cancel()

	notes := make([]string, 0, 2)
	start := time.Now()
	if err := pool.Ping(probeCtx); err != nil {
		notes = append(notes, fmt.Sprintf("数据库探测失败（耗时 %v）：%v", time.Since(start).Round(time.Millisecond), err))
	} else {
		notes = append(notes, fmt.Sprintf("数据库探测正常，往返耗时 %v；连接本身没问题，超时更可能是锁等待或瞬时抖动",
			time.Since(start).Round(time.Millisecond)))
	}

	rows, err := pool.Query(probeCtx, `
		SELECT pid,
		       state,
		       coalesce(wait_event_type, ''),
		       coalesce(wait_event, ''),
		       coalesce(round(extract(epoch FROM now() - xact_start))::bigint, 0),
		       left(regexp_replace(coalesce(query, ''), '\s+', ' ', 'g'), 70)
		FROM pg_stat_activity
		WHERE datname = current_database()
		  AND pid <> pg_backend_pid()
		  AND (xact_start IS NOT NULL OR state <> 'idle')
		ORDER BY xact_start NULLS LAST
		LIMIT 3`)
	if err != nil {
		notes = append(notes, fmt.Sprintf("无法读取其它会话状态：%v", err))
		return notes
	}
	defer rows.Close()

	for rows.Next() {
		var (
			pid                       int
			state, waitType, waitName string
			xactAge                   int64
			query                     string
		)
		if err := rows.Scan(&pid, &state, &waitType, &waitName, &xactAge, &query); err != nil {
			notes = append(notes, fmt.Sprintf("读取会话状态失败：%v", err))
			break
		}
		waiting := ""
		if waitName != "" {
			waiting = fmt.Sprintf("，等待 %s", waitName)
		}
		notes = append(notes, fmt.Sprintf("另一会话 pid=%d 状态 %s%s，事务已开 %ds，SQL：%s",
			pid, state, waiting, xactAge, query))
	}
	if err := rows.Err(); err != nil {
		notes = append(notes, fmt.Sprintf("读取会话状态中断：%v", err))
	}
	return notes
}

// stopWorker 在退出时等待在途任务结束，超时则取消剩余任务。
func stopWorker(client *river.Client[pgx.Tx], cfg config.Config, logger *slog.Logger) {
	if client == nil {
		return
	}
	logger.Info("等待在途任务完成", "phase", string(PhaseWorker), "timeout", cfg.ShutdownTimeout.String())
	stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := client.Stop(stopCtx); err != nil {
		logger.Warn("在超时内未能优雅停止，取消剩余任务", "phase", string(PhaseWorker), "error", err)
		_ = client.StopAndCancel(context.Background())
		return
	}
	logger.Info("后台任务已停止", "phase", string(PhaseWorker))
}

// safeTarget 从连接串里取出「主机/库名」用于日志，绝不打印用户名与密码。
func safeTarget(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "（连接串无法解析）"
	}
	return parsed.Host + "/" + strings.TrimPrefix(parsed.Path, "/")
}

func databaseHints(cfg config.Config) []string {
	return []string{
		"确认 TRIPFOLIO_DATABASE_URL 指向已有实例，且容器能解析该主机名（容器内不能用 localhost 指代宿主机）",
		"确认库已创建、账号密码正确；连接串格式为 postgres://用户:密码@主机:5432/库名?sslmode=disable",
		"密码含 @ : / ? # 等字符时必须按 URL 编码（例如 @ 写成 %40），否则连接串会被截断，" +
			"表现为「连接串无法解析」或连到错误的主机",
		"确认数据库所在主机的防火墙或安全组允许来自容器网段的 5432 端口",
		fmt.Sprintf("当前等待上限为 %s，网络较慢时可调大 TRIPFOLIO_STARTUP_DB_TIMEOUT", cfg.StartupDBTimeout),
	}
}

func migrateHints() []string {
	return []string{
		"确认数据库账号有建表权限（迁移需要 CREATE TABLE、ALTER TABLE、CREATE INDEX）",
		"确认没有另一个进程正在迁移：迁移使用 PostgreSQL 会话锁，持锁时会快速失败",
		"先用 tripfolio migrate status 查看已应用版本，再单独执行 tripfolio migrate up 看完整报错",
		"迁移语句报的具体版本号在日志里，可对照 db/migrations 下的同名文件",
	}
}

func servicesHints(cfg config.Config) []string {
	return []string{
		"生产环境要求签名密钥与高德 Web 服务 key 齐备；邮件和对象存储可不配置",
		"确认 TRIPFOLIO_KEYRING 形如 k1=<base64 至少 32 字节>，可用 openssl rand -base64 32 生成",
		"确认对象存储端点、桶名、AK/SK 与 TRIPFOLIO_OBJECTSTORE_USE_PATH_STYLE 匹配（OSS 用 false）",
		fmt.Sprintf("当前邮件驱动为 %q；不使用邮件可设为 disabled，生产不允许 log", cfg.Mail.Driver),
	}
}

// workerHints 是「启动后台任务」阶段的排查建议。它与装配失败完全是两回事，所以不再复用
// servicesHints——那套讲的是签名密钥与对象存储，对着「拉取队列设置超时」只会把排查带偏。
func workerHints(diagnosis []string) []string {
	hints := make([]string, 0, 5)
	if len(diagnosis) > 0 {
		hints = append(hints, "现场信息（失败后探测到的数据库状态）：")
		for _, note := range diagnosis {
			hints = append(hints, "  - "+note)
		}
	}
	return append(hints,
		fmt.Sprintf("River 拉取队列设置的超时是它内部写死的 10 秒（不是 TRIPFOLIO_* 配置项）；本次已重试 %d 次仍失败",
			workerStartAttempts),
		"报 lock timeout（SQLSTATE 55P03）说明确实被锁挡住——连接池设了 lock_timeout=5s，所以 5 秒就返回："+
			"执行 SELECT pid, state, now()-xact_start AS xact_age, left(query,80) FROM pg_stat_activity "+
			"WHERE datname = current_database() ORDER BY xact_start NULLS LAST; 找到长时间未提交的事务，"+
			"确认无用后 SELECT pg_terminate_backend(pid) 结束它",
		"报 context deadline exceeded（满 10 秒才失败）说明这 10 秒数据库根本没响应，不是锁等待："+
			"对照上面的现场信息——探测耗时正常多半是一次网络抖动，重启容器即可；探测也慢或失败则查数据库与网络本身",
		"远端或跨网数据库：阶段 3、4 已通过说明网络本身是通的，再看这段时间数据库有没有在做备份或大查询",
		"任务消费失败不会降级启动——没有 worker 时资产确认、旅行清理等后台任务不会执行，带着残缺能力运行比直接报错更难查",
	)
}

func httpHints(cfg config.Config) []string {
	return []string{
		fmt.Sprintf("确认 %s 没有被其他进程占用（容器里就是容器内的端口，不要写成宿主端口）", cfg.HTTPAddr),
		"确认容器端口映射与 TRIPFOLIO_HTTP_ADDR 一致，例如 -p 8080:8080 对应 :8080",
		"若前面的反向代理提前断开长连接，检查其超时设置与 TRIPFOLIO_SHUTDOWN_TIMEOUT 的配合",
	}
}
