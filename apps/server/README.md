# apps/server

Go 后端：一个 Go Module，唯一入口 `cmd/tripfolio`（HTTP 接口、后台任务与内嵌前端同进程运行），目录职责见 `docs/architecture/2026-09-11-后端代码层级结构设计.md`，进程模型与部署见 `docs/architecture/2026-09-15-单二进制部署设计.md`。

## 前置条件

- Go 1.27.x。`go.mod` 声明 `go 1.27.1`，本机 Go 较旧时会自动下载对应工具链；大陆环境先执行 `go env -w GOPROXY=https://goproxy.cn,direct`。
- PostgreSQL 18：`docker compose -f ../../infra/compose.yaml up -d postgres`。
- sqlc 1.31.1 官方二进制：`bash scripts/sqlc-install.sh`（下载到仓库根 `.tools/`）。本机没有 C 编译器，不能通过 `go tool` 构建 sqlc。
- oapi-codegen 通过 `go.mod` 的 `tool` 指令锁定版本，`go generate` 时按需构建。

## 常用命令

```bash
# 环境变量（或复制 .env.example 为 .env 后用工具加载）
export TRIPFOLIO_DATABASE_URL=postgres://tripfolio:tripfolio@localhost:5432/tripfolio?sslmode=disable
export TRIPFOLIO_TEST_DATABASE_URL=postgres://tripfolio:tripfolio@localhost:5432/tripfolio_test?sslmode=disable

go run ./cmd/tripfolio serve              # http://localhost:8080/health/ready、/api/v1/metadata，启动时自动迁移
go run ./cmd/tripfolio migrate up         # 只跑迁移：先 River 自有表，再业务迁移
go run ./cmd/tripfolio migrate status     # 查看迁移状态
go run ./cmd/tripfolio migrate down       # 回退最近一次业务迁移，仅开发环境
go run ./cmd/tripfolio healthcheck        # 就绪探针，供容器健康检查使用

go test ./... -count=1        # 单元测试；设置了 TRIPFOLIO_TEST_DATABASE_URL 时包含集成测试
go vet ./...
```

`go test -race` 需要 cgo，本机（Windows，无 C 编译器）不可用，由 CI 在 Linux 上执行。

## 生成代码

| 来源                                                                                       | 命令                                        | 输出                                              |
| ------------------------------------------------------------------------------------------ | ------------------------------------------- | ------------------------------------------------- |
| `packages/contracts/dist/openapi.v1.yaml`（先 `pnpm --filter @tripfolio/contracts build`） | `go generate ./internal/transport/httpapi/` | `internal/transport/httpapi/generated/api.gen.go` |
| `db/migrations/*.sql` + `db/queries/**/*.sql`                                              | `bash scripts/sqlc.sh generate`             | `internal/adapters/postgres/dbgen/`               |

生成文件提交入库；CI 重新生成并比对，不手工修改。

## 目录

```text
cmd/tripfolio/     唯一入口：serve（等待数据库 → 自动迁移 → HTTP 与后台任务）、migrate、healthcheck、version
db/                迁移（Goose）与查询（sqlc）的手工来源，以及嵌入声明
internal/
  bootstrap/       装配与生命周期
  config/          环境变量配置
  transport/       httpapi（chi + oapi-codegen strict server）、river（任务处理器）
  web/             内嵌前端产物（go:embed all:dist）与 /_AMapService 高德安全密钥代理
  modules/         业务模块（目前只有 metadata；旅行、账户、文件模块随功能落地）
  workflows/       跨模块协调（sync、datamanagement，随功能落地）
  adapters/        postgres（pgcore、dbgen）、queue（River）、objectstore、mail、security、geo
  foundation/      基础值类型（目前只有 money）
tests/integration/ 需要真实 PostgreSQL 的测试
```

## 约定

- 可同步写事务使用 READ COMMITTED，第一条语句锁定 account_sync_state；连接级超时在 `pgcore.NewPool` 设置。
- 错误响应统一 `application/problem+json`，代码清单见接口设计 1.4；传输层用 `httpapi.WriteProblem`。
- 业务包不导入 transport、adapters、workflows；HTTP 生成代码只被 httpapi 使用，sqlc 生成代码只被 postgres 适配器使用。
