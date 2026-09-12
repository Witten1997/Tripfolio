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

## 待补充

备份与恢复演练、对象存储桶的 CORS 与生命周期规则、阿里云邮件推送的域名记录，按技术选型第 8 节在部署设计时补入。
