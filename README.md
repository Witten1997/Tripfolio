# Tripfolio

个人旅行档案：规划行程、准备行李、记账统计、保存预订与相册。网页与安卓共用一个 Vue 3 前端工程（安卓由 Capacitor 打包），后端为 Go 模块化单体，数据在 PostgreSQL 与私有对象存储。设计文档在 `docs/`，实施计划在 `docs/superpowers/plans/`。

## 仓库结构

```text
apps/
  client/     Vue 3 前端：桌面壳、移动壳、Capacitor android/；主题体系（--tf-* 令牌、src/themes/）见 docs/architecture/2026-09-13-前端主题体系设计.md
  server/     Go API、worker、migrate
packages/
  contracts/  OpenAPI 契约、生成的 TS 类型、跨语言测试样例
infra/        Docker Compose（PostgreSQL 18、Caddy）、Caddyfile
docs/         需求、架构、接口、数据库设计与评审
```

## 前置条件

| 工具              | 版本         | 说明                                                                                               |
| ----------------- | ------------ | -------------------------------------------------------------------------------------------------- |
| Go                | 1.27.x       | `go.mod` 声明 1.27.1，旧版本会自动下载工具链；大陆先 `go env -w GOPROXY=https://goproxy.cn,direct` |
| Node.js / pnpm    | 24 / 10      | `.node-version`；pnpm 由 `packageManager` 字段锁定                                                 |
| Docker            | 任意近期版本 | 本地 PostgreSQL；Docker Hub 不可直连时经镜像站拉取（见 `infra/README.md`）                         |
| sqlc              | 1.31.1       | `bash apps/server/scripts/sqlc-install.sh` 下载官方二进制到 `.tools/`                              |
| JDK / Android SDK | 21 / 最新    | 只有构建安卓包时需要                                                                               |

## 快速开始

```bash
pnpm install
pnpm --filter @tripfolio/contracts build            # 契约 lint、打包与 TS 类型

docker compose -f infra/compose.yaml up -d postgres minio
docker compose -f infra/compose.yaml run --rm minio-init    # 建私有桶与暂存前缀清理规则；控制台 http://localhost:9001
cp apps/server/.env.example apps/server/.env         # 开发配置，启动时自动读取；也可直接 export 同名变量
(cd apps/server && go run ./cmd/migrate up && go run ./cmd/api)   # http://localhost:8080/api/v1/metadata

pnpm --filter @tripfolio/client dev                 # http://localhost:5173
```

## 验证

```bash
(cd apps/server && go vet ./... && go test ./... -race)          # 设置 TRIPFOLIO_TEST_DATABASE_URL 时含集成测试
pnpm typecheck && pnpm test && pnpm build
pnpm format:check
```

对象存储用例需要额外设置 `TRIPFOLIO_TEST_OBJECTSTORE_ENDPOINT`（开发指向 MinIO 的 `http://localhost:9000`），未设置时自动跳过。高德地点服务需要 `TRIPFOLIO_AMAP_WEB_SERVICE_KEY`，未配置时地点接口返回 503；它与前端 JS API 的安全密钥是两套凭证，后者由 Caddy 在 `/_AMapService` 路径追加。

浏览器直传对象存储需要桶的 CORS 允许 PUT 并暴露 `ETag`。MinIO 不实现按桶 CORS（`PutBucketCors` 返回 NotImplemented），开发环境由 `infra/compose.yaml` 里 minio 服务的 `MINIO_API_CORS_ALLOW_ORIGIN` 全局配置；生产使用 OSS 时在控制台按桶配置。

## 生成代码

契约改动后依次运行 `pnpm --filter @tripfolio/contracts build` 与 `(cd apps/server && go generate ./internal/transport/httpapi/)`；数据库改动后运行 `bash apps/server/scripts/sqlc.sh generate`。生成文件提交入库，CI 重新生成并比对。
