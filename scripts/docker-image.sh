#!/usr/bin/env bash
# 构建单二进制镜像并导出，供离线导入 NAS 部署。
#
# 用法：
#   bash scripts/docker-image.sh                        # 版本取当天日期，平台 linux/amd64
#   bash scripts/docker-image.sh 2026.09.15             # 指定版本号
#   PLATFORM=linux/arm64 bash scripts/docker-image.sh   # arm64 的 NAS（需要能拉取对应架构的基础镜像）
#   VITE_AMAP_JS_KEY=xxx bash scripts/docker-image.sh   # 不设则尝试从 apps/client/.env.local 读取
#
# 产物（默认在 dist/ 下）：
#   tripfolio-<版本>.tar              单文件镜像，NAS 上 docker load -i 导入
#   tripfolio-nas-<版本>/             部署包：镜像 tar + compose + .env 模板 + install.sh + README
#   tripfolio-nas-<版本>.tar.gz       整个部署包的压缩版，便于上传到 NAS
#
# 说明：NAS 上没有源码与工具链，所以导出的是「只引用镜像」的编排（docker/docker-compose.nas.yaml），
# 且镜像内的前端产物是构建期嵌入的——改前端或换 VITE_AMAP_JS_KEY 都必须重新执行本脚本。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(cd -- "$SCRIPT_DIR/.." && pwd)
cd "$ROOT_DIR"

VERSION=${1:-$(date +%Y.%m.%d)}
IMAGE=${IMAGE:-tripfolio/app}
PLATFORM=${PLATFORM:-linux/amd64}
OUT_DIR=${OUT_DIR:-dist}
NPM_REGISTRY=${TRIPFOLIO_BUILD_NPM_REGISTRY:-https://registry.npmmirror.com}
GOPROXY=${TRIPFOLIO_BUILD_GOPROXY:-https://goproxy.cn,direct}

# 前端 JS Key 是编译期注入的：环境变量没给就退回本地 .env.local（该文件已被 git 忽略）
if [ -z "${VITE_AMAP_JS_KEY:-}" ] && [ -f apps/client/.env.local ]; then
	VITE_AMAP_JS_KEY=$(grep -E '^VITE_AMAP_JS_KEY=' apps/client/.env.local | head -1 | cut -d= -f2- | tr -d '\r' | xargs || true)
	[ -n "${VITE_AMAP_JS_KEY:-}" ] && echo "==> 已从 apps/client/.env.local 读取 VITE_AMAP_JS_KEY"
fi
if [ -z "${VITE_AMAP_JS_KEY:-}" ]; then
	echo "警告：未提供 VITE_AMAP_JS_KEY，镜像内页面可用但没有地图底图。" >&2
	echo "      需要底图时：VITE_AMAP_JS_KEY=你的Key bash scripts/docker-image.sh" >&2
fi

TAR_NAME="tripfolio-${VERSION}.tar"
BUNDLE_DIR="${OUT_DIR}/tripfolio-nas-${VERSION}"
BUNDLE_TGZ="${OUT_DIR}/tripfolio-nas-${VERSION}.tar.gz"
mkdir -p "$OUT_DIR"
TAR_PATH="${OUT_DIR}/${TAR_NAME}"

echo "==> [1/4] 构建镜像 ${IMAGE}:${VERSION}（平台 ${PLATFORM}）"
docker build \
	--platform "$PLATFORM" \
	--load \
	-f docker/Dockerfile \
	--build-arg "VITE_AMAP_JS_KEY=${VITE_AMAP_JS_KEY:-}" \
	--build-arg "NPM_REGISTRY=${NPM_REGISTRY}" \
	--build-arg "GOPROXY=${GOPROXY}" \
	--build-arg "VERSION=${VERSION}" \
	-t "${IMAGE}:${VERSION}" \
	-t "${IMAGE}:latest" \
	.

echo "==> [2/4] 导出 ${TAR_PATH}"
rm -f -- "$TAR_PATH" "${OUT_DIR}/.tmp-${TAR_NAME}"*
# 不用 docker save -o：-o 先写临时文件再 rename，Windows 上会被杀软/索引句柄挡住而失败，
# 但 docker 仍返回 0，结果是目标文件悄悄缺失。stdout 重定向由 shell 直接持有句柄，没有这个竞态。
docker save "${IMAGE}:${VERSION}" > "$TAR_PATH"
tar -tf "$TAR_PATH" > /dev/null
test -s "$TAR_PATH"

echo "==> [3/4] 组装部署包 ${BUNDLE_DIR}"
rm -rf -- "$BUNDLE_DIR"
mkdir -p "$BUNDLE_DIR"
cp "$TAR_PATH" "${BUNDLE_DIR}/${TAR_NAME}"
cp docker/.env.example "${BUNDLE_DIR}/.env.example"
# 把镜像版本写死进 compose：NAS 上不必再改 .env 里的 tag，也不会误用旧版本
sed -E "s#^([[:space:]]*image:[[:space:]]*).*#\1${IMAGE}:${VERSION}#" \
	docker/docker-compose.nas.yaml > "${BUNDLE_DIR}/docker-compose.yaml"

cat > "${BUNDLE_DIR}/install.sh" <<'INSTALL'
#!/usr/bin/env bash
# 在 NAS 上执行：导入镜像 → 准备 .env → 启动。
# 首次运行前请先复制并填写 .env.example（数据库、OSS、签名密钥、高德凭证）。
set -euo pipefail
cd "$(dirname -- "${BASH_SOURCE[0]}")"

DOCKER=${DOCKER:-docker}
TAR=$(ls tripfolio-*.tar | head -1)
[ -n "$TAR" ] || { echo "找不到镜像 tar" >&2; exit 1; }

if [ ! -f .env ]; then
	echo "缺少 .env：先执行 cp .env.example .env 并填写数据库、OSS、签名密钥与高德凭证" >&2
	exit 1
fi

echo "==> 导入镜像 $TAR"
$DOCKER load -i "$TAR"

echo "==> 启动服务"
$DOCKER compose -f docker-compose.yaml up -d

echo "==> 状态"
$DOCKER compose -f docker-compose.yaml ps
echo "启动失败时看日志：$DOCKER compose -f docker-compose.yaml logs --tail=80 app"
INSTALL
chmod +x "${BUNDLE_DIR}/install.sh"

cat > "${BUNDLE_DIR}/README.md" <<'BUNDLE_README'
# Tripfolio 离线部署包

1. 把本目录（或 `tripfolio-nas-*.tar.gz` 解压后的目录）传到 NAS，例如 `/volume1/docker/tripfolio`。
2. `cp .env.example .env`，填写：站点访问地址、已有 PostgreSQL 连接串、阿里云 OSS、签名密钥、
   邮件 SMTP 与高德凭证。变量含义见文件内注释。
3. `sudo ./install.sh`（等价于 `sudo docker load -i tripfolio-*.tar` +
   `sudo docker compose -f docker-compose.yaml up -d`）。
4. 打开 `http://<NAS 地址>:${TRIPFOLIO_HTTP_PORT}/`，用 `/health/ready` 确认就绪。

要点：

- 容器启动时会**自动执行数据库迁移**，不需要单独的迁移步骤。
- 数据库与对象存储都在本编排之外：PostgreSQL 用你已有的实例，文件存阿里云 OSS。
- 群晖等 NAS 若 `host.docker.internal` 不可用，把 `.env` 里的主机名换成 NAS 的局域网 IP。
- 升级：把新版本的 tar 与 compose 传到同一目录，`sudo docker load -i 新 tar` 后
  `sudo docker compose -f docker-compose.yaml up -d`；回滚就是换回旧 tar 再 up。
- 启动失败会打印中文排查块（阶段／原因／建议），退出码含义与排障对照表见仓库的 docker/README.md。
BUNDLE_README

echo "==> [4/4] 打包 ${BUNDLE_TGZ}"
rm -f -- "$BUNDLE_TGZ"
tar -czf "$BUNDLE_TGZ" -C "$OUT_DIR" "$(basename "$BUNDLE_DIR")"

SIZE=$(du -h "$TAR_PATH" | cut -f1)
echo
echo "==> 完成（平台 ${PLATFORM}）"
echo "    镜像文件：${TAR_PATH}（${SIZE}）"
echo "    部署包：  ${BUNDLE_DIR}/"
echo "    压缩包：  ${BUNDLE_TGZ}"
echo "    NAS 上：解压部署包 → cp .env.example .env 填好 → sudo ./install.sh"
