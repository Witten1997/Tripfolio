package web

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// 产物目录固定，公开地址由配置决定；只在后台 HTML 注入路径，不下发其他配置。
func (h *handler) serveAdmin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == h.adminPath {
		http.Redirect(w, r, h.adminPath+"/", http.StatusPermanentRedirect)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, h.adminPath+"/")
	if path == "setup" || strings.HasPrefix(path, "setup/") {
		if h.adminSetupOpen == nil {
			http.NotFound(w, r)
			return
		}
		open, err := h.adminSetupOpen(r.Context())
		if err != nil {
			http.Error(w, "初始化状态暂不可用，请稍后重试", http.StatusServiceUnavailable)
			return
		}
		if !open {
			http.NotFound(w, r)
			return
		}
	}
	name := "wahaha/" + path
	if path != "" && path != "index.html" {
		if info, err := fs.Stat(h.assets, name); err == nil && !info.IsDir() {
			h.serveFile(w, r, name)
			return
		}
		if strings.HasPrefix(path, assetsDir) {
			http.NotFound(w, r)
			return
		}
	}
	data, err := fs.ReadFile(h.assets, "wahaha/index.html")
	if err != nil {
		http.Error(w, "后台前端产物缺失，请构建并重新打包管理后台", http.StatusServiceUnavailable)
		return
	}
	data = bytes.Replace(data, []byte("<head>"), []byte(`<head><base href="`+html.EscapeString(h.adminPath+"/")+`">`), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
}
