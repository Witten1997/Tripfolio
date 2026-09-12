# 工程骨架实施计划（Go 后端 + Vue 前端 + 契约包）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立可编译、可测试、可运行的单仓库骨架：Go API/worker/migrate 三个入口、Vue 3 双壳前端与 Capacitor 安卓工程、OpenAPI 契约包及两端代码生成管线、本地 PostgreSQL 与 CI 配置；业务内容只放入验证管线所需的最小真实切片（metadata 接口、accounts 表、金额规范化）。

**Architecture:** 按《后端代码层级结构设计 v0.2》的目录落地 `apps/server`（cmd → bootstrap → transport/modules/adapters/foundation），契约放 `packages/contracts`（拆分源文件 + redocly 打包 + oapi-codegen/openapi-typescript 双向生成），前端按《技术选型 v0.4》第 4、9 节放 `apps/client`（一个 Vue 工程、桌面壳与移动壳、`src/platform` 平台接口、Capacitor `android/`）。

**Tech Stack:** Go 1.27.1（GOTOOLCHAIN 自动下载，GOPROXY=goproxy.cn）、chi v5.3.2、pgx v5.11.0、River v0.47.0、goose v3.28.0、oapi-codegen v2.8.0（`go tool`）、sqlc 1.31.1（Docker 镜像，本机无 cgo 工具链）、PostgreSQL 18；Node 24、pnpm 10、Vue 3.5、Vite 8、vue-router 5、Pinia 4、Vant 4、Element Plus 2.14、TypeScript 5.9（vue-tsc 3.3 尚未验证 TS 7）、Vitest 5、openapi-typescript 7.13、openapi-fetch 0.17、@redocly/cli 2.52、Capacitor 8.5 + @aparajita/capacitor-secure-storage 8。

**Spec:** `docs/architecture/2026-09-11-后端代码层级结构设计.md`（目录与依赖方向）、`docs/requirements/2026-09-10-技术选型与架构建议.md`（v0.4，第 3、4、8、9、10 节）、`docs/api/2026-09-11-P0接口设计.md`（1.1、1.4、§6、§7）、`docs/database/2026-09-11-P0数据库表结构设计.md`（§1 全局约定、表 1、表 15）。

## Global Constraints

- 模块不导入 transport、adapters、workflows；dbgen 只被 postgres 适配器使用；HTTP generated 只被 HTTP 层使用（结构文档 §6）。
- 业务路径前缀 `/api/v1`；探针 `/health/live`、`/health/ready` 不带前缀（接口 1.1、§7）。
- 错误统一 `application/problem+json`，字段 type、title、status、code、detail、request_id；`X-Request-ID` 放响应头（接口 1.4）。
- 可同步写事务 READ COMMITTED；连接统一设置 lock_timeout、statement_timeout、idle_in_transaction_session_timeout（数据库 §1）。
- CORS 允许来源：网页域与 Capacitor 来源 `https://localhost`（技术选型 4.3、§8）。
- 金额十进制字符串，按币种小数位规范化，"128.5" 与 "128.50" 相同（接口 1.2、1.3）。
- 币种清单 21 项，JPY/KRW/VND 0 位、KWD/BHD 3 位、其余 2 位，默认 CNY（接口 §6）。
- 生成代码提交入库，CI 检查与源文件一致；不手工修改生成文件（结构文档 §9）。
- 本机约束：无 make/gcc，sqlc 用 Docker 运行；Git Bash 下挂载路径用 `pwd -W` 与 `MSYS_NO_PATHCONV=1`。

---

### Task 1: 仓库根与本机环境

**Files:**
- Create: `.gitignore`、`.gitattributes`、`.editorconfig`、`.node-version`、`package.json`、`pnpm-workspace.yaml`、`README.md`
- Env: `go env -w GOPROXY=https://goproxy.cn,direct`

**Interfaces:**
- Produces: pnpm workspace（`apps/client`、`packages/contracts`）；根脚本 `pnpm build|typecheck|test|contracts:gen`。

- [x] **Step 1: 设置 Go 代理**（依据：proxy.golang.org 12 秒超时，goproxy.cn 1.4 秒 200）

```bash
go env -w GOPROXY=https://goproxy.cn,direct
```

- [x] **Step 2: 写入根文件**（`.gitattributes` 强制 LF；`.gitignore` 覆盖 node_modules、dist、coverage、.env、apps/server/bin、android 构建目录与 local.properties）
- [x] **Step 3: `pnpm-workspace.yaml`** 列出 `apps/client`、`packages/contracts`
- [x] **Step 4: 验证** `pnpm -v` 与 `go env GOPROXY` 输出预期值

### Task 2: 契约包 packages/contracts

**Files:**
- Create: `packages/contracts/package.json`、`openapi/v1/openapi.yaml`、`openapi/v1/paths/metadata.yaml`、`openapi/v1/schemas/common.yaml`、`openapi/v1/schemas/metadata.yaml`、`fixtures/money.json`、`README.md`
- Generated: `packages/contracts/dist/openapi.v1.yaml`（redocly bundle）、`packages/contracts/generated/openapi.v1.d.ts`（openapi-typescript）

**Interfaces:**
- Produces: 操作 `getMetadata`（GET /metadata → Metadata）；`Problem` 模型；TS 类型导出 `@tripfolio/contracts/openapi/v1` 的 `paths`、`components`；金额样例 `fixtures/money.json`：`[{ "input": "128.5", "currency": "CNY", "canonical": "128.50" }, ...]`，含应被拒绝的样例 `{ "input": "1.234", "currency": "CNY", "error": "SCALE_EXCEEDED" }`。

- [x] **Step 1: 写 OpenAPI 源文件**：`openapi: 3.0.3`，`servers: [{ url: /api/v1 }]`，Metadata 全部字段 required；Problem 含 errors 可选数组。
- [x] **Step 2: 写 fixtures/money.json**（覆盖 0 位、2 位、3 位币种、前导零、负号拒绝、科学计数拒绝、超精度拒绝）。
- [x] **Step 3: package.json 脚本** `lint`（redocly lint）、`bundle`（redocly bundle → dist）、`gen:ts`（openapi-typescript dist → generated）、`build`（bundle + gen:ts）。
- [x] **Step 4: 运行** `pnpm --filter @tripfolio/contracts build`，期望生成两份文件且 lint 无 error。

### Task 3: Go 模块与配置

**Files:**
- Create: `apps/server/go.mod`、`cmd/api/main.go`、`cmd/worker/main.go`、`cmd/migrate/main.go`、`internal/config/config.go`、`internal/config/config_test.go`、`.env.example`、`README.md`

**Interfaces:**
- Produces: `config.Load(getenv func(string) string) (Config, error)`；`Config{Env, HTTPAddr, DatabaseURL, DBMaxConns int32, LogLevel slog.Level, ShutdownTimeout time.Duration, CORSOrigins []string}`；环境变量前缀 `TRIPFOLIO_`；缺少 `TRIPFOLIO_DATABASE_URL` 返回错误；默认 HTTP_ADDR `:8080`、LOG_LEVEL `info`、SHUTDOWN_TIMEOUT `15s`、DB_MAX_CONNS `10`、CORS_ORIGINS `http://localhost:5173,https://localhost`。

- [x] **Step 1: 写失败测试** `config_test.go`：默认值、必填校验、非法日志级别报错、CORS 逗号切分去空格。
- [x] **Step 2: `go mod init tripfolio/server`**，go 指令 1.27.1；`go test ./internal/config/` 预期编译失败。
- [x] **Step 3: 实现 config.go**；`go test ./internal/config/` 通过。
- [x] **Step 4: 三个 main.go** 只做：信号上下文、调用 bootstrap、非零退出。

### Task 4: 数据库层：迁移、pgcore、sqlc

**Files:**
- Create: `apps/server/db/embed.go`、`db/migrations/00001_accounts.sql`、`db/queries/account/accounts.sql`、`sqlc.yaml`、`scripts/sqlc.sh`、`internal/adapters/postgres/pgcore/pool.go`、`internal/bootstrap/migrate.go`
- Generated: `internal/adapters/postgres/dbgen/*.go`

**Interfaces:**
- Produces: `db.Migrations embed.FS`；`db.LatestVersion() (int64, error)`（解析文件名前缀取最大版本）；`pgcore.NewPool(ctx, url string, maxConns int32) (*pgxpool.Pool, error)`（RuntimeParams：application_name=tripfolio、lock_timeout=5s、statement_timeout=30s、idle_in_transaction_session_timeout=30s）；`bootstrap.RunMigrate(ctx, cfg, args)` 支持 `up`（先 River 后业务）、`status`；`dbgen.Queries` 的 `CreateAccount`、`GetAccountByEmailKey`、`GetAccountByID`、`CreateAccountSyncState`。
- 迁移 00001 内容：accounts（表 1 全部列与 CHECK，不含 avatar 外键）、account_sync_state（表 15，fillfactor=70）。

- [x] **Step 1: 写迁移 SQL 与查询 SQL**（goose `-- +goose Up/Down` 标记）。
- [x] **Step 2: sqlc.yaml**（engine postgresql、schema db/migrations、queries db/queries、sql_package pgx/v5、out internal/adapters/postgres/dbgen、package dbgen）。
- [x] **Step 3: `scripts/sqlc.sh generate`** 通过 Docker 运行 `sqlc/sqlc:1.31.1`；检查生成文件存在并 `go build ./...`。
- [x] **Step 4: pgcore.NewPool 与 migrate.go**（goose Provider + PostgresSessionLocker；River `rivermigrate`）。

### Task 5: HTTP 层：中间件、错误、探针、metadata

**Files:**
- Create: `internal/modules/metadata/metadata.go`、`metadata_test.go`、`internal/foundation/money/money.go`、`money_test.go`、`internal/transport/httpapi/generate.go`、`oapi-codegen.yaml`、`internal/transport/httpapi/middleware/{requestid.go,requestid_test.go,cors.go,cors_test.go,recover.go,logging.go}`、`internal/transport/httpapi/{errors.go,errors_test.go,handler.go,metadata.go,health.go,router.go,router_test.go,server.go}`、`internal/bootstrap/api.go`
- Generated: `internal/transport/httpapi/generated/api.gen.go`

**Interfaces:**
- Consumes: `generated.StrictServerInterface`（GetMetadata）、`generated.Metadata`、`config.Config`、`pgcore.NewPool`、`db.LatestVersion`。
- Produces: `metadata.Service.Get() metadata.Metadata`（21 币种）；`money.Canonicalize(amount string, minorUnits int) (string, error)` 与错误 `money.ErrInvalidFormat`、`money.ErrScaleExceeded`、`money.ErrNegative`；`httpapi.NewRouter(Deps{Logger, Metadata, Readiness, CORSOrigins}) http.Handler`；`httpapi.Readiness` 接口 `Check(ctx) error`；`httpapi.WriteProblem(w, r, status int, code, title, detail string)`；`middleware.RequestIDFrom(ctx) string`。

- [x] **Step 1: 测试先行**：money 用 fixtures 驱动；metadata 断言 21 项与小数位；requestid 断言透传与生成；cors 断言允许来源与预检；errors 断言 problem+json 头与字段；router 用 httptest 断言 `/health/live` 200、`/api/v1/metadata` 200 且 `X-Request-ID` 存在、未知路径 404 RESOURCE_NOT_FOUND、错误方法 405 METHOD_NOT_ALLOWED。
- [x] **Step 2: `go generate ./internal/transport/httpapi/`**（`go tool oapi-codegen -config oapi-codegen.yaml ../../../../packages/contracts/dist/openapi.v1.yaml`）。
- [x] **Step 3: 实现并使全部测试通过**：`go test ./... -race`。
- [x] **Step 4: bootstrap.RunAPI**：连接池、River 仅插入客户端、路由、`server.go` 超时与优雅关闭。

### Task 6: 后台任务：River worker 骨架

**Files:**
- Create: `internal/adapters/queue/river.go`、`internal/adapters/queue/jobs.go`、`internal/transport/river/ping.go`、`internal/transport/river/register.go`、`internal/bootstrap/worker.go`

**Interfaces:**
- Produces: `queue.PingArgs{Message string}`（Kind "ping"）；`queue.NewInsertOnlyClient(pool, logger) (*river.Client[pgx.Tx], error)`；`queue.NewWorkerClient(pool, logger, workers) (*river.Client[pgx.Tx], error)`；`riverjobs.RegisterWorkers(workers *river.Workers, logger *slog.Logger)`；`bootstrap.RunWorker(ctx, cfg)`。

- [x] **Step 1: 写 PingArgs、PingWorker（记录日志）、注册函数、客户端构造。**
- [x] **Step 2: `go build ./... && go vet ./...`**

### Task 7: 集成测试与本地基础设施

**Files:**
- Create: `infra/compose.yaml`、`infra/caddy/Caddyfile`、`infra/README.md`、`apps/server/tests/integration/migrate_test.go`、`apps/server/Dockerfile`

**Interfaces:**
- Consumes: `bootstrap.RunMigrate`、`dbgen`、`queue.NewInsertOnlyClient`。
- Produces: 环境变量 `TRIPFOLIO_TEST_DATABASE_URL`（缺省跳过）；Compose 服务 `postgres`（postgres:18-alpine，端口 5432，用户/密码/库 tripfolio），profile `edge` 下的 `caddy`。

- [x] **Step 1: 写集成测试**：迁移两次幂等；`accounts`、`account_sync_state`、`river_job` 表存在；用 dbgen 插入并读回账号；用 River 事务入队 PingArgs 后回滚，`river_job` 计数不变。
- [x] **Step 2: 写 compose.yaml、Caddyfile（/api、/health 反代；/_AMapService 三段代理追加 jscode；SPA 回退）、Dockerfile（golang:1.27-alpine 构建，alpine 运行，非 root）。**
- [x] **Step 3: 运行** `docker compose -f infra/compose.yaml up -d postgres`，`go run ./cmd/migrate up`，`TRIPFOLIO_TEST_DATABASE_URL=... go test ./tests/... -run Integration -v`；启动 `go run ./cmd/api`，`curl /health/live`、`/health/ready`、`/api/v1/metadata`。

### Task 8: Vue 前端骨架与 Capacitor

**Files:**
- Create: `apps/client/package.json`、`vite.config.ts`、`index.html`、`tsconfig.json`、`tsconfig.app.json`、`tsconfig.node.json`、`capacitor.config.ts`、`.prettierrc`、`.env.development`、`src/main.ts`、`src/App.vue`、`src/env.d.ts`、`src/style.css`、`src/router/index.ts`、`src/router/routes.ts`、`src/shell/pickShell.ts`、`src/shell/pickShell.spec.ts`、`src/desktop/DesktopShell.vue`、`src/desktop/pages/TripListPage.vue`、`src/mobile/MobileShell.vue`、`src/mobile/pages/TripListPage.vue`、`src/shared/api/client.ts`、`src/shared/stores/session.ts`、`src/shared/stores/metadata.ts`、`src/shared/money.ts`、`src/shared/money.spec.ts`、`src/platform/types.ts`、`src/platform/index.ts`、`src/platform/web/secureStorage.ts`、`src/platform/web/network.ts`、`src/platform/capacitor/secureStorage.ts`、`src/platform/capacitor/network.ts`、`README.md`
- Generated: `apps/client/android/`（`npx cap add android`）

**Interfaces:**
- Consumes: `@tripfolio/contracts/openapi/v1` 的 `paths`；`fixtures/money.json`。
- Produces: `pickShell({ isNative, viewportWidth }): 'desktop' | 'mobile'`（原生或宽度 < 768 → mobile）；`canonicalizeAmount(input, minorUnits): string`（抛 `MoneyError` code SCALE_EXCEEDED / INVALID_FORMAT / NEGATIVE）；`api`（openapi-fetch 客户端，Authorization 注入）；`useSessionStore`（accessToken 内存态）；`useMetadataStore().load()`；`platform.secureStorage.get/set/remove`、`platform.network.isOnline()/onChange(cb)`。

- [x] **Step 1: 测试先行**：pickShell 三个分支；money 用 fixtures 驱动（与 Go 侧同一文件）。
- [x] **Step 2: 写工程配置与源文件**，`pnpm install`，`pnpm --filter @tripfolio/client typecheck && test && build`。
- [x] **Step 3: 检查构建产物**：Element Plus 与 Vant 位于不同 chunk（`dist/assets` 文件名可辨）。
- [x] **Step 4: Capacitor**：`npx cap add android`、`npx cap sync android`；不构建 APK（JDK 21 未安装）。

### Task 9: CI 与收尾

**Files:**
- Create: `.github/workflows/ci.yml`
- Modify: `docs/requirements/2026-09-10-技术选型与架构建议.md`（Capacitor 版本 7.x → 8.x；JDK 21 前置）、`docs/api/2026-09-11-P0接口设计.md`（错误码表补 405 METHOD_NOT_ALLOWED）

- [x] **Step 1: ci.yml**：`server` 作业（Go 1.27.1、PostgreSQL 18 服务、`go vet`、`go test -race`、生成结果 diff）、`contracts-client` 作业（pnpm、contracts build、client typecheck/test/build）、`sqlc` 作业（docker sqlc diff）。
- [x] **Step 2: 文档修正与 `git init`**（不提交，由用户决定首个提交）。
- [x] **Step 3: 全量验证**：`go test ./... -race`、`pnpm -r typecheck test build` 全绿。

---

## 执行记录（2026-09-12）

全部任务已在本机执行并验证：`go build/vet/test`（含集成测试）、`pnpm typecheck/test/build`、契约 lint/bundle/gen、API 与 worker 冒烟、Caddyfile 校验、`cap add android` 与 `cap sync android`。与计划的偏差：

- sqlc 改用官方 Windows 二进制（`.tools/sqlc.exe`，`scripts/sqlc-install.sh` 下载），Docker 作为回退；Docker Hub 直连不可用，镜像经 DaoCloud 镜像站拉取后打回官方标签。
- Capacitor 当前主版本为 8.x（锁定 8.5.2），技术选型文档已同步；SQLite 插件 8.1.1 待"SQLite 数据链路"验证任务再接入。
- `go test -race` 在本机不可用（Windows 无 cgo），由 CI（Linux）执行。
- 契约拆分文件用 Redocly 打包为 `dist/openapi.v1.yaml` 后再喂给两端生成器，`redocly.yaml` 关闭了两条与共享模型库冲突的规则。
- 错误码表补充 405 METHOD_NOT_ALLOWED（传输层）。
- 已 `git init`（main），未提交，首个提交由用户决定。
