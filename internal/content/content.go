// Package content 在内容主机上提供静态版本和素材：安全响应头、缓存、Range、SPA 回退。
//
// 原型内容一律视为不可信，只在内容主机上提供；这里不读也不发后台的 cookie。
// 请求路径只用来查 manifest，从不拼进文件系统路径。
package content

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smokecat/easel/internal/blob"
	"github.com/smokecat/easel/internal/config"
	"github.com/smokecat/easel/internal/publish"
	"github.com/smokecat/easel/internal/store"
	"github.com/smokecat/easel/internal/store/db"
)

// Handler 提供内容主机上的请求。
type Handler struct {
	Store *store.Store
	Blobs *blob.Store
	Hosts config.Hosts
	Log   *slog.Logger

	mu    sync.Mutex
	cache map[int64]*manifest // 版本不可变，按版本 ID 缓存永远有效
}

const maxCachedManifests = 128

type manifest struct {
	rev   db.Revision
	files map[string]db.Artifact
	list  []db.Artifact // 按路径排序
}

func (m *manifest) spa() bool {
	if m.rev.Spa != 0 {
		return true
	}
	// 没有顶层 404.html 的站点视为 SPA（Cloudflare Pages 的约定）
	_, has404 := m.files["404.html"]
	return m.rev.Kind == publish.KindSite && !has404
}

// ServeHTTP 处理一个内容主机上的请求；label 是主机名的一级标签。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, label string) {
	h.setSecurityHeaders(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		h.plainError(w, http.StatusMethodNotAllowed)
		return
	}
	// 默认不让原型注册 Service Worker：撤销访问后它还能离线展示缓存的内容
	if r.Header.Get("Service-Worker") == "script" {
		h.plainError(w, http.StatusForbidden)
		return
	}

	m, pinned, err := h.lookup(r.Context(), label)
	if errors.Is(err, sql.ErrNoRows) {
		h.plainError(w, http.StatusNotFound)
		return
	}
	if err != nil {
		h.Log.Error("content lookup", "label", label, "err", err)
		h.plainError(w, http.StatusInternalServerError)
		return
	}

	reqPath := r.URL.Path
	if reqPath == "/"+publish.ReservedPrefix || strings.HasPrefix(reqPath, "/"+publish.ReservedPrefix+"/") {
		h.plainError(w, http.StatusNotFound) // 留给换票、密码页等，后续任务实现
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+reqPath), "/")
	dirReq := strings.HasSuffix(reqPath, "/")

	cacheControl := "private, no-cache"
	if pinned {
		cacheControl = "private, max-age=31536000, immutable"
	}

	switch {
	case p == "" && m.rev.Kind == publish.KindAssets:
		w.Header().Set("Cache-Control", cacheControl)
		h.serveListing(w, r, m)
		return
	case p == "":
		if a, ok := m.files[m.rev.Entry]; ok {
			h.serveArtifact(w, r, a, http.StatusOK, cacheControl)
			return
		}
	case dirReq:
		if a, ok := m.files[p+"/index.html"]; ok {
			h.serveArtifact(w, r, a, http.StatusOK, cacheControl)
			return
		}
	default:
		if a, ok := m.files[p]; ok {
			h.serveArtifact(w, r, a, http.StatusOK, cacheControl)
			return
		}
		if a, ok := m.files[p+".html"]; ok {
			h.serveArtifact(w, r, a, http.StatusOK, cacheControl)
			return
		}
		if _, ok := m.files[p+"/index.html"]; ok {
			// 补上结尾斜杠，页面里的相对链接才能按目录解析
			target := "/" + p + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			w.Header().Set("Cache-Control", cacheControl)
			http.Redirect(w, r, (&url.URL{Path: target}).String(), http.StatusPermanentRedirect)
			return
		}
	}
	h.notFound(w, r, m, p, cacheControl)
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request, m *manifest, p, cacheControl string) {
	// 向上找最近的 404.html
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		candidate := "404.html"
		if dir != "." && dir != "/" {
			candidate = dir + "/404.html"
		}
		if a, ok := m.files[candidate]; ok {
			h.serveArtifact(w, r, a, http.StatusNotFound, cacheControl)
			return
		}
		if dir == "." || dir == "/" {
			break
		}
	}
	// SPA 回退只对像页面路由的路径（最后一段没有扩展名）生效，缺失的 .js 不会拿到 HTML
	if m.spa() && path.Ext(p) == "" {
		if a, ok := m.files[m.rev.Entry]; ok {
			h.serveArtifact(w, r, a, http.StatusOK, cacheControl)
			return
		}
	}
	h.plainError(w, http.StatusNotFound)
}

// lookup 按标签找到要提供的版本。<label>--v<N> 固定到第 N 版（pinned）。
func (h *Handler) lookup(ctx context.Context, label string) (m *manifest, pinned bool, err error) {
	base, num, pinned := strings.Cut(label, "--v")
	proto, err := h.Store.Q.GetPrototypeByLabel(ctx, base)
	if err != nil {
		return nil, false, err
	}
	var rev db.Revision
	if pinned {
		n, perr := strconv.ParseInt(num, 10, 64)
		if perr != nil || n <= 0 || strconv.FormatInt(n, 10) != num {
			return nil, false, sql.ErrNoRows
		}
		rev, err = h.Store.Q.GetRevisionByNumber(ctx, db.GetRevisionByNumberParams{PrototypeID: proto.ID, Number: n})
	} else {
		if !proto.CurrentRevisionID.Valid {
			return nil, false, sql.ErrNoRows
		}
		rev, err = h.Store.Q.GetRevision(ctx, proto.CurrentRevisionID.Int64)
	}
	if err != nil {
		return nil, false, err
	}
	m, err = h.manifest(ctx, rev)
	return m, pinned, err
}

func (h *Handler) manifest(ctx context.Context, rev db.Revision) (*manifest, error) {
	h.mu.Lock()
	m, ok := h.cache[rev.ID]
	h.mu.Unlock()
	if ok {
		return m, nil
	}
	list, err := h.Store.Q.ListArtifacts(ctx, rev.ID)
	if err != nil {
		return nil, err
	}
	m = &manifest{rev: rev, list: list, files: make(map[string]db.Artifact, len(list))}
	for _, a := range list {
		m.files[a.Path] = a
	}
	h.mu.Lock()
	if h.cache == nil {
		h.cache = map[int64]*manifest{}
	}
	if len(h.cache) >= maxCachedManifests {
		for k := range h.cache { // map 遍历顺序随机，相当于随机淘汰
			delete(h.cache, k)
			break
		}
	}
	h.cache[rev.ID] = m
	h.mu.Unlock()
	return m, nil
}

func (h *Handler) serveArtifact(w http.ResponseWriter, r *http.Request, a db.Artifact, status int, cacheControl string) {
	f, err := h.Blobs.Open(a.BlobHash)
	if err != nil {
		h.Log.Error("open blob", "hash", a.BlobHash, "path", a.Path, "err", err)
		h.plainError(w, http.StatusInternalServerError)
		return
	}
	defer f.Close()

	hdr := w.Header()
	hdr.Set("Content-Type", a.ContentType)
	if a.ContentType == publish.OctetStream {
		hdr.Set("Content-Disposition", attachment(path.Base(a.Path)))
	}
	if status != http.StatusOK {
		// 404 页不参与条件请求和 Range，直接给全文
		hdr.Set("Cache-Control", "no-store")
		hdr.Set("Content-Length", strconv.FormatInt(a.Size, 10))
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, f)
		}
		return
	}
	hdr.Set("ETag", `"`+a.BlobHash+`"`)
	hdr.Set("Cache-Control", cacheControl)
	// ETag 已设置，ServeContent 处理 If-None-Match / If-Range / Range；*os.File 让 Go 走 sendfile
	http.ServeContent(w, r, "", time.Time{}, f)
}

// attachment 按 RFC 6266 编码文件名。
func attachment(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(name))
}

func (h *Handler) setSecurityHeaders(hdr http.Header) {
	hdr.Set("X-Content-Type-Options", "nosniff")
	// 不限制 script-src 之类，否则原型会坏；隔离靠源。只限制谁能把原型嵌进 iframe
	hdr.Set("Content-Security-Policy", "frame-ancestors 'self' "+h.Hosts.AppOrigin())
	hdr.Set("Referrer-Policy", "same-origin")
	hdr.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	hdr.Set("Cross-Origin-Resource-Policy", "same-origin")
	if h.Hosts.Content.Scheme == "https" {
		hdr.Set("Strict-Transport-Security", "max-age=63072000")
	}
}

func (h *Handler) plainError(w http.ResponseWriter, status int) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/plain; charset=utf-8")
	hdr.Set("Cache-Control", "no-store")
	hdr.Del("ETag")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "%d %s\n", status, http.StatusText(status))
}
