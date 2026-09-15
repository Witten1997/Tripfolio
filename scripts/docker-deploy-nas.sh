#!/usr/bin/env bash
# 一键部署到 NAS：构建 → 导出 tar → scp 上传 → docker load → 重启 compose 项目。
#
# 建议在 Git Bash / WSL 下运行（ssh、scp 走密钥；PowerShell 下的交互式密码会失败）。
#
# 用法：
#   cp scripts/.deploy.env.example scripts/.deploy.env    # 填 NAS 连接信息（该文件不入库）
#   bash scripts/docker-deploy-nas.sh 2026.09.15
#
# 首次部署：脚本会在 NAS 上没有 compose 时上传一份（只引用镜像，不含 build），
# 并在 NAS 上生成 .env.example；随后需要你登录 NAS 执行 cp .env.example .env 并填写凭证，
# 再跑一次本脚本即可启动。密钥不经过本脚本传输。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(cd -- "$SCRIPT_DIR/.." && pwd)
cd "$ROOT_DIR"

VERSION=${1:?'缺少版本号，例：bash scripts/docker-deploy-nas.sh 2026.09.15'}

ENV_FILE="$SCRIPT_DIR/.deploy.env"
# shellcheck disable=SC1090
[ -f "$ENV_FILE" ] && source "$ENV_FILE"

: "${NAS_HOST:?请在 scripts/.deploy.env 配置 NAS_HOST}"
: "${NAS_USER:?请在 scripts/.deploy.env 配置 NAS_USER}"
: "${NAS_DIR:?请在 scripts/.deploy.env 配置 NAS_DIR（compose 与 .env 所在目录）}"
NAS_PORT=${NAS_PORT:-22}
NAS_TMP=${NAS_TMP:-/tmp}
NAS_DOCKER=${NAS_DOCKER:-/usr/local/bin/docker}
NAS_PROJECT=${NAS_PROJECT:-$(basename "$NAS_DIR")}
IMAGE=${IMAGE:-tripfolio/app}
PLATFORM=${PLATFORM:-linux/amd64}
SUDO=${SUDO-sudo}

TAR_NAME="tripfolio-${VERSION}.tar"
REMOTE="${NAS_USER}@${NAS_HOST}"
SSH="ssh -p ${NAS_PORT} ${REMOTE}"
SCP="scp -O -P ${NAS_PORT}"

echo "==> [1/4] 构建并导出镜像（复用 scripts/docker-image.sh）"
PLATFORM="$PLATFORM" IMAGE="$IMAGE" bash "$SCRIPT_DIR/docker-image.sh" "$VERSION"

echo "==> [2/4] 上传 ${TAR_NAME} 到 ${REMOTE}:${NAS_TMP}"
$SCP "dist/${TAR_NAME}" "${REMOTE}:${NAS_TMP}/${TAR_NAME}"

echo "==> [3/4] 在 NAS 上导入镜像并更新编排"
# compose 里只改 image 行：保留 NAS 上已有的端口、环境与注释，不覆盖用户改动。
$SSH "set -e
${SUDO} mkdir -p '${NAS_DIR}'
if [ ! -f '${NAS_DIR}/docker-compose.yaml' ]; then
  echo '  NAS 上没有 compose，稍后由本脚本上传一份'
fi
${SUDO} ${NAS_DOCKER} load -i '${NAS_TMP}/${TAR_NAME}'
rm -f '${NAS_TMP}/${TAR_NAME}'"

# 首次部署时把「只引用镜像」的 compose 与环境模板送上去；之后不再覆盖已有文件。
BUNDLE_COMPOSE=$(mktemp)
sed -E "s#^([[:space:]]*image:[[:space:]]*).*#\1${IMAGE}:${VERSION}#" \
	docker/docker-compose.nas.yaml > "$BUNDLE_COMPOSE"
$SCP "$BUNDLE_COMPOSE" "${REMOTE}:${NAS_TMP}/tripfolio-compose.yaml"
$SCP docker/.env.example "${REMOTE}:${NAS_TMP}/tripfolio-env.example"
rm -f "$BUNDLE_COMPOSE"
$SSH "set -e
if [ ! -f '${NAS_DIR}/docker-compose.yaml' ]; then
  ${SUDO} mv '${NAS_TMP}/tripfolio-compose.yaml' '${NAS_DIR}/docker-compose.yaml'
  echo '  已放置新的 compose'
else
  ${SUDO} sed -i -E 's#^([[:space:]]*image:[[:space:]]*).*#\1${IMAGE}:${VERSION}#' '${NAS_DIR}/docker-compose.yaml'
  rm -f '${NAS_TMP}/tripfolio-compose.yaml'
  echo '  已把 compose 的镜像版本更新为 ${VERSION}'
fi
if [ ! -f '${NAS_DIR}/.env' ]; then
  ${SUDO} mv '${NAS_TMP}/tripfolio-env.example' '${NAS_DIR}/.env.example'
  echo
  echo '  首次部署：NAS 上还没有 .env。请在 NAS 上执行'
  echo '    cd ${NAS_DIR} && cp .env.example .env && vi .env'
  echo '  填好数据库、OSS、签名密钥与高德凭证后，重新执行本脚本。'
  exit 0
fi
rm -f '${NAS_TMP}/tripfolio-env.example'
echo '  已确认 NAS 上存在 .env'"

echo "==> [4/4] 重启项目 ${NAS_PROJECT}"
$SSH "set -e
if [ ! -f '${NAS_DIR}/.env' ]; then
  echo '  跳过启动：等待 .env'
  exit 0
fi
cd '${NAS_DIR}'
${SUDO} ${NAS_DOCKER} compose -p '${NAS_PROJECT}' -f docker-compose.yaml up -d
${SUDO} ${NAS_DOCKER} compose -p '${NAS_PROJECT}' -f docker-compose.yaml ps
echo
echo '最近日志（启动失败会打印中文排查块）：'
${SUDO} ${NAS_DOCKER} compose -p '${NAS_PROJECT}' -f docker-compose.yaml logs --tail=30 app"

echo
echo "==> 完成：${IMAGE}:${VERSION}（本地导出保留在 dist/${TAR_NAME}，可手动导入）"
