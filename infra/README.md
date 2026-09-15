# infra

本地开发的依赖与调试用配置。**生产／单机部署请用仓库根的 [`docker/`](../docker/README.md)**：那里是单二进制部署（单个应用服务、已有的 PostgreSQL 与阿里云 OSS、变量表与启动失败排查）；本目录的 `app` profile 只用于本机调试。

## 本地数据库

```bash
docker compose -f infra/compose.yaml up -d postgres
```

- 业务库 `tripfolio`，测试库 `tripfolio_test`（由 `postgres/init/01-test-database.sql` 在首次初始化时创建），用户与密码都是 `tripfolio`。
- 连接串：`postgres://tripfolio:tripfolio@localhost:5432/tripfolio?sslmode=disable`。
- Docker Hub 直连不可用时，先经镜像站拉取再打回官方标签：

```bash
docker pull docker.m.daocloud.io/library/postgres:18-alpine
docker tag docker.m.daocloud.io/library/postgres:18-alpine postgres:18-alpine
```

## 迁移与运行

```bash
cd apps/server
export TRIPFOLIO_DATABASE_URL=postgres://tripfolio:tripfolio@localhost:5432/tripfolio?sslmode=disable
go run ./cmd/tripfolio serve              # 等待数据库 → 自动迁移 → 同一进程提供 HTTP 与后台任务
go run ./cmd/tripfolio migrate status     # 只想看迁移到哪一版
```

前端联调走 Vite 开发服务器（`pnpm --filter @tripfolio/client dev`，http://localhost:5173），改前端不需要重新编译二进制。

## 完整栈（可选）

`app` profile 构建并运行单个 `app` 容器：镜像里是单二进制（HTTP、后台任务与内嵌前端同进程），与生产同一形态，只是环境变量指向本机依赖：

```bash
docker compose -f infra/compose.yaml --profile app up -d --build
```

镜像构建时把前端产物嵌进二进制（构建参数 `VITE_AMAP_JS_KEY` 从同名环境变量读入，缺省为空则页面没有底图）；运行期从宿主环境读取 `TRIPFOLIO_KEYRING`、`TRIPFOLIO_AMAP_WEB_SERVICE_KEY` 与高德安全密钥 `AMAP_JSCODE`。容器内固定监听 `:8080` 并映射到宿主机 8080，访问 http://localhost:8080；正式部署见 [`docker/README.md`](../docker/README.md)。

## 高德凭证与代理

三项配置须分层提供，不要把 Web 服务 Key 或 JS 安全密钥注入浏览器构建：

- 前端构建环境：`VITE_AMAP_JS_KEY`；网页同源代理无需设 `VITE_AMAP_SERVICE_HOST`，Capacitor 则必须设 HTTPS 代理完整路径。
- API 运行环境：`TRIPFOLIO_AMAP_WEB_SERVICE_KEY`，用于已认证的 `/api/v1/geo/places`、`reverse-geocode`、`routes`。
- API 运行环境：`TRIPFOLIO_WEB_BASE_URL`，网页站点根地址，服务端用它拼出旅行分享链接；生产必须是正式域名的 https 地址。
- 应用运行环境：`TRIPFOLIO_AMAP_JSCODE`，由进程内的 `/_AMapService` 代理覆盖请求中的 `jscode`（本地不使用容器时由 Vite 开发代理追加）。样式、矢量图、REST 三类请求分别转发到高德官方固定域名。

Compose 已把 Web 服务 Key、账号／全局配额和缓存／超时变量透传给 `app` 容器，高德安全密钥按 `AMAP_JSCODE` → `TRIPFOLIO_AMAP_JSCODE` 传入。Compose 默认不会读取 `apps/server/.env` 或 `apps/client/.env.local`；在本地使用已配置文件时，从仓库根显式指定：

```bash
docker compose --env-file apps/server/.env --env-file apps/client/.env.local -f infra/compose.yaml --profile app up -d --build
```

其中 `VITE_AMAP_JS_KEY` 是镜像构建期的构建参数，改了 Key 要重新 `--build`。该命令会启动／更新单个 `app` 容器；已有宿主机 8080 服务时先自行协调端口。正式部署改由部署平台注入相同变量。不要公开 `docker compose config`、容器环境转储或完整上游请求日志，其中可能包含凭证。

`TRIPFOLIO_GEO_PER_ACCOUNT_PER_MINUTE` 默认 60；`TRIPFOLIO_GEO_GLOBAL_DAILY_LIMIT` 默认 0（不限制），可按实际高德套餐设置保守日额度。额度与缓存当前是单进程内状态（HTTP 与后台任务同进程），重启会重置，多副本部署需改为共享限额。逆地理编码地址缓存默认 24 小时；路线缓存 5 分钟。不可将个人套餐额度理解成服务端已实现的全月配额保证。

## 待补充

备份与恢复演练、对象存储桶的 CORS 与生命周期规则、阿里云邮件推送的域名记录，按技术选型第 8 节在部署设计时补入。
