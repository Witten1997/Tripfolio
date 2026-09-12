#!/usr/bin/env bash
# 运行 sqlc（默认 generate）。优先使用仓库 .tools/ 下的二进制，其次 PATH 中的 sqlc，最后回退到 Docker 镜像。
# 本机没有 C 编译器时不能用 `go tool` 构建 sqlc，因此用官方发布的二进制；`scripts/sqlc-install.sh` 负责下载。
set -euo pipefail

SERVER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT_DIR="$(cd "$SERVER_DIR/../.." && pwd)"
VERSION="1.31.1"
cd "$SERVER_DIR"

if [ -x "$ROOT_DIR/.tools/sqlc.exe" ]; then
  exec "$ROOT_DIR/.tools/sqlc.exe" "${@:-generate}"
elif [ -x "$ROOT_DIR/.tools/sqlc" ]; then
  exec "$ROOT_DIR/.tools/sqlc" "${@:-generate}"
elif command -v sqlc >/dev/null 2>&1; then
  exec sqlc "${@:-generate}"
fi

HOST_DIR="$SERVER_DIR"
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) HOST_DIR="$(pwd -W)" ;;
esac
export MSYS_NO_PATHCONV=1
exec docker run --rm -v "${HOST_DIR}:/src" -w /src "sqlc/sqlc:${VERSION}" "${@:-generate}"
