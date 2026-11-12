package cli

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testAdminPassword = "correct horse battery"

// browser 模拟一个浏览器：有自己的 cookie jar，直接调 TCP handler（按 Host 分流），不跟随重定向。
type browser struct {
	t   *testing.T
	h   http.Handler
	jar *cookiejar.Jar
}

func newBrowser(t *testing.T, h http.Handler) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, h: h, jar: jar}
}

func (b *browser) do(method, rawURL string, body io.Reader, hdr ...string) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(method, rawURL, body)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	for _, c := range b.jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	b.jar.SetCookies(req.URL, rec.Result().Cookies())
	return rec
}

func (b *browser) get(rawURL string, hdr ...string) *httptest.ResponseRecorder {
	return b.do("GET", rawURL, nil, hdr...)
}

// postForm 以同源表单提交（带 Origin 头，和浏览器一样）。
func (b *browser) postForm(rawURL string, form url.Values) *httptest.ResponseRecorder {
	u, _ := url.Parse(rawURL)
	return b.do("POST", rawURL, strings.NewReader(form.Encode()),
		"Content-Type", "application/x-www-form-urlencoded", "Origin", u.Scheme+"://"+u.Host)
}

// follow 从 loc 开始跟随重定向，直到响应不再是重定向、或者重定向回到 easel 自己以外的路径
// （即换票完成、要回原页面）为止，返回最后一个响应。
func (b *browser) follow(loc string) *httptest.ResponseRecorder {
	b.t.Helper()
	for range 10 {
		rec := b.get(loc)
		next := rec.Header().Get("Location")
		if rec.Code < 300 || rec.Code >= 400 || next == "" {
			return rec
		}
		base, _ := url.Parse(loc)
		nu, _ := base.Parse(next)
		if !strings.HasPrefix(nu.Path, "/auth/") && !strings.HasPrefix(nu.Path, "/_easel/") {
			return rec
		}
		loc = nu.String()
	}
	b.t.Fatal("too many redirects")
	return nil
}

// login 用管理员密码登录后台。
func (b *browser) login(appURL, password string) *httptest.ResponseRecorder {
	return b.postForm(appURL+"/auth/login", url.Values{"password": {password}, "next": {"/"}})
}

// admin 返回已登录的管理员浏览器（第一次调用时设置密码并登录）。
func (s *testServer) admin() *browser {
	s.t.Helper()
	if s.adminBrowser != nil {
		return s.adminBrowser
	}
	if err := s.inst.auth.SetAdminPassword(s.t.Context(), testAdminPassword); err != nil {
		s.t.Fatal(err)
	}
	b := newBrowser(s.t, s.inst.tcp)
	if rec := b.login(s.inst.hosts.AppOrigin(), testAdminPassword); rec.Code != http.StatusSeeOther {
		s.t.Fatalf("admin login = %d %s", rec.Code, rec.Body.String())
	}
	s.adminBrowser = b
	return b
}
