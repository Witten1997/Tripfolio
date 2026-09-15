package web

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 前端资源的缓存策略：Vite 产物文件名带内容哈希，可长缓存；入口文件每次校验，发版后立即生效。
const (
	immutableCache = "public, max-age=31536000, immutable"
	noCache        = "no-cache"
	assetsDir      = "assets/"
	// mountPrefix 是高德代理的挂载前缀，转发时只剥掉它，其余路径原样带给高德。
	mountPrefix = "/_AMapService"
)

// Upstream 是一条高德代理规则：命中 Prefix 的请求转发到 Target。
type Upstream struct {
	Prefix string
	Target string
}

// DefaultUpstreams 与高德官方给出的三段代理路径一致：样式、矢量图、其余 REST。
func DefaultUpstreams() []Upstream {
	return []Upstream{
		{Prefix: mountPrefix + "/v4/map/styles", Target: "https://webapi.amap.com"},
		{Prefix: mountPrefix + "/v3/vectormap", Target: "https://fmap01.amap.com"},
		{Prefix: mountPrefix + "/", Target: "https://restapi.amap.com"},
	}
}

// Options 是构造处理器的依赖；除 Assets 外都可缺省，便于测试注入。
type Options struct {
	Logger *slog.Logger
	// JSCode 是高德 JS API 安全密钥，由服务端覆盖进查询串，不进入浏览器产物。
	JSCode string
	// Assets 缺省使用内嵌的前端产物。
	Assets fs.FS
	// Transport 供测试注入；缺省使用 http.DefaultTransport。
	Transport http.RoundTripper
	// Upstreams 供测试覆盖；缺省使用高德官方三条路径。
	Upstreams []Upstream
}

type handler struct {
	logger     *slog.Logger
	jsCode     string
	assets     fs.FS
	noEntry    bool
	proxies    []proxyRoute
	warnedOnce sync.Once
}

// proxyRoute 是预建好的一条高德代理：构造时解析上游地址，请求路径上不再重复解析与分配。
type proxyRoute struct {
	prefix  string
	handler http.Handler
}

// NewHandler 组装静态资源、SPA 回退与高德代理。
// 它挂在外层路由的兜底位置：/api/v1 与 /health/* 由各自的路由先匹配。
func NewHandler(o Options) (http.Handler, error) {
	assets := o.Assets
	if assets == nil {
		var err error
		assets, err = Assets()
		if err != nil {
			return nil, err
		}
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.Default()
	}
	transport := o.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	upstreams := o.Upstreams
	if len(upstreams) == 0 {
		upstreams = DefaultUpstreams()
	}
	// 代理在构造时建好：上游地址解析只做一次，请求路径上不再重复 url.Parse 与分配。
	base := &handler{logger: logger, jsCode: o.JSCode, assets: assets}
	proxies := make([]proxyRoute, 0, len(upstreams))
	for _, up := range upstreams {
		proxy, err := base.proxyFor(up, transport)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, proxyRoute{prefix: up.Prefix, handler: proxy})
	}
	base.proxies = proxies
	_, err := fs.Stat(assets, "index.html")
	base.noEntry = err != nil
	h := base
	if h.noEntry {
		logger.Warn("二进制内没有前端产物，页面会返回说明页；先构建前端再编译（见 docker/Dockerfile 或 scripts/package.sh）")
	}
	if h.jsCode == "" {
		logger.Warn("未配置 TRIPFOLIO_AMAP_JSCODE，地图底图与样式将不可用；搜索选点等功能不受影响")
	}
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, mountPrefix+"/") {
		h.serveAMap(w, r)
		return
	}
	h.serveStatic(w, r)
}

// serveStatic 提供前端产物：命中文件直接返回，未知路径回落入口页；/assets/ 下缺失的资源返回 404，
// 避免浏览器把 HTML 当成脚本或样式解析。以点开头的路径一律 404：embed 把 dist 下的 .gitignore
// 之类的构建期文件也嵌了进来，它们不该通过 HTTP 暴露。
func (h *handler) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != "" {
		if hasDotSegment(name) {
			http.NotFound(w, r)
			return
		}
		if info, err := fs.Stat(h.assets, name); err == nil && !info.IsDir() {
			h.serveFile(w, r, name)
			return
		}
		if strings.HasPrefix(name, assetsDir) {
			http.NotFound(w, r)
			return
		}
	}
	h.serveEntry(w, r)
}

// hasDotSegment 判断路径里是否有以点开头的片段（.gitignore、.env、. DS_Store 等）。
func hasDotSegment(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}

func (h *handler) serveEntry(w http.ResponseWriter, r *http.Request) {
	if h.noEntry {
		h.warnedOnce.Do(func() {
			h.logger.Error("收到页面请求，但二进制内没有前端产物", "path", r.URL.Path)
		})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", noCache)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, missingFrontendPage)
		return
	}
	h.serveFile(w, r, "index.html")
}

func (h *handler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(h.assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", noCache)
	if strings.HasPrefix(name, assetsDir) {
		w.Header().Set("Cache-Control", immutableCache)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, modTime(h.assets, name), bytes.NewReader(data))
}

// modTime 取资源修改时间；内嵌资源的零值时间会让 ServeContent 跳过 Last-Modified 校验。
func modTime(assets fs.FS, name string) time.Time {
	info, err := fs.Stat(assets, name)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// serveAMap 把 /_AMapService 下的请求交给预建好的代理（构造时已解析上游地址）。
func (h *handler) serveAMap(w http.ResponseWriter, r *http.Request) {
	if h.jsCode == "" {
		http.Error(w, "未配置 TRIPFOLIO_AMAP_JSCODE", http.StatusServiceUnavailable)
		return
	}
	route, ok := h.match(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	route.handler.ServeHTTP(w, r)
}

// match 取最长匹配的代理规则；未命中任何规则时不代理，避免把任意路径带出站。
func (h *handler) match(path string) (proxyRoute, bool) {
	best := proxyRoute{}
	for _, route := range h.proxies {
		if strings.HasPrefix(path, route.prefix) && len(route.prefix) > len(best.prefix) {
			best = route
		}
	}
	return best, best.prefix != ""
}

// proxyFor 在构造阶段调用一次：解析上游地址并建好反向代理。
func (h *handler) proxyFor(up Upstream, transport http.RoundTripper) (http.Handler, error) {
	target, err := url.Parse(up.Target)
	if err != nil {
		return nil, fmt.Errorf("解析高德上游地址 %q: %w", up.Target, err)
	}
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme = target.Scheme
			out.URL.Host = target.Host
			out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, mountPrefix)
			if out.URL.Path == "" {
				out.URL.Path = "/"
			}
			out.URL.RawPath = ""
			out.Host = target.Host
			// 覆盖客户端可能自带的 jscode：安全密钥只能由服务端提供，其余查询参数保留。
			query := out.URL.Query()
			query.Set("jscode", h.jsCode)
			out.URL.RawQuery = query.Encode()
			// 不把本站凭证转发给第三方。
			out.Header.Del("Cookie")
			out.Header.Del("Authorization")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h.logger.Warn("高德代理请求失败", "path", r.URL.Path, "error", err)
			http.Error(w, "高德服务暂时不可达，请稍后重试", http.StatusBadGateway)
		},
	}, nil
}

const missingFrontendPage = `<!doctype html>
<html lang="zh-CN">
<meta charset="utf-8">
<title>Tripfolio 前端产物缺失</title>
<style>body{font-family:system-ui,sans-serif;max-width:44rem;margin:10vh auto;padding:0 1.5rem;line-height:1.7}code{background:#eee;padding:.1rem .3rem;border-radius:.2rem}</style>
<h1>前端产物没有嵌入这个二进制</h1>
<p>编译时 <code>apps/server/internal/web/dist/</code> 里没有 <code>index.html</code>，所以没有页面可以返回。接口与分享链接不受影响。</p>
<p>打包方式：在仓库根执行 <code>bash scripts/package.sh</code>（会先构建前端再编译二进制），或使用 <code>docker compose build</code> 构建镜像。</p>
<p>仅后端开发时不需要前端产物，可以直接调用 <code>/api/v1/…</code> 与 <code>/health/ready</code>。</p>
</html>
`
