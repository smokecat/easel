// Package proxy 是端口代理：把内容主机上的请求转发给服务器本机回环地址上的 dev server。
//
// 只服务内容主机，鉴权由 auth.Gate 在前面做完。上游一律视为不可信：它的 Set-Cookie 不能带 Domain、
// 不能冒充 easel 的 cookie，HSTS / Alt-Svc 不透传。目标只能是回环地址（[::1] 和 127.0.0.1）。
//
// 两种 Host 模式：
//   - localhost（默认）：对上游伪装成 localhost:<port>。Vite、Next、webpack-dev-server 的 Host / Origin
//     校验零配置通过；Origin、Referer 只在等于本原型公网源时改写，第三方来源原样透传（不削弱上游的 CSRF 防护）
//   - preserve：保留公网 Host，附带 X-Forwarded-*，给需要知道公网地址的应用用
package proxy

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/smokecat/easel/internal/config"
	"github.com/smokecat/easel/internal/store/db"
)

// Host 模式。
const (
	HostLocalhost = "localhost"
	HostPreserve  = "preserve"
)

// WatchInterval 是长连接（WebSocket、SSE、慢请求）重新校验访问权的间隔。测试里会调小。
var WatchInterval = 5 * time.Second

// Handler 转发内容主机上的请求。
type Handler struct {
	Hosts config.Hosts
	Log   *slog.Logger

	rp *httputil.ReverseProxy
}

type upstreamKey struct{}

// target 是一次请求的转发目标，经 context 传给 Rewrite / ModifyResponse。
type target struct {
	port   int
	mode   string
	public string // 本原型的公网源，如 https://app-k3x9qa.easel.example.com
	label  string
}

// New 返回 Handler。
func New(hosts config.Hosts, log *slog.Logger) *Handler {
	h := &Handler{Hosts: hosts, Log: log}
	h.rp = &httputil.ReverseProxy{
		FlushInterval:  -1, // SSE、流式响应立即刷新
		Rewrite:        h.rewrite,
		ModifyResponse: h.modifyResponse,
		ErrorHandler:   h.errorHandler,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				t, _ := ctx.Value(upstreamKey{}).(*target)
				if t == nil {
					return nil, errors.New("proxy: no target")
				}
				return dialLoopback(ctx, t.port)
			},
			// 上游都在本机，不复用跨原型的连接：Transport 按 URL 主机名分池，而 URL 主机名就是 localhost:<port>
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 5 * time.Minute, // dev server 首次编译可能很慢
			DisableCompression:    true,            // 原样透传上游的压缩
		},
	}
	return h
}

// Still 由调用方（auth.Gate）提供：长连接期间定期调用，返回 false 时断开。
type Still func(ctx context.Context) bool

// ServeHTTP 把请求转发到 up 指定的端口。still 为 nil 时不做长连接校验。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, label string, up db.Upstream, still Still) {
	// Gate 给自己的页面设了 frame-ancestors 'none'；ReverseProxy 用 Add 合并上游的头，不删掉的话
	// 多个 CSP 取交集，后台就没法用 iframe 嵌入预览。easel 的 CSP 在 modifyResponse 里重新加
	w.Header().Del("Content-Security-Policy")
	h.setSecurityHeaders(w.Header())
	// 和静态内容一样默认禁止 Service Worker：撤销访问后它还能离线展示缓存的内容
	if r.Header.Get("Service-Worker") == "script" {
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return
	}
	t := &target{port: int(up.Port), mode: up.HostMode, public: h.Hosts.ContentOrigin(label), label: label}
	ctx, cancel := context.WithCancel(context.WithValue(r.Context(), upstreamKey{}, t))
	defer cancel()
	if still != nil {
		go watch(ctx, still, cancel)
	}
	h.rp.ServeHTTP(w, r.WithContext(ctx))
}

// watch 每 WatchInterval 校验一次访问权，失效时取消请求：ReverseProxy 对升级后的连接（WebSocket）
// 会在 context 结束时关闭后端连接，客户端随之断开；普通流式响应也会中止。
func watch(ctx context.Context, still Still, cancel context.CancelFunc) {
	t := time.NewTicker(WatchInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !still(ctx) {
				cancel()
				return
			}
		}
	}
}

func targetOf(r *http.Request) *target {
	t, _ := r.Context().Value(upstreamKey{}).(*target)
	return t
}

func (h *Handler) rewrite(pr *httputil.ProxyRequest) {
	t := targetOf(pr.In)
	local := "localhost:" + strconv.Itoa(t.port)
	pr.SetURL(&url.URL{Scheme: "http", Host: local})
	out := pr.Out.Header
	// easel 自己的凭据不给上游（访问 cookie 已由 Gate 剔除）
	out.Del("Authorization")
	// 进来的 X-Forwarded-* 是前置代理（Caddy）加的，不透传
	for _, k := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "X-Real-Ip"} {
		out.Del(k)
	}
	switch t.mode {
	case HostPreserve:
		pr.Out.Host = pr.In.Host
		pr.SetXForwarded()
		out.Set("X-Forwarded-Proto", h.Hosts.Content.Scheme) // 公网 scheme 来自配置，不从请求推断
	default:
		pr.Out.Host = local
		// 只改写本原型自己的来源；第三方来源原样透传，上游照常拒绝
		if o := pr.In.Header.Get("Origin"); o == t.public {
			out.Set("Origin", "http://"+local)
		}
		if ref := pr.In.Header.Get("Referer"); ref == t.public || strings.HasPrefix(ref, t.public+"/") {
			out.Set("Referer", "http://"+local+strings.TrimPrefix(ref, t.public))
		}
	}
}

func (h *Handler) modifyResponse(resp *http.Response) error {
	t := targetOf(resp.Request)
	hdr := resp.Header
	if loc := hdr.Get("Location"); loc != "" {
		hdr.Set("Location", rewriteLocation(loc, t.port, t.public))
	}
	if cs := hdr.Values("Set-Cookie"); len(cs) > 0 {
		hdr.Del("Set-Cookie")
		for _, c := range cs {
			if c = filterCookie(c); c != "" {
				hdr.Add("Set-Cookie", c)
			}
		}
	}
	// 这些头会影响整个主机（甚至父域）上的后续请求，上游说了不算
	hdr.Del("Strict-Transport-Security")
	hdr.Del("Alt-Svc")
	hdr.Del("Public-Key-Pins")
	h.addSecurityHeaders(hdr)
	return nil
}

// rewriteLocation 把指向上游本机地址的绝对重定向改回公网源。两种模式都做：
// Next 等框架拼绝对 URL 时不看 Host 头，还会按 X-Forwarded-Proto 拼出 https://localhost:<port>。
func rewriteLocation(loc string, port int, public string) string {
	p := strconv.Itoa(port)
	for _, host := range []string{"localhost:" + p, "127.0.0.1:" + p, "[::1]:" + p} {
		for _, scheme := range []string{"http://", "https://"} {
			prefix := scheme + host
			if loc == prefix || strings.HasPrefix(loc, prefix+"/") || strings.HasPrefix(loc, prefix+"?") {
				return public + strings.TrimPrefix(loc, prefix)
			}
		}
	}
	return loc
}

// filterCookie 处理上游的一条 Set-Cookie：冒充 easel 的 cookie 丢弃；去掉 Domain 属性，强制 host-only。
// 伪装模式下上游以为自己在 localhost，Domain 写的是 localhost；两种模式都不能让上游往父域
// （进而后台和别的原型）种 cookie。
func filterCookie(c string) string {
	parts := strings.Split(c, ";")
	name, _, _ := strings.Cut(strings.TrimSpace(parts[0]), "=")
	if strings.HasPrefix(name, "__Host-easel") || strings.HasPrefix(name, "easel_") || strings.HasPrefix(name, "__Secure-easel") {
		return ""
	}
	out := parts[:1]
	for _, p := range parts[1:] {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(p)), "domain=") {
			out = append(out, p)
		}
	}
	return strings.Join(out, ";")
}

func (h *Handler) setSecurityHeaders(hdr http.Header) {
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if h.Hosts.Content.Scheme == "https" {
		hdr.Set("Strict-Transport-Security", "max-age=63072000")
	}
}

// addSecurityHeaders 在上游响应头之后追加 easel 的头：CSP 用 Add，多个 CSP 头取交集，上游放宽不了。
func (h *Handler) addSecurityHeaders(hdr http.Header) {
	hdr.Add("Content-Security-Policy", "frame-ancestors 'self' "+h.Hosts.AppOrigin())
	hdr.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if h.Hosts.Content.Scheme == "https" {
		hdr.Set("Strict-Transport-Security", "max-age=63072000")
	}
}

func (h *Handler) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	t := targetOf(r)
	if errors.Is(r.Context().Err(), context.Canceled) {
		// 客户端走了，或者访问权在长连接期间失效
		return
	}
	h.Log.Warn("proxy upstream error", "label", t.label, "port", t.port, "err", err)
	hdr := w.Header()
	hdr.Set("Cache-Control", "no-store")
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Error(w, "502 Bad Gateway: nothing is answering on the upstream port", http.StatusBadGateway)
		return
	}
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'self' "+h.Hosts.AppOrigin())
	w.WriteHeader(http.StatusBadGateway)
	_ = badGateway.Execute(w, map[string]any{"Port": t.port})
}

var badGateway = template.Must(template.New("502").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>Dev server not reachable · easel</title>
<style>
:root { color-scheme: light dark; font-family: system-ui, -apple-system, sans-serif; }
body { max-width: 40rem; margin: 10vh auto; padding: 0 1.5rem; line-height: 1.55; }
code { font-size: .9em; }
</style></head><body>
<h1>Dev server not reachable</h1>
<p>Nothing is answering on port <code>{{.Port}}</code> on the easel server. Start the dev server (or check that it is still running), then reload.</p>
<p>Notes:</p>
<ul>
<li>Bind it to the loopback address so it is not exposed directly, e.g. <code>next dev -H 127.0.0.1</code> or <code>vite --host 127.0.0.1</code>.</li>
<li>webpack-dev-server: hot reload needs <code>client: { webSocketURL: 'auto://0.0.0.0:0/ws' }</code> in <code>devServer</code>.</li>
</ul>
</body></html>
`))
