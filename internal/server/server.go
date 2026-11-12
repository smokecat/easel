// Package server 负责 HTTP 装配：按主机把请求分流到后台或内容子域，并挂中间件。
//
// 后台主机：管理面板、健康检查、/api/。内容主机（<label>.<内容域>）：只有 internal/content。
// 两边不共用任何 cookie 逻辑；落在内容域里却不是一级标签的主机直接 404，不会落到后台。
package server

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/smokecat/easel/internal/api"
	"github.com/smokecat/easel/internal/auth"
	"github.com/smokecat/easel/internal/config"
	"github.com/smokecat/easel/internal/content"
)

// Options 是装配服务端所需的依赖。API、Content 为空时对应的路由不挂（测试用）。
type Options struct {
	// Assets 是管理面板的静态文件，根目录下应有 index.html。
	Assets  fs.FS
	Hosts   config.Hosts
	API     *api.API
	Content *content.Handler
}

// New 返回 TCP 监听器用的根 handler，按 Host 分流。
func New(opts Options) http.Handler {
	app := appMux(opts)
	if opts.Content == nil {
		return app
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if label, ok := opts.Hosts.Label(r.Host); ok {
			opts.Content.ServeHTTP(w, r, label)
			return
		}
		if opts.Hosts.IsContentDomain(r.Host) {
			http.Error(w, "404 page not found", http.StatusNotFound)
			return
		}
		app.ServeHTTP(w, r)
	})
}

// NewSocket 返回本机 unix socket 用的 handler：只有后台路由，请求标记为本机受信。
func NewSocket(opts Options) http.Handler {
	return auth.TrustLocal(appMux(opts))
}

func appMux(opts Options) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	if opts.API != nil {
		opts.API.Register(mux)
	}
	if opts.Content != nil {
		mux.Handle("GET "+TLSAskPath, tlsAsk(opts))
	}
	mux.Handle("/", spaHandler(opts.Assets))
	return mux
}

// TLSAskPath 是给 Caddy on-demand TLS 的 ask 端点：GET /_tls/ask?domain=<主机名>，200 表示可以签发。
const TLSAskPath = "/_tls/ask"

// tlsAsk 只放行后台主机和已存在的原型 / 版本标签，避免任意子域都去签证书（会被 CA 拦截、挤占额度）。
// Caddy 自己发起的 ask 请求不带 X-Forwarded-For；经反向代理进来的公网请求都带，一律 404，
// 免得把它当成枚举原型的接口。Caddyfile 里也应屏蔽这个路径。
func tlsAsk(opts Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("X-Forwarded-For") != "" {
			http.NotFound(w, r)
			return
		}
		domain := r.URL.Query().Get("domain")
		ok := opts.Hosts.IsApp(domain)
		if label, isLabel := opts.Hosts.Label(domain); !ok && isLabel {
			var err error
			if ok, err = opts.Content.Exists(r.Context(), label); err != nil {
				http.Error(w, "lookup failed", http.StatusInternalServerError)
				return
			}
		}
		if !ok {
			http.Error(w, "unknown domain", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// spaHandler 提供管理面板：存在的文件直接返回，其余路径回退到 index.html，交给前端路由。
func spaHandler(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if st, err := fs.Stat(assets, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					// Vite 产物文件名带内容哈希，可以长期缓存。
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "admin UI not built: run `make build`", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(index)
	})
}
