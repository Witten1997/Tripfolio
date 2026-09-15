# infra

本地开发与部署配置。

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
go run ./cmd/migrate up
go run ./cmd/api
go run ./cmd/worker
```

## 完整栈（可选）

`app` profile 构建并运行 API 与 worker 容器，`edge` profile 运行 Caddy（反代、静态站点、高德安全密钥代理）：

```bash
docker compose -f infra/compose.yaml --profile app --profile edge up -d --build
```

Caddy 需要环境变量 `AMAP_JSCODE`（高德 JS API 安全密钥）。生产部署时把 `:80` 换成站点域名以启用自动 HTTPS，并把 CORS 允许来源改为正式网页域与 Capacitor 来源。

## 高德凭证与代理

三项配置须分层提供，不要把 Web 服务 Key 或 JS 安全密钥注入浏览器构建：

- 前端构建环境：`VITE_AMAP_JS_KEY`；网页同源代理无需设 `VITE_AMAP_SERVICE_HOST`，Capacitor 则必须设 HTTPS 代理完整路径。
- API 运行环境：`TRIPFOLIO_AMAP_WEB_SERVICE_KEY`，用于已认证的 `/api/v1/geo/places`、`reverse-geocode`、`routes`。
- Caddy 运行环境：`AMAP_JSCODE`，由 `/_AMapService` 代理追加。样式、矢量图、REST 三类请求分别转发到高德官方固定域名。

Compose 已透传 Web 服务 Key、账号／全局配额和缓存／超时变量到 API，安全密钥只传给 Caddy。Compose 默认不会读取 `apps/server/.env` 或 `apps/client/.env.local`；在本地使用已配置文件时，从仓库根显式指定：

```bash
pnpm --filter @tripfolio/client build
docker compose --env-file apps/server/.env --env-file apps/client/.env.local -f infra/compose.yaml --profile app --profile edge up -d --build
```

该完整栈命令会启动／更新 API、worker、Caddy；已有宿主机 8080 服务时先自行协调端口。正式部署改由部署平台注入相同变量。不要公开 `docker compose config`、容器环境转储或完整上游请求日志，其中可能包含凭证。

`TRIPFOLIO_GEO_PER_ACCOUNT_PER_MINUTE` 默认 60；`TRIPFOLIO_GEO_GLOBAL_DAILY_LIMIT` 默认 0（不限制），可按实际高德套餐设置保守日额度。额度与缓存当前为单 API 进程内状态，重启会重置，多副本部署需改为共享限额。逆地理编码地址缓存默认 24 小时；路线缓存 5 分钟。不可将个人套餐额度理解成服务端已实现的全月配额保证。

验证 Caddy 配置（不启动整栈）：

```bash
docker compose -f infra/compose.yaml --profile app --profile edge run --rm --no-deps caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
```

## 待补充

备份与恢复演练、对象存储桶的 CORS 与生命周期规则、阿里云邮件推送的域名记录，按技术选型第 8 节在部署设计时补入。
