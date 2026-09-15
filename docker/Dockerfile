# check=skip=SecretsUsedInArgOrEnv
# 说明：VITE_AMAP_JS_KEY 是高德 JS API 的浏览器公开标识（会打进前端产物），不是密钥，
# 因此以 build-arg 传入；真正的服务端凭证（Web 服务 Key、安全密钥）只通过运行时环境变量注入。
#
# 单二进制镜像：前端产物嵌入 Go 二进制，运行镜像里只有一个进程。
#
# 构建上下文是仓库根目录（需要 pnpm 工作区、apps/client 与 apps/server）。
# 构建参数：
#   VITE_AMAP_JS_KEY        高德 JS API Key（浏览器公开标识），编译期注入前端；改动后必须重新 build
#   VITE_API_BASE_URL       可选：同源部署**不要传**，前端回退到 /api/v1；跨域（Capacitor）才传绝对地址
#   VITE_AMAP_SERVICE_HOST  可选：同源部署不要传，前端回退到 /_AMapService
#   NPM_REGISTRY            大陆可用 https://registry.npmmirror.com
#   GOPROXY                 大陆可用 https://goproxy.cn,direct
#   VERSION                 写进 tripfolio version 的版本号
#
# 可选变量一律用「不传」而不是「传空串」：空串会覆盖前端默认值，
# 历史上这样传 VITE_API_BASE_URL 曾导致容器里接口前缀丢失（客户端已改用 || 兜底并加了回归测试）。

# ---------- 阶段 1：前端产物 ----------
FROM node:24-alpine AS web

ARG NPM_REGISTRY=https://registry.npmjs.org
ARG VITE_AMAP_JS_KEY
ARG VITE_API_BASE_URL
ARG VITE_AMAP_SERVICE_HOST

# corepack 与 npm 走同一个镜像源，否则拉 pnpm 本身会卡住
ENV COREPACK_NPM_REGISTRY=${NPM_REGISTRY} \
    npm_config_registry=${NPM_REGISTRY} \
    CI=1
RUN corepack enable

WORKDIR /src
# 先只拷贝依赖清单，依赖没变时这层命中缓存
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/contracts/package.json packages/contracts/
COPY apps/client/package.json apps/client/
RUN pnpm install --frozen-lockfile

COPY . .
RUN pnpm --filter @tripfolio/contracts build \
 && pnpm --filter @tripfolio/client build

# ---------- 阶段 2：编译后端单二进制 ----------
FROM golang:1.27-alpine AS build

ARG GOPROXY=https://proxy.golang.org,direct
ARG VERSION=docker
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0 GOFLAGS=-trimpath

WORKDIR /src
COPY apps/server/go.mod apps/server/go.sum ./
RUN go mod download
COPY apps/server/ ./
# 前端产物覆盖进 embed 目录：本机残留的旧产物已被 .dockerignore 排除，镜像里只会嵌这一份
COPY --from=web /src/apps/client/dist ./internal/web/dist
RUN go build -ldflags="-s -w -X main.version=${VERSION}" -o /out/tripfolio ./cmd/tripfolio

# ---------- 阶段 3：运行镜像 ----------
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S app && adduser -S -G app -u 10001 app

COPY --from=build /out/tripfolio /app/tripfolio
USER app
WORKDIR /app

# 生产/开发模式由部署方通过 TRIPFOLIO_ENV 决定：http 访问时用 dev（Cookie 不带 Secure），
# 前面有 HTTPS 代理时用 prod（强制 https 站点地址与更严格的必填校验）。
ENV TRIPFOLIO_HTTP_ADDR=:8080
EXPOSE 8080

# 用二进制自带的探针，镜像里不需要额外安装 curl/wget
HEALTHCHECK --interval=20s --timeout=5s --start-period=60s --retries=5 \
  CMD ["/app/tripfolio", "healthcheck"]

ENTRYPOINT ["/app/tripfolio"]
# serve：等待数据库 → 自动迁移 → 启动 HTTP 与后台任务
CMD ["serve"]
