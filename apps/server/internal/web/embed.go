// Package web 提供嵌入二进制的前端产物与高德 JS API 安全密钥代理。
// 单二进制部署下，静态资源、SPA 回退与 /_AMapService 代理都在进程内完成，不需要额外的反向代理。
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

// embedded 是前端构建产物。打包脚本先把 apps/client/dist 复制到 dist/ 再编译；
// 仓库里只有 dist/.gitignore，因此没构建前端时 go build 与 go test 依然可用，
// 此时运行期会返回一页说明「前端产物未嵌入」，而不是白屏。
//
//go:embed all:dist
var embedded embed.FS

// Assets 返回内嵌的前端产物目录（dist/ 的内容）。
func Assets() (fs.FS, error) {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, fmt.Errorf("读取内嵌前端产物: %w", err)
	}
	return sub, nil
}

// MissingFrontend 判断内嵌产物里是否缺少入口文件，用于启动时给出明确告警。
func MissingFrontend() bool {
	assets, err := Assets()
	if err != nil {
		return true
	}
	_, err = fs.Stat(assets, "index.html")
	return err != nil
}
