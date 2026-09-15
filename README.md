# Tripfolio

个人旅行档案：规划行程、准备行李、记账统计、保存预订与相册。网页与安卓共用一个 Vue 3 前端工程（安卓由 Capacitor 打包），后端为 Go 模块化单体，数据在 PostgreSQL 与私有对象存储。设计文档在 `docs/`，实施计划在 `docs/superpowers/plans/`。

## 仓库结构

```text
apps/
  client/     Vue 3 前端：桌面壳、移动壳、Capacitor android/；主题体系（--tf-* 令牌、src/themes/）见 docs/architecture/2026-09-13-前端主题体系设计.md
  server/     Go 后端：唯一入口 cmd/tripfolio（HTTP、后台任务、内嵌前端）
packages/
  contracts/  OpenAPI 契约、生成的 TS 类型、跨语言测试样例
infra/        本地开发依赖：Docker Compose（PostgreSQL 18、MinIO）与初始化脚本
docker/       单二进制部署：单服务 compose、镜像 Dockerfile 与部署说明
docs/         需求、架构、接口、数据库设计与评审
```

## 功能

一个账号可以管理多趟旅行，每趟旅行是「行程 + 账单 + 行李 + 待办」的集合，另有一条不登录也能看的分享通道。

**账号与安全**：邮箱验证码注册、登录、找回密码；访问令牌只放内存，刷新令牌走 HttpOnly Cookie（网页）或安全存储（安卓），401 自动刷新一次并重放；账号页可改昵称、上传头像、查看并注销其他会话。

**旅行管理**：新建、编辑、归档与回收站；按旅行时区判断「待出发／旅行中／已结束」；回收站支持恢复与申请永久清理。

**行程与地图**：按天编排行程，同日与跨日拖动排序；高德 POI 搜索、地图点选或坐标输入选点（GCJ-02）；自动计算相邻路段的驾车／步行／骑行距离与时长，跨日接续和缺坐标项目明确标注而不冒充道路里程；独立「地图」页签展示整趟或单日路线、沿途各站与总里程用时，缺省按整趟显示。

**账单与统计**：账目增删改与游标分页；退款必须关联原支出，原支出减额与删除时联动校验；账号级共用分类管理（预设分类可改名排序，被引用时禁止删除）；旅行总预算就地编辑，超支高亮；分类占比环图与每日净支出柱图，图表配色随主题切换重新取色。

**行李与待办**：行李清单按七大类分组、物品库勾选批量加入、待准备／已准备两态；待办支持四态筛选、逾期标记与勾选完成。

**旅行分享**：为旅行生成一条分享链接（可重新生成、可关闭），任何人无需登录即可查看行程与地图；访客页与主人侧页面保持一致（站间距离、沿途各站、日期筛选），只暴露行程骨架，不含备注、实际情况、费用与完成状态；访客没有写入入口，响应带 `X-Robots-Tag: noindex`。

**主题与终端**：`--tf-*` 令牌驱动的主题体系，预置有机自然与玻璃态两套主题，可在主题中心切换并记住偏好；桌面壳用 Element Plus，移动壳用 Vant，同一份前端产物由 Capacitor 打进安卓包。

尚未实现的部分：相册、预订与资料页签仍是占位；数据导出与账号注销在后续切片；安卓真机、完整屏幕阅读器与生产环境尚未验收。进度与遗留问题记录在 `docs/superpowers/plans/2026-09-12-web-p0.md`。

## 部署

单二进制部署：一个可执行文件内含 HTTP 接口、后台任务与前端产物，启动时等待数据库、自动迁移，再对外提供页面与接口；部署物就是二进制加一份 `.env`。

```bash
# 方式一：不用容器。仓库根打包（构建前端 → 复制进 embed 目录 → 交叉编译），默认 linux/amd64
bash scripts/package.sh

# 方式二：容器，编排里只有 app 一个服务
cd docker
cp .env.example .env      # 站点地址、数据库连接串、签名密钥、邮件、OSS 与高德凭证
docker compose up -d --build
```

- 编排（`docker/docker-compose.yaml`）里只有一个 `app` 服务：PostgreSQL 与对象存储都用已有服务（生产对象存储用阿里云 OSS），不在编排里再起数据库或 MinIO。
- 容器启动即自动迁移（`TRIPFOLIO_AUTO_MIGRATE`，默认 `true`）；需要单独跑迁移时用 `tripfolio migrate up|status|down`，容器健康检查用 `tripfolio healthcheck`。
- 编排默认不终结 TLS：直接以 http 访问（内网 IP、个人服务器）时显式设置 `TRIPFOLIO_COOKIE_SECURE=false` 表示确认无 TLS（启动会打印风险告警），否则 `prod` 会因为要求 https 而拒绝启动；需要 HTTPS 时在前面放一层 Nginx / Caddy / 云负载均衡，转发 `/`、`/api/*`、`/health/*` 与 `/_AMapService/*` 到 8080 端口。
- 生产环境（`TRIPFOLIO_ENV=prod`）要求 https 站点地址（纯 http 须显式设置 `TRIPFOLIO_COOKIE_SECURE=false`）、签名密钥与高德 Web 服务 Key；邮件和对象存储可不配置。邮件驱动留空时默认 `disabled`，验证码接口返回 503，已有账号仍可用密码登录；对象存储未配齐时文件授权接口返回 503。配置错误会打印中文排查块（退出码 2 配置、3 数据库、4 迁移、5 装配与任务、6 HTTP 监听）。
- 前端是直传，对象存储端点必须是浏览器能直接访问的地址；OSS 桶要在控制台配置 CORS（允许 `PUT/GET/HEAD`、暴露 `ETag`）。
- 部署到 NAS（群晖等）时不需要在 NAS 上编译：`bash scripts/docker-image.sh` 会构建镜像并导出 tar 与部署包，导入后按 `docker-compose.yaml` 启动。

完整步骤、变量表、NAS 导出与一键上传、启动失败排查、运维与回滚命令见 [docker/README.md](docker/README.md)；进程模型与取舍见 [单二进制部署设计](docs/architecture/2026-09-15-单二进制部署设计.md)。

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
(cd apps/server && go run ./cmd/tripfolio serve)    # http://localhost:8080，启动时自动迁移

pnpm --filter @tripfolio/client dev                 # http://localhost:5173
```

打开 http://localhost:8080 就能看到页面。页面是编译期嵌进二进制的：需要先构建前端并把 `apps/client/dist` 复制进 `apps/server/internal/web/dist/`（`bash scripts/package.sh` 会一并完成，产出 `dist/tripfolio`）；没有嵌入产物时页面会提示「前端产物没有嵌入这个二进制」，接口与分享链接仍可用。日常改前端走 `pnpm --filter @tripfolio/client dev`（http://localhost:5173）的热更新，不必重新编译。

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

本地开发配置两套独立凭证，修改后重启后端与 Vite：

| 文件                     | 变量                             | 用途                                                         |
| ------------------------ | -------------------------------- | ------------------------------------------------------------ |
| `apps/server/.env`       | `TRIPFOLIO_AMAP_WEB_SERVICE_KEY` | Web 服务类型 Key；后端 POI 搜索、逆地理编码与算路            |
| `apps/client/.env.local` | `VITE_AMAP_JS_KEY`               | Web 端（JS API）类型 Key；浏览器公开标识                     |
| `apps/client/.env.local` | `AMAP_JSCODE`                    | JS API 安全密钥；仅 Vite 服务端代理读取，禁止加 `VITE_` 前缀 |

上述本地文件均被 Git 忽略，模板不含真实凭证。JS Key 应在高德控制台限制允许域名；后端 Key 应按部署出口限制访问。未配 Web 服务 Key 时 `/geo` 接口返回 503；未配 JS Key 时仍可搜索并保存地点，但不显示底图。生产构建需要注入 `VITE_AMAP_JS_KEY`；JS API 安全密钥由应用进程内的 `/_AMapService` 代理覆盖进请求（`TRIPFOLIO_AMAP_JSCODE`，本地开发时由 Vite 开发代理追加），API 运行时需要 Web 服务 Key，详见 [docker/README.md](docker/README.md) 与 [infra/README.md 的高德凭证与代理](infra/README.md#高德凭证与代理)。

真实高德冒烟测试仅在显式设置 `TRIPFOLIO_TEST_AMAP_KEY` 后运行：`cd apps/server && go test ./internal/adapters/geo -run TestLiveAmap -count=1 -v`。每次消耗 5 次上游请求，普通测试默认使用桩响应，不消耗高德配额。

## 生成代码

契约改动后依次运行 `pnpm --filter @tripfolio/contracts build` 与 `(cd apps/server && go generate ./internal/transport/httpapi/)`；数据库改动后运行 `bash apps/server/scripts/sqlc.sh generate`。生成文件提交入库，CI 重新生成并比对。
