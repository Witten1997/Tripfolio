# docker

单二进制部署：一个容器跑完整个后端，前端页面已经嵌进同一个可执行文件。

| 文件                  | 用途                                                                 |
| --------------------- | -------------------------------------------------------------------- |
| `docker-compose.yaml` | 只包含一个 `app` 服务；PostgreSQL 与对象存储都用已有服务             |
| `Dockerfile`          | 三阶段：Node 构建前端 → 嵌入前端并编译 Go 单二进制 → alpine 运行镜像 |
| `.env.example`        | 部署变量模板，复制为 `.env` 使用                                     |

进程内部一次做四件事：等待数据库 → 自动迁移 → 提供 HTTP 接口 → 运行后台任务；静态页面、SPA 回退与高德安全密钥代理也在同一进程。设计与取舍见 [单二进制部署设计](../docs/architecture/2026-09-15-单二进制部署设计.md)。

本地开发仍用 `infra/compose.yaml` 起 PostgreSQL 与 MinIO，并用 `pnpm --filter @tripfolio/client dev` 与 `go run ./cmd/tripfolio serve` 跑热更新；本目录只面向部署。

## 前置条件

- Docker 与 Compose v2。
- **已有的 PostgreSQL**：库已创建，账号能建表（启动时会自动迁移，需要 `CREATE TABLE`/`ALTER TABLE`/`CREATE INDEX`）。
- **阿里云 OSS 桶（可选）**：使用文件上传与下载时配置；配好 CORS（见下文），浏览器要能直接访问 OSS 域名。
- **高德两套凭证**：Web 服务 Key（后端用）、JS API Key 与安全密钥（前端与代理用）。
- **SMTP 账号（可选）**：用于注册与找回密码的验证码；生产环境不配置时禁用发信，已有账号仍可用密码登录。
- HTTPS 可选：本编排不终结 TLS。没有 HTTPS 时保留 `prod` 并显式设置 `TRIPFOLIO_COOKIE_SECURE=false`（见下文）；对外服务可在前面放一层 Nginx/Caddy/云负载均衡。

## 首次部署

```bash
cd docker
cp .env.example .env

# 生成签名主密钥，填进 TRIPFOLIO_KEYRING
openssl rand -base64 32

${EDITOR:-vi} .env      # 站点地址、数据库连接串、签名密钥、高德凭证；OSS 与邮件可选
docker compose up -d --build
docker compose logs -f app
```

启动顺序与日志：`正在连接数据库` → `数据库连接成功` → `开始数据库迁移` → `已应用业务迁移 …` → `后台任务已启动` → `HTTP 服务准备就绪`。看到最后一条后访问 `http://<主机>:${TRIPFOLIO_HTTP_PORT:-8080}/`；健康检查用 `docker compose ps`（healthy）或 `curl -fsS http://127.0.0.1:8080/health/ready`。

`prod` 下也可以先不配置 OSS 和邮件：邮件驱动留空或设为 `disabled`，对象存储变量留空即可启动。未启用邮件时，注册与找回密码的验证码接口返回 `503 DEPENDENCY_UNAVAILABLE`，已有账号仍可用密码登录；未配置对象存储时，文件授权接口返回 503。要注册首个账号，请启用 SMTP。

旧 `.env` 如果写了 `TRIPFOLIO_MAIL_DRIVER=smtp`，暂不发信时须改为 `disabled`；显式使用 `smtp` 时仍校验主机与发信地址。应用本次代码修改需重新构建镜像并运行 `docker compose up -d --build`；NAS 部署需重新导出、导入镜像后运行 `docker compose up -d --force-recreate`。只改 `.env` 后运行 `docker compose up -d` 以重建容器并注入新配置。

## 导出镜像并部署到 NAS

NAS 上没有源码与 Node/Go 工具链，因此做法是：**在开发机构建镜像并导出成 tar，导入 NAS 后用只引用镜像的编排启动**（`docker/docker-compose.nas.yaml`，不含 `build`）。

开发机上一条命令完成构建与导出：

```bash
bash scripts/docker-image.sh                 # 版本取当天日期，平台 linux/amd64
bash scripts/docker-image.sh 2026.09.15      # 指定版本号
PLATFORM=linux/arm64 bash scripts/docker-image.sh 2026.09.15   # arm64 的 NAS
```

脚本会尝试从 `apps/client/.env.local` 读取 `VITE_AMAP_JS_KEY`（前端 Key 是编译期注入的，改 Key 必须重新导出）；也可以显式传入 `VITE_AMAP_JS_KEY=... bash scripts/docker-image.sh`。产物在 `dist/` 下：

| 产物                          | 用途                                                       |
| ----------------------------- | ---------------------------------------------------------- |
| `tripfolio-<版本>.tar`        | 镜像本体，NAS 上 `docker load -i` 导入                     |
| `tripfolio-nas-<版本>/`       | 部署包：镜像 tar + compose + `.env.example` + `install.sh` |
| `tripfolio-nas-<版本>.tar.gz` | 部署包压缩版，便于上传                                     |

NAS 上（把部署包解压到例如 `/volume1/docker/tripfolio`）：

```bash
cp .env.example .env      # 填站点地址、已有 PostgreSQL、签名密钥与高德凭证；OSS 与邮件可选
sudo ./install.sh         # 等价于 docker load -i tripfolio-*.tar + docker compose up -d
```

也可以手动执行：`sudo docker load -i tripfolio-<版本>.tar`，再 `sudo docker compose -f docker-compose.yaml up -d`。容器启动即自动迁移，不需要额外步骤。

### 一键上传（可选）

配好 SSH 免密后，可以用 `scripts/docker-deploy-nas.sh` 把「构建 → 导出 → 上传 → 导入 → 重启」串起来：

```bash
cp scripts/.deploy.env.example scripts/.deploy.env   # 填 NAS 地址、账号与部署目录（该文件不入库）
bash scripts/docker-deploy-nas.sh 2026.09.15
```

首次运行时脚本会放置 compose 与 `.env.example` 并提示你登录 NAS 填写 `.env`（密钥不经过脚本传输），填好后再执行一次即启动。

### NAS 上的注意事项

- **架构**：x86_64 的 NAS 用默认的 `linux/amd64`；arm64 的 NAS 用 `PLATFORM=linux/arm64`，且构建机需要能拉取该架构的基础镜像（`golang`、`node`、`alpine`）。
- **主机名**：数据库或对象存储跑在 NAS 本机（或同一台 NAS 的另一个容器）时，`host.docker.internal` 在部分群晖版本不可用，直接把 `.env` 里的主机名写成 NAS 的局域网 IP。
- **端口**：默认映射 `8080`，与 DSM 的 5000/5001 通常不冲突；若被占用，改 `.env` 里的 `TRIPFOLIO_HTTP_PORT`。
- **升级与回滚**：把新的 tar 与 compose 传到同一目录，`sudo docker load -i 新 tar` 后 `sudo docker compose up -d`；回滚就是 `docker load` 旧 tar 并把 compose 的 `image:` 行改回旧版本号。
- **数据库备份**：NAS 上的 PostgreSQL 用你自己的备份方式（Container Manager 的计划任务或 `pg_dump`），对象存储数据在 OSS 侧。

## 配置变量

| 变量                             | 说明                                                                      |
| -------------------------------- | ------------------------------------------------------------------------- |
| `TRIPFOLIO_ENV`                  | 部署用 `prod`，本地开发用 `dev`；纯 http 部署须显式关闭 Secure Cookie     |
| `TRIPFOLIO_DATABASE_URL`         | 已有 PostgreSQL 的连接串；宿主机上的库用 `host.docker.internal`           |
| `TRIPFOLIO_KEYRING`              | `k1=<openssl rand -base64 32>`；丢失会让所有会话失效，多副本必须一致      |
| `TRIPFOLIO_WEB_BASE_URL`         | 站点根地址，`prod` 必须是 https；用于拼分享链接                           |
| `TRIPFOLIO_CORS_ORIGINS`         | 网页域 + `https://localhost`（安卓包来源）                                |
| `TRIPFOLIO_MAIL_*`               | 可选；`prod` 下驱动留空默认 `disabled`，启用邮件设为 `smtp`，不允许 `log` |
| `TRIPFOLIO_OBJECTSTORE_*`        | 可选；使用 OSS 时配齐端点、桶、AK/SK，`USE_PATH_STYLE=false`              |
| `TRIPFOLIO_AMAP_WEB_SERVICE_KEY` | 后端高德 Web 服务 Key（`prod` 必填）                                      |
| `TRIPFOLIO_AMAP_JSCODE`          | JS API 安全密钥，供 `/_AMapService` 代理使用                              |
| `VITE_AMAP_JS_KEY`               | 前端 JS API Key，**编译期**注入；改了要重新 `docker compose build`        |
| `TRIPFOLIO_COOKIE_SECURE`        | 可选：`prod` + http 部署时必须显式设为 `false`；有 HTTPS 时不要设置       |

`TRIPFOLIO_AUTO_MIGRATE`（默认 `true`）控制启动时是否自动迁移；设为 `false` 时启动会核对结构版本，落后就拒绝启动而不是带着旧结构运行。`TRIPFOLIO_STARTUP_DB_TIMEOUT`（默认 `60s`）是等待数据库可达的上限。

## 邮件配置从哪里获取

向所选邮件服务商获取 SMTP 配置。模板以阿里云邮件推送为例：在控制台验证发信域名、按提示配置 DNS 记录、创建发信地址，并设置 SMTP 密码。主机和端口以控制台对应地域的说明为准。

| 变量                           | 获取位置或填写方式                                                            |
| ------------------------------ | ----------------------------------------------------------------------------- |
| `TRIPFOLIO_MAIL_DRIVER`        | 启用邮件填 `smtp`；暂不使用填 `disabled`                                      |
| `TRIPFOLIO_MAIL_SMTP_HOST`     | 服务商提供的 SMTP 主机；模板示例为 `smtpdm.aliyun.com`                        |
| `TRIPFOLIO_MAIL_SMTP_PORT`     | 模板使用隐式 TLS 端口 `465`，同时设置 `TRIPFOLIO_MAIL_SMTP_IMPLICIT_TLS=true` |
| `TRIPFOLIO_MAIL_SMTP_USERNAME` | 控制台提供的 SMTP 用户名，通常为发信地址                                      |
| `TRIPFOLIO_MAIL_SMTP_PASSWORD` | 单独设置的 SMTP 密码或邮箱授权码，不是云账号登录密码或 AccessKey              |
| `TRIPFOLIO_MAIL_FROM`          | 已验证并获准发信的邮箱地址                                                    |
| `TRIPFOLIO_MAIL_FROM_NAME`     | 收件人看到的发信人名称，可填 `Tripfolio`                                      |

## 阿里云 OSS

1. 创建私有 Bucket（读权限设为私有，所有访问都走服务端签发的短期授权）。
2. 跨域设置（CORS）：
   - 来源：`https://你的网页域`（Capacitor 打包调试时再加 `https://localhost`）
   - 允许方法：`PUT`、`GET`、`HEAD`
   - 暴露 Header：`ETag`（前端用 ETag 校验直传完整性，缺了会静默拿不到）
3. `TRIPFOLIO_OBJECTSTORE_ENDPOINT` 填 OSS 端点（如 `https://oss-cn-hangzhou.aliyuncs.com`），`REGION` 填如 `cn-hangzhou`，`USE_PATH_STYLE=false`。

OSS 端点本身是公网地址，浏览器可以直传，不需要额外暴露端口。

## 没有 HTTPS 时

本编排不提供证书。直接以 http 访问（内网 IP、个人服务器）时，推荐**保留 `prod` 并显式确认**：

```ini
TRIPFOLIO_ENV=prod
TRIPFOLIO_WEB_BASE_URL=http://192.168.1.10:8080
TRIPFOLIO_CORS_ORIGINS=http://192.168.1.10:8080,https://localhost
TRIPFOLIO_COOKIE_SECURE=false      # 显式声明「这套部署没有 TLS」
```

这样仍保留 prod 的严格校验（签名密钥与高德 Web 服务 key 必填，邮件可禁用，启用时不得用 log），启动时会打印一条风险告警：分享链接是 http、刷新 Cookie 不再带 `Secure`。如果不写这一行，启动会以配置错误拒绝——因为 `prod` 默认要求 https 且 Cookie 带 `Secure`，而浏览器在 http 下不会回传这种 Cookie，登录会表现为「登录后又变回未登录」。

另一种做法是把 `TRIPFOLIO_ENV` 设为 `dev`：它同样能跑 http，但会放宽若干校验（邮件允许 `log` 驱动、签名密钥缺失只告警、对象存储与高德 key 缺失也不阻塞），启动日志里会有对应提醒；适合本地或内网试跑，不建议用于对外服务。

需要 HTTPS 时在前面加一层反向代理（Nginx / Caddy / 云 LB），把 `/`、`/api/*`、`/health/*`、`/_AMapService/*` 转发到 `127.0.0.1:8080`，然后把 `TRIPFOLIO_WEB_BASE_URL` 改成 https 域名并去掉 `TRIPFOLIO_COOKIE_SECURE` 那一行。

## 启动失败排查

启动失败时容器会打印一个中文排查块（阶段、原因、逐条建议），并按阶段返回退出码：

| 退出码 | 阶段       | 常见原因与处理                                                                                                                                                                       |
| ------ | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 2      | 读取配置   | 变量缺失或格式错误，报错会点名变量。`prod` 要求 https 站点地址（或显式 `TRIPFOLIO_COOKIE_SECURE=false`）、签名密钥与高德 key；邮件和对象存储可不配置，显式选择 smtp 时仍校验邮件配置 |
| 3      | 连接数据库 | `host.docker.internal` 没写对、库没建、密码错、安全组不放行、外部库启动慢（调大 `TRIPFOLIO_STARTUP_DB_TIMEOUT`）；密码含 `@ : / ? #` 要按 URL 编码                                   |
| 4      | 数据库迁移 | 账号没有建表权限、另一个进程正在迁移（会话锁）、某个迁移语句报错（日志里有版本号，对应 `db/migrations/` 下同名文件）                                                                 |
| 5      | 装配与任务 | 对象存储或高德客户端创建失败、`TRIPFOLIO_KEYRING` 不是合法 base64                                                                                                                    |
| 6      | HTTP 监听  | 端口被占用、`TRIPFOLIO_HTTP_ADDR` 与端口映射不一致                                                                                                                                   |
| 1      | 其他       | 见原因行                                                                                                                                                                             |

常用命令：

```bash
docker compose logs --tail=80 app                       # 看启动日志与排查块
docker compose exec app /app/tripfolio migrate status   # 迁移到哪一版了
docker compose exec app /app/tripfolio healthcheck      # 手动跑一次就绪探针
docker compose config                                   # 检查变量是否传进容器（输出含凭证，不要外传）
docker compose up -d --force-recreate                   # 改完 .env 后重建容器
```

几类具体症状：

- **页面能开但接口 401／一直未登录**：多半是 `prod` + http 的组合，见上一节。
- **页面提示「前端产物没有嵌入这个二进制」**：镜像构建时前端阶段失败或用了旧镜像，重新 `docker compose build --no-cache`。
- **地图空白、控制台报 `_AMapService` 失败**：`TRIPFOLIO_AMAP_JSCODE` 未配，或 `VITE_AMAP_JS_KEY` 没在构建期传入（改 Key 必须重新 build）。
- **上传失败、浏览器报 CORS**：OSS 桶的 CORS 少了 `PUT`/`ETag`，或来源域名不匹配。
- **日志出现 `版本 N 低于程序需要的 M`**：数据库结构落后，说明设了 `TRIPFOLIO_AUTO_MIGRATE=false` 又没手工迁移；执行 `docker compose exec app /app/tripfolio migrate up`。

## 运维

```bash
docker compose up -d --build            # 发布／更新（启动时自动迁移）
docker compose restart app              # 重启
docker compose down                     # 停止（外部数据库与 OSS 不受影响）

TRIPFOLIO_IMAGE_TAG=2026-09-15 docker compose up -d --build   # 发布打版本标签
TRIPFOLIO_IMAGE_TAG=2026-09-01 docker compose up -d           # 回滚到旧标签（不重建）
```

迁移只前进不回退：`migrate down` 仅供开发。回滚镜像前先确认新版迁移是向后兼容的（例如只新增列）。

数据库备份在已有实例上做，例如：

```bash
pg_dump "postgres://tripfolio:密码@主机:5432/tripfolio" | gzip > tripfolio-$(date +%F).sql.gz
```

## 不用 Docker 也能跑

二进制是自包含的（前端已嵌入），拷到服务器配一份 `.env` 即可：

```bash
bash scripts/package.sh                      # 仓库根打包，产出 dist/tripfolio
scp dist/tripfolio server:/opt/tripfolio/
# 服务器上：/opt/tripfolio/.env 填好变量后
/opt/tripfolio/tripfolio serve
```

systemd 单元示例：

```ini
[Unit]
Description=Tripfolio
After=network-online.target

[Service]
WorkingDirectory=/opt/tripfolio
ExecStart=/opt/tripfolio/tripfolio serve
Restart=always
RestartSec=3
User=tripfolio

[Install]
WantedBy=multi-user.target
```

## 限制

- 单副本假设：分享限流与高德缓存是进程内状态，扩容前需改为共享存储。
- 高德配额是账号级免费额度，`TRIPFOLIO_GEO_GLOBAL_DAILY_LIMIT` 只是单进程保护，重启会重置。
- 容器不终结 TLS，也不做 WAF／灰度：这些交给外层代理。
