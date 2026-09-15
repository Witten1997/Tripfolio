# Tripfolio 项目约定

个人旅行档案应用：Go 后端（apps/server）、一个 Vue 3 前端工程同时产出网页与 Capacitor 安卓应用（apps/client）、OpenAPI 契约（packages/contracts）。设计文档在 `docs/`，是实现的依据；文档与代码冲突时先改文档再改代码。

## 会话工具约定（本机环境）

- 内置 WebSearch 持续返回 429，内置 WebFetch 对所有域名被拦截，都不可用。查资料统一用 searchix MCP：`search_proxy_serper_search`（Google）、`search_proxy_tavily_search`、`search_proxy_tavily_extract`（抓取网页正文，可替代 WebFetch）；exa 通道偶尔无容量，失败就换 serper。
- 本机没有 C 编译器：`go test -race` 不可用（交给 CI），sqlc 用 `.tools/sqlc.exe`（`bash apps/server/scripts/sqlc-install.sh` 下载）。
- Go 模块代理已全局设为 goproxy.cn；Docker Hub 直连不通，镜像经 `docker.m.daocloud.io` 拉取后 `docker tag` 回官方标签。
- JDK 17 与 Android SDK 已装，Capacitor 8 构建安卓包需要 JDK 21，尚未安装；不要在本机尝试构建 APK。
- Git Bash 下给 Docker 挂载路径用 `pwd -W` 并设 `MSYS_NO_PATHCONV=1`。

## 常用命令

```bash
pnpm install && pnpm --filter @tripfolio/contracts build       # 契约 lint、打包、TS 类型
docker compose -f infra/compose.yaml up -d postgres             # PostgreSQL 18，含 tripfolio_test 测试库
cd apps/server && go run ./cmd/tripfolio serve                 # http://localhost:8080，启动时自动迁移
go generate ./internal/transport/httpapi/ && bash scripts/sqlc.sh generate   # 契约或 SQL 改动后
go vet ./... && TRIPFOLIO_TEST_DATABASE_URL=postgres://tripfolio:tripfolio@localhost:5432/tripfolio_test?sslmode=disable go test ./... -count=1
pnpm typecheck && pnpm test && pnpm build && pnpm format:check
```

## 代码约定

- 后端分层与依赖方向见 `docs/architecture/2026-09-11-后端代码层级结构设计.md`：modules 不导入 transport、adapters、workflows；生成代码提交入库且不手改。
- 后端只有一个入口 `cmd/tripfolio`（`serve`／`migrate`／`healthcheck`／`version`），前端产物通过 `go:embed` 嵌进二进制，改动前端要重新打包，见 `docs/architecture/2026-09-15-单二进制部署设计.md`。
- 错误响应统一 problem+json，代码只在接口设计 1.4 登记；写接口带 Idempotency-Key 与 If-Match，可同步写走统一写事务（账号锁、收据、变更日志）。
- 金额十进制字符串按币种小数位规范化；坐标 GCJ-02；时间列 timestamptz，当地时间 timestamp(0)。
- 前端：桌面壳 Element Plus、移动壳 Vant，页面只依赖 `src/platform` 的接口访问平台能力；访问令牌只放内存。
- 前端样式只引用 `--tf-*` 主题令牌，禁止字面颜色与 `--el-*`/`--van-*`（样式契约测试强制）；全站配色禁止蓝紫色；新增主题只加 `src/themes/<id>/` 与注册表条目，见 `docs/architecture/2026-09-13-前端主题体系设计.md`。

## Git 提交规范

- 提交信息必须使用中文，主要分为新增、优化、修复三类，每条内容单独一行，不留空白行，每类可多条。
- 格式为 `【新增】：xxx；`、`【优化】：xxx；`、`【修复】：xxx；`，按实际改动选择类别。
- 提交前确保 gofmt、vet、测试、typecheck、Prettier 全部通过。
- 提交源码、测试、文档、依赖锁文件、数据库迁移、Android 原生工程与 Gradle Wrapper；契约打包文件 `packages/contracts/dist/openapi.v1.yaml` 以及 Go / TypeScript 生成源码也需入库，供 CI 检查一致性。
- 环境变量模板 `.env.example`、`.env.*.example` 与仅含公开默认值的 `apps/client/.env.development` 可以提交；本地环境变量、密钥、日志、数据库文件、依赖目录、`.tools/`、编辑器本机配置和构建产物按 `.gitignore` 过滤。
- 不要主动 git commit / push，由用户决定。

## 文档索引

- 需求：`docs/requirements/2026-09-10-P0核心需求.md`（v0.4）
- 技术选型：`docs/requirements/2026-09-10-技术选型与架构建议.md`（v0.4）
- 接口：`docs/api/2026-09-11-P0接口设计.md`（v0.4，含 2026-09-14 算路补充与 2026-09-15 分享共 96 个接口）
- 数据库：`docs/database/2026-09-11-P0数据库表结构设计.md`（v0.5，24 张表，支持下限 PostgreSQL 13）
- 总览与同步规则：`docs/architecture/2026-09-11-P0接口与数据设计总览.md`
- 评审与决策记录：`docs/reviews/2026-09-12-P0设计评审.md`
- 部署设计：`docs/architecture/2026-09-15-单二进制部署设计.md`（单二进制：内嵌前端、进程内迁移与高德代理、启动退出码）
- 部署：`docker/README.md`（单二进制部署：单个 app 服务、已有 PostgreSQL 与阿里云 OSS、变量表与运维命令；本地开发依赖仍走 `infra/compose.yaml`）
- 实施计划：`docs/superpowers/plans/`。**当前进行中：`docs/superpowers/plans/2026-09-12-web-p0.md`，接手任何工作前先读文件顶部“接手须知”与文末“进度日志”，任务状态用 `[ ]`/`[~]`/`[x]` 标记，开始前改 `[~]`，完成后改 `[x]` 并追加日志。**
