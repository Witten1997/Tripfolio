#!/usr/bin/env bash
# 一键部署到 NAS：构建 → 导出 tar → scp 上传 → docker load → 重启 compose 项目。
#
# 建议在 Git Bash / WSL 下运行（ssh、scp 走密钥；PowerShell 下的交互式密码会失败）。
#
# 用法：
#   cp scripts/.deploy.env.example scripts/.deploy.env.example    # 填 NAS 连接信息（该文件不入库）
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

: "${NAS_HOST:?请在 scripts/.deploy.env.example 配置 NAS_HOST}"
: "${NAS_USER:?请在 scripts/.deploy.env.example 配置 NAS_USER}"
: "${NAS_DIR:?请在 scripts/.deploy.env.example 配置 NAS_DIR（compose 与 .env 所在目录）}"
NAS_PORT=${NAS_PORT:-22}
NAS_TMP=${NAS_TMP:-/tmp}
NAS_DOCKER=${NAS_DOCKER:-/usr/local/bin/docker}
NAS_PROJECT=${NAS_PROJECT:-$(basename "$NAS_DIR")}
NAS_START_TIMEOUT=${NAS_START_TIMEOUT:-300}
IMAGE=${IMAGE:-tripfolio/app}
PLATFORM=${PLATFORM:-linux/amd64}
SUDO=${SUDO-sudo}

if [[ ! $NAS_START_TIMEOUT =~ ^[1-9][0-9]*$ ]]; then
  echo 'NAS_START_TIMEOUT 必须为正整数（秒）' >&2
  exit 1
fi

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
# 用未加引号的 heredoc 交给远端 bash：本地变量在此展开，远端要自己求值的用 \$ 转义。
$SSH 'bash -s' <<REMOTE
set -euo pipefail
cd '${NAS_DIR}' 2>/dev/null || { echo '==> 部署失败：部署目录不存在' >&2; exit 1; }
if [ ! -f .env ]; then
  echo '==> 部署未完成：请先填写 NAS 上的 .env，再重新执行本脚本' >&2
  exit 1
fi
# 签名密钥留空时自动生成一次并写入 .env；换掉它会让已登录会话、游标与验证码失效，所以只在空值时补。
if grep -qE '^TRIPFOLIO_KEYRING=(k[0-9A-Za-z_-]+=)?$' .env; then
  KEY=\$(openssl rand -base64 32 2>/dev/null || head -c 32 /dev/urandom | base64)
  ${SUDO} cp .env ".env.bak-\$(date +%Y%m%d%H%M%S)"
  ${SUDO} sed -i "s|^TRIPFOLIO_KEYRING=.*|TRIPFOLIO_KEYRING=k1=\$KEY|" .env
  echo '  已生成签名密钥并写入 NAS 上的 .env'
fi
compose() {
  ${SUDO} ${NAS_DOCKER} compose -p '${NAS_PROJECT}' -f docker-compose.yaml "\$@"
}
startup_failed() {
  echo "==> 部署失败：\$1" >&2
  compose ps -a >&2 || true
  echo '最近启动日志（含失败阶段、原因与排查建议）：' >&2
  compose logs --no-color --tail=120 app >&2 || true
  exit 1
}

# 强制重建 app，确保本次部署使用新镜像和配置，并从零开始记录重启次数。
compose up -d --force-recreate app || startup_failed '无法启动 app 容器'
CONTAINER_ID=\$(compose ps -a -q app) || startup_failed '无法查询 app 容器'
[ -n "\$CONTAINER_ID" ] || startup_failed '未找到 app 容器'

echo '等待 app 健康检查通过（最多 ${NAS_START_TIMEOUT} 秒）…'
DEADLINE=\$((SECONDS + ${NAS_START_TIMEOUT}))
LAST_STATE=''
while true; do
  STATE=\$(${SUDO} ${NAS_DOCKER} inspect --format '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}} {{.RestartCount}} {{.State.ExitCode}}' "\$CONTAINER_ID") || startup_failed '无法读取容器状态'
  read -r STATUS HEALTH RESTARTS EXIT_CODE <<< "\$STATE"
  case "\$STATUS" in
    exited|dead|restarting|removing|paused)
      startup_failed "容器状态为 \$STATUS，进程退出码 \$EXIT_CODE，重启次数 \$RESTARTS"
      ;;
  esac
  [ "\$RESTARTS" -eq 0 ] || startup_failed "启动期间发生自动重启（\$RESTARTS 次），请查看启动日志"
  case "\$HEALTH" in
    unhealthy) startup_failed '容器健康检查未通过' ;;
    missing) startup_failed '容器未启用健康检查，无法确认服务就绪' ;;
  esac
  if [ "\$STATUS" = running ] && [ "\$HEALTH" = healthy ]; then
    echo '==> app 健康检查通过'
    break
  fi
  if [ "\$STATE" != "\$LAST_STATE" ]; then
    echo "  容器状态：\$STATUS，健康状态：\$HEALTH"
    LAST_STATE=\$STATE
  fi
  [ "\$SECONDS" -lt "\$DEADLINE" ] || startup_failed '等待健康检查超时（${NAS_START_TIMEOUT} 秒）'
  sleep 2
done
compose ps
echo
echo '最近启动日志：'
compose logs --no-color --tail=30 app || true
REMOTE

echo
echo "==> 完成：${IMAGE}:${VERSION}（本地导出保留在 dist/${TAR_NAME}，可手动导入）"
