#!/usr/bin/env bash
# 下载与 scripts/sqlc.sh 匹配的 sqlc 官方二进制到仓库根 .tools/（已在 .gitignore 中）。
set -euo pipefail

VERSION="1.31.1"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TOOLS_DIR="$ROOT_DIR/.tools"
mkdir -p "$TOOLS_DIR"

case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) OS="windows"; EXT="zip" ;;
  Darwin) OS="darwin"; EXT="tar.gz" ;;
  Linux) OS="linux"; EXT="tar.gz" ;;
  *) echo "不支持的系统: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "不支持的架构: $(uname -m)" >&2; exit 1 ;;
esac

ARCHIVE="sqlc_${VERSION}_${OS}_${ARCH}.${EXT}"
URL="https://downloads.sqlc.dev/${ARCHIVE}"
echo "下载 ${URL}"
curl -fsSL -o "$TOOLS_DIR/$ARCHIVE" "$URL"
cd "$TOOLS_DIR"
if [ "$EXT" = "zip" ]; then
  unzip -o -q "$ARCHIVE"
else
  tar -xzf "$ARCHIVE"
fi
rm -f "$ARCHIVE"
"$TOOLS_DIR"/sqlc* version
