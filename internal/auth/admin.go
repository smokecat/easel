package auth

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/smokecat/easel/internal/store/db"
)

// 后台主机上由 auth 提供的页面。
const (
	LoginPath     = "/auth/login"
	LogoutPath    = "/auth/logout"
	AuthorizePath = "/auth/authorize"
)

const sessionCookie = "easel_session"

func (s *Service) appSecure() bool { return s.Hosts.App.Scheme == "https" }

// HasAdminPassword 报告是否已设置管理员密码。
func (s *Service) HasAdminPassword(ctx context.Context) (bool, error) {
	_, err := s.Store.Q.GetAdmin(ctx)
	if noRows(err) {
		return false, nil
	}
	return err == nil, err
}

// SetAdminPassword 设置（或更换）管理员密码，并注销所有后台会话（连带票据和管理员的访问会话）。
func (s *Service) SetAdminPassword(ctx context.Context, pw string) error {
	if err := CheckPasswordPolicy(pw); err != nil {
		return &Denied{Status: http.StatusBadRequest, Message: err.Error()}
	}
	hash := hashPassword(pw)
	return s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.SetAdminPassword(ctx, db.SetAdminPasswordParams{PasswordHash: hash, UpdatedAt: s.nowMs()}); err != nil {
			return err
		}
		return q.DeleteAllSessions(ctx)
	})
}

// sessionFromRequest 返回请求带的有效后台会话；没有时返回 nil, nil。
func (s *Service) sessionFromRequest(r *http.Request) (*db.Session, error) {
	c, err := r.Cookie(cookieName(sessionCookie, s.appSecure()))
	if err != nil || c.Value == "" {
		return nil, nil
	}
	sess, err := s.Store.Q.GetSession(r.Context(), db.GetSessionParams{IDHash: hashToken(c.Value), ExpiresAt: s.nowMs()})
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// RegisterApp 把登录、登出、换票入口挂到后台主机的 mux 上。mux 外层必须套 Identify。
func (s *Service) RegisterApp(mux *http.ServeMux) {
	mux.HandleFunc("GET "+LoginPath, s.loginPage)
	mux.HandleFunc("POST "+LoginPath, s.login)
	mux.HandleFunc("POST "+LogoutPath, s.logout)
	mux.HandleFunc("GET "+AuthorizePath, s.authorize)
}

// RequireAdminPage 用在管理面板的页面上：已登录返回 true；否则重定向到登录页并返回 false。
func (s *Service) RequireAdminPage(w http.ResponseWriter, r *http.Request) bool {
	if FromRequest(r) != nil && FromRequest(r).Kind != KindToken {
		return true
	}
	redirectToLogin(w, r)
	return false
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, LoginPath+"?"+url.Values{"next": {r.URL.RequestURI()}}.Encode(), http.StatusFound)
}

func isAdmin(r *http.Request) bool {
	id := FromRequest(r)
	return id != nil && id.Kind == KindAdmin
}

func loginForm(next string) page {
	return page{Title: "Sign in", Form: true, Action: LoginPath, Submit: "Sign in", Hidden: map[string]string{"next": next}}
}

func (s *Service) loginPage(w http.ResponseWriter, r *http.Request) {
	next := safeReturn(r.URL.Query().Get("next"))
	if isAdmin(r) {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	ok, err := s.HasAdminPassword(r.Context())
	if err != nil {
		s.Log.Error("admin lookup", "err", err)
		renderPage(w, http.StatusInternalServerError, page{Title: "Sign in", Error: "Internal error."})
		return
	}
	if !ok {
		renderPage(w, http.StatusServiceUnavailable, page{Title: "Sign in",
			Message: "No admin password has been set. On the server, run: easel admin passwd"})
		return
	}
	renderPage(w, http.StatusOK, loginForm(next))
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	// 登录 CSRF：防止别的站点把管理员登进攻击者的账号（单管理员下意义不大，但同源校验很便宜）
	if !sameOrigin(r, s.Hosts.AppOrigin()) {
		renderPage(w, http.StatusForbidden, page{Title: "Sign in", Error: "Cross-site request rejected."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		renderPage(w, http.StatusBadRequest, page{Title: "Sign in", Error: "Invalid form."})
		return
	}
	next := safeReturn(r.PostForm.Get("next"))
	form := loginForm(next)
	now := s.now()
	admin, err := s.Store.Q.GetAdmin(r.Context())
	if noRows(err) {
		s.loginPage(w, r)
		return
	}
	if err != nil {
		s.Log.Error("admin lookup", "err", err)
		renderPage(w, http.StatusInternalServerError, page{Title: "Sign in", Error: "Internal error."})
		return
	}
	if wait := s.loginLimit.begin("admin", now); wait > 0 {
		tooManyAttempts(w, wait, form)
		return
	}
	ok, err := verifyPassword(admin.PasswordHash, r.PostForm.Get("password"))
	if err != nil {
		s.Log.Error("verify admin password", "err", err)
	}
	if !ok {
		s.Log.Warn("admin login failed")
		form.Error = "Wrong password."
		renderPage(w, http.StatusUnauthorized, form)
		return
	}
	s.loginLimit.reset("admin")

	tok := randomToken(32)
	ua := r.UserAgent()
	if len(ua) > 256 {
		ua = ua[:256]
	}
	if err := s.Store.W.CreateSession(r.Context(), db.CreateSessionParams{
		IDHash: hashToken(tok), CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(SessionTTL).UnixMilli(), UserAgent: ua,
	}); err != nil {
		s.Log.Error("create session", "err", err)
		renderPage(w, http.StatusInternalServerError, page{Title: "Sign in", Error: "Internal error."})
		return
	}
	http.SetCookie(w, newCookie(sessionCookie, tok, s.appSecure(), SessionTTL))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if id := FromRequest(r); id != nil && id.Kind == KindAdmin {
		if err := s.Store.W.DeleteSession(r.Context(), id.sessionHash); err != nil {
			s.Log.Error("delete session", "err", err)
		}
	} else if !sameOrigin(r, s.Hosts.AppOrigin()) {
		// 跨站的登出请求不处理（Identify 已经把它当成无身份）
		renderPage(w, http.StatusForbidden, page{Title: "Sign out", Error: "Cross-site request rejected."})
		return
	}
	http.SetCookie(w, newCookie(sessionCookie, "", s.appSecure(), 0))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, LoginPath, http.StatusSeeOther)
}

// authorize 给已登录的管理员签发一次性票据，跳到目标内容主机核销：
// GET /auth/authorize?host=<label>&return=<路径>。
func (s *Service) authorize(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		redirectToLogin(w, r)
		return
	}
	q := r.URL.Query()
	label := strings.ToLower(q.Get("host"))
	ret := safeReturn(q.Get("return"))
	if _, ok := s.Hosts.Label(label + "." + s.Hosts.Content.Host); !ok || !validLabel(label) {
		renderPage(w, http.StatusBadRequest, page{Title: "Open preview", Error: "Invalid host."})
		return
	}
	base, _, _ := strings.Cut(label, "--v")
	if _, err := s.Store.Q.GetPrototypeByLabel(r.Context(), base); err != nil {
		if noRows(err) {
			renderPage(w, http.StatusNotFound, page{Title: "Open preview", Error: "No such prototype."})
			return
		}
		s.Log.Error("prototype lookup", "err", err)
		renderPage(w, http.StatusInternalServerError, page{Title: "Open preview", Error: "Internal error."})
		return
	}
	id := FromRequest(r)
	tok := randomToken(32)
	if err := s.Store.W.CreateTicket(r.Context(), db.CreateTicketParams{
		IDHash: hashToken(tok), HostLabel: label, SessionHash: id.sessionHash, ExpiresAt: s.now().Add(TicketTTL).UnixMilli(),
	}); err != nil {
		s.Log.Error("create ticket", "err", err)
		renderPage(w, http.StatusInternalServerError, page{Title: "Open preview", Error: "Internal error."})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	target := s.Hosts.ContentOrigin(label) + ticketPath + "?" + url.Values{"ticket": {tok}, "return": {ret}}.Encode()
	http.Redirect(w, r, target, http.StatusFound)
}

// AuthorizeURL 返回让管理员打开某个内容主机的后台地址。
func (s *Service) AuthorizeURL(label, ret string) string {
	return s.Hosts.AppOrigin() + AuthorizePath + "?" + url.Values{"host": {label}, "return": {safeReturn(ret)}}.Encode()
}

// safeReturn 只接受站内相对路径（防开放重定向），否则返回 "/"。
func safeReturn(p string) string {
	if p == "" || p[0] != '/' || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return u.RequestURI()
}

// validLabel 校验内容主机标签的字符集：小写字母、数字、连字符。
func validLabel(l string) bool {
	if l == "" || len(l) > 63 {
		return false
	}
	for _, c := range l {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
