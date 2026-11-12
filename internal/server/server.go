// Package server 负责 HTTP 装配：按主机把请求分流到后台或内容子域，并挂中间件。
//
// 目前只有后台主机的一部分：嵌入的管理面板和健康检查。按主机分流在内容服务落地时加入。
package server

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Options 是装配服务端所需的依赖。
type Options struct {
	// Assets 是管理面板的静态文件，根目录下应有 index.html。
	Assets fs.FS
}

// New 返回服务端的根 handler。
func New(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", spaHandler(opts.Assets))
	return mux
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
