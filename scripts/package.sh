#!/usr/bin/env bash
# 打包单二进制：构建契约与前端 → 复制进 embed 目录 → 交叉编译后端。
#
# 用法：
#   bash scripts/package.sh                    # 默认产出 dist/tripfolio（linux/amd64）
#   GOOS=linux GOARCH=arm64 bash scripts/package.sh dist/tripfolio-arm64
#   TRIPFOLIO_VERSION=2026.09.15 bash scripts/package.sh
#
# 前端产物与后端编译进同一个文件；改前端必须重新执行本脚本。
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

out="${1:-dist/tripfolio}"
target_os="${GOOS:-linux}"
target_arch="${GOARCH:-amd64}"
version="${TRIPFOLIO_VERSION:-$(date +%Y.%m.%d)}"
dest="apps/server/internal/web/dist"

# 前端原生依赖（rolldown 等）与 Node 版本绑定：Git Bash 里的 node 往往与 pnpm 用的不是同一份，
# 版本不符时报错会很难懂，这里提前拦住并说清怎么修。
if [ -f .node-version ]; then
	required="$(tr -d 'v \n' < .node-version)"
	current="$(node -p 'process.versions.node' 2>/dev/null || echo '未知')"
	if [ "${current%%.*}" != "${required%%.*}" ]; then
		echo "错误：当前 node 是 $current，仓库要求 $(cat .node-version)。" >&2
		echo "      请在与 pnpm 相同的 Node 环境下执行本脚本（Windows 上 Git Bash 常指向另一份 Node）。" >&2
		exit 1
	fi
fi

echo "==> 构建契约与前端"
pnpm --filter @tripfolio/contracts build
pnpm --filter @tripfolio/client build

echo "==> 复制前端产物到 $dest"
# 保留 .gitignore：该目录里的构建产物按约定不入库
find "$dest" -mindepth 1 -not -name .gitignore -delete
cp -R apps/client/dist/. "$dest"/

echo "==> 编译 $target_os/$target_arch（版本 $version）"
mkdir -p "$(dirname "$out")"
(
	cd apps/server
	CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
		-trimpath -ldflags "-s -w -X main.version=$version" \
		-o "$root/$out" ./cmd/tripfolio
)

size="$(du -h "$out" | cut -f1)"
echo "==> 完成：$out（$size）"
echo "    部署只需该文件与一份 .env：./tripfolio serve 会自动迁移并同时提供接口、页面与后台任务。"
echo "    提醒：$dest 下现在有构建产物，已由 .gitignore 忽略；重新打包会覆盖它们。"
