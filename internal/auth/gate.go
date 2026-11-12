package auth

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/smokecat/easel/internal/store/db"
)

// 内容主机上 easel 自己的路径（原型文件不能以 _easel/ 开头）。
const (
	reservedPrefix = "/_easel/"
	ticketPath     = "/_easel/auth"
)

const viewCookie = "easel_view"

func (s *Service) contentSecure() bool { return s.Hosts.Content.Scheme == "https" }

// Gate 处理内容主机上的请求：/_easel/ 下的换票、分享入口由它自己处理；其余请求校验访问 cookie，
// 有效才交给 next，否则把浏览器送回后台登录（不管原型存不存在，不暴露存在性）。
// 交给 next 之前从请求里剔除 easel 自己的 cookie，原型（以及以后的代理上游）看不到它。
func (s *Service) Gate(label string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 门卫自己的响应（重定向、密码页、错误）也带基本的安全头；内容响应会再覆盖一遍
		hdr := w.Header()
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Content-Security-Policy", "frame-ancestors 'none'")
		hdr.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		if s.contentSecure() {
			hdr.Set("Strict-Transport-Security", "max-age=63072000")
		}
		if p := r.URL.Path; p == "/_easel" || strings.HasPrefix(p, reservedPrefix) {
			s.serveReserved(w, r, label)
			return
		}
		ok, err := s.viewAllowed(r, label)
		if err != nil {
			s.Log.Error("view session lookup", "label", label, "err", err)
			http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
			return
		}
		if !ok {
			s.denyContent(w, r, label)
			return
		}
		stripCookie(r, cookieName(viewCookie, s.contentSecure()))
		next.ServeHTTP(w, r)
	})
}

func (s *Service) viewAllowed(r *http.Request, label string) (bool, error) {
	c, err := r.Cookie(cookieName(viewCookie, s.contentSecure()))
	if err != nil || c.Value == "" {
		return false, nil
	}
	now := s.now()
	vs, err := s.Store.Q.GetViewSession(r.Context(), db.GetViewSessionParams{
		IDHash: hashToken(c.Value), HostLabel: label, ExpiresAt: now.UnixMilli(),
	})
	if noRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !vs.ShareID.Valid {
		return true, nil // 管理员的访问会话随后台会话删除（外键级联），过期已由查询条件排除
	}
	// 分享的状态每个请求都查：撤销、过期立即生效
	sh, err := s.Store.Q.GetShare(r.Context(), vs.ShareID.Int64)
	if noRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return shareActive(sh, now), nil
}

// denyContent 处理没有访问权的请求：页面导航送去后台换票，其余请求直接 401。
func (s *Service) denyContent(w http.ResponseWriter, r *http.Request, label string) {
	hdr := w.Header()
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, s.AuthorizeURL(label, r.URL.RequestURI()), http.StatusFound)
}

func (s *Service) serveReserved(w http.ResponseWriter, r *http.Request, label string) {
	switch p := r.URL.Path; {
	case p == ticketPath && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.redeemTicket(w, r, label)
	case strings.HasPrefix(p, SharePathPrefix) && (r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodPost):
		s.openShare(w, r, label, strings.TrimPrefix(p, SharePathPrefix))
	default:
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "404 page not found", http.StatusNotFound)
	}
}

// redeemTicket 核销后台签发的一次性票据，设置本主机的访问 cookie 后跳到干净的原 URL。
func (s *Service) redeemTicket(w http.ResponseWriter, r *http.Request, label string) {
	q := r.URL.Query()
	ret := safeReturn(q.Get("return"))
	now := s.now()
	var t db.Ticket
	var sess db.Session
	err := s.Store.Tx(r.Context(), func(qq *db.Queries) error {
		var err error
		if t, err = qq.TakeTicket(r.Context(), hashToken(q.Get("ticket"))); err != nil {
			return err
		}
		sess, err = qq.GetSession(r.Context(), db.GetSessionParams{IDHash: t.SessionHash, ExpiresAt: now.UnixMilli()})
		return err
	})
	if err != nil && !noRows(err) {
		s.Log.Error("redeem ticket", "err", err)
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	}
	if err != nil || t.HostLabel != label || t.ExpiresAt <= now.UnixMilli() {
		// 票据无效、过期、用过、或者是给别的主机的：重新走一遍授权
		s.denyContent(w, withPath(r, ret), label)
		return
	}
	expires := min(now.Add(AdminViewTTL).UnixMilli(), sess.ExpiresAt)
	if !s.startView(w, r, label, sql.NullString{String: sess.IDHash, Valid: true}, sql.NullInt64{}, now, expires) {
		return
	}
	http.Redirect(w, r, ret, http.StatusFound)
}

// openShare 是分享入口：校验令牌，需要密码时出密码页，通过后设置访问 cookie 并跳到首页。
func (s *Service) openShare(w http.ResponseWriter, r *http.Request, label, token string) {
	now := s.now()
	sh, err := s.Store.Q.GetShareByTokenHash(r.Context(), hashToken(token))
	if err != nil && !noRows(err) {
		s.Log.Error("share lookup", "err", err)
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	}
	valid := err == nil && shareActive(sh, now)
	if valid {
		// 分享只对原型的主地址有效；拿到 A 的令牌去 B 的主机上用不行
		proto, err := s.Store.Q.GetPrototype(r.Context(), sh.PrototypeID)
		if err != nil && !noRows(err) {
			s.Log.Error("prototype lookup", "err", err)
			http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
			return
		}
		valid = err == nil && proto.Label == label
	}
	if !valid {
		renderPage(w, http.StatusNotFound, page{Title: "Link unavailable",
			Message: "This share link is invalid, has expired, or has been revoked. Ask the person who shared it for a new link."})
		return
	}

	if sh.PasswordHash != "" {
		form := page{Title: "Password required", Form: true, Action: SharePathPrefix + token, Submit: "Continue",
			Message: "This preview is password protected."}
		if r.Method != http.MethodPost {
			renderPage(w, http.StatusOK, form)
			return
		}
		if !sameOrigin(r, s.Hosts.ContentOrigin(label)) {
			form.Error = "Cross-site request rejected."
			renderPage(w, http.StatusForbidden, form)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if err := r.ParseForm(); err != nil {
			form.Error = "Invalid form."
			renderPage(w, http.StatusBadRequest, form)
			return
		}
		key := "share:" + sh.TokenHash
		if wait := s.shareLimit.begin(key, now); wait > 0 {
			tooManyAttempts(w, wait, form)
			return
		}
		ok, err := verifyPassword(sh.PasswordHash, r.PostForm.Get("password"))
		if err != nil {
			s.Log.Error("verify share password", "share", sh.ID, "err", err)
		}
		if !ok {
			s.Log.Warn("share password failed", "share", sh.ID)
			form.Error = "Wrong password."
			renderPage(w, http.StatusUnauthorized, form)
			return
		}
		s.shareLimit.reset(key)
	}

	expires := now.Add(ShareViewTTL).UnixMilli()
	if sh.ExpiresAt.Valid {
		expires = min(expires, sh.ExpiresAt.Int64)
	}
	if !s.startView(w, r, label, sql.NullString{}, sql.NullInt64{Int64: sh.ID, Valid: true}, now, expires) {
		return
	}
	// 跳到干净的 URL：令牌只出现在入口这一跳，之后的页面地址、Referer 里都没有它
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// startView 创建访问会话并设置 cookie；失败时已经写好错误响应，返回 false。
func (s *Service) startView(w http.ResponseWriter, r *http.Request, label string, sess sql.NullString, share sql.NullInt64, now time.Time, expiresMs int64) bool {
	tok := randomToken(32)
	err := s.Store.Tx(r.Context(), func(q *db.Queries) error {
		return q.CreateViewSession(r.Context(), db.CreateViewSessionParams{
			IDHash: hashToken(tok), HostLabel: label, SessionHash: sess, ShareID: share,
			CreatedAt: now.UnixMilli(), ExpiresAt: expiresMs,
		})
	})
	if err != nil {
		s.Log.Error("create view session", "err", err)
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return false
	}
	ttl := time.UnixMilli(expiresMs).Sub(now)
	http.SetCookie(w, newCookie(viewCookie, tok, s.contentSecure(), ttl))
	hdr := w.Header()
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Referrer-Policy", "no-referrer")
	return true
}

func withPath(r *http.Request, requestURI string) *http.Request {
	u, err := url.Parse(requestURI)
	if err != nil {
		return r
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path, r2.URL.RawPath, r2.URL.RawQuery = u.Path, u.RawPath, u.RawQuery
	return r2
}

// stripCookie 从请求的 Cookie 头里删掉名为 name 的 cookie。
func stripCookie(r *http.Request, name string) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	var kept []string
	for _, c := range cookies {
		if c.Name != name {
			kept = append(kept, c.Name+"="+c.Value)
		}
	}
	if len(kept) > 0 {
		r.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}
