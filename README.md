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

对象存储用例需要额外设置 `TRIPFOLIO_TEST_OBJECTSTORE_ENDPOINT`（开发指向 MinIO 的 `http://localhost:9000`），未设置时自动跳过。

浏览器直传对象存储需要桶的 CORS 允许 PUT 并暴露 `ETag`。MinIO 不实现按桶 CORS（`PutBucketCors` 返回 NotImplemented），开发环境由 `infra/compose.yaml` 里 minio 服务的 `MINIO_API_CORS_ALLOW_ORIGIN` 全局配置；生产使用 OSS 时在控制台按桶配置。

## 高德地图

每日行程新建／编辑支持搜索高德兴趣点、地图点击或输入 GCJ-02 坐标选点。保存两个及以上地点后，自动显示相邻路段的驾车、步行或骑行距离与预计时长。旅行“地图”页签和账单统计区的“地图视图”入口展示整趟路线，也可筛选单日；缺坐标或无可通行路线会明确提示，不用直线距离冒充道路里程。

本地开发配置两套独立凭证，修改后重启 API 与 Vite：

| 文件                     | 变量                             | 用途                                                         |
| ------------------------ | -------------------------------- | ------------------------------------------------------------ |
| `apps/server/.env`       | `TRIPFOLIO_AMAP_WEB_SERVICE_KEY` | Web 服务类型 Key；后端 POI 搜索、逆地理编码与算路            |
| `apps/client/.env.local` | `VITE_AMAP_JS_KEY`               | Web 端（JS API）类型 Key；浏览器公开标识                     |
| `apps/client/.env.local` | `AMAP_JSCODE`                    | JS API 安全密钥；仅 Vite 服务端代理读取，禁止加 `VITE_` 前缀 |

上述本地文件均被 Git 忽略，模板不含真实凭证。JS Key 应在高德控制台限制允许域名；后端 Key 应按部署出口限制访问。未配 Web 服务 Key 时 `/geo` 接口返回 503；未配 JS Key 时仍可搜索并保存地点，但不显示底图。生产构建需要注入 `VITE_AMAP_JS_KEY`，Caddy 运行时需要 `AMAP_JSCODE`，API 运行时需要 Web 服务 Key，详见 [部署说明](infra/README.md#高德凭证与代理)。

真实高德冒烟测试仅在显式设置 `TRIPFOLIO_TEST_AMAP_KEY` 后运行：`cd apps/server && go test ./internal/adapters/geo -run TestLiveAmap -count=1 -v`。每次消耗 5 次上游请求，普通测试默认使用桩响应，不消耗高德配额。

## 生成代码

契约改动后依次运行 `pnpm --filter @tripfolio/contracts build` 与 `(cd apps/server && go generate ./internal/transport/httpapi/)`；数据库改动后运行 `bash apps/server/scripts/sqlc.sh generate`。生成文件提交入库，CI 重新生成并比对。
