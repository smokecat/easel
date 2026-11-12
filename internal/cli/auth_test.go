package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// run 运行一条 easel 命令，stdin 可选。
func (s *testServer) run(stdin string, args ...string) (int, string, string) {
	s.t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), &Env{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb}, args)
	return code, out.String(), errb.String()
}

// runJSON 运行命令（自动加 --json 和 --socket），解析 stdout。
func (s *testServer) runJSON(stdin string, args ...string) (int, map[string]any) {
	s.t.Helper()
	code, out, errs := s.run(stdin, append(args, "--json", "--socket", s.socket)...)
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		s.t.Fatalf("%v: stdout is not JSON: %q (stderr %q)", args, out, errs)
	}
	return code, m
}

// pushSite 推送一个最小站点，返回预览地址（不带结尾斜杠）。
func (s *testServer) pushSite(name, body string) string {
	s.t.Helper()
	dir := s.t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0o644); err != nil {
		s.t.Fatal(err)
	}
	code, res, stderr := s.push(dir, "--name", name)
	if code != 0 {
		s.t.Fatalf("push %s = %d %v %s", name, code, res, stderr)
	}
	if res["access"].(map[string]any)["preview_requires_login"] != true {
		s.t.Errorf("preview_requires_login = %v, want true", res["access"])
	}
	return strings.TrimSuffix(dig(res, "urls", "preview").(string), "/")
}

func TestAdminSignInFlow(t *testing.T) {
	s := newTestServer(t)
	preview := s.pushSite("site", "hello admin")
	app := s.inst.hosts.AppOrigin()

	b := newBrowser(t, s.inst.tcp)
	// 未登录：内容主机送回后台换票，后台再送去登录页
	rec := b.get(preview + "/page?x=1")
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), app+"/auth/authorize?") {
		t.Fatalf("anonymous content = %d %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("gate redirect lacks security headers: %v", rec.Header())
	}
	authorize := rec.Header().Get("Location")
	rec = b.get(authorize)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/login?next=") {
		t.Fatalf("authorize without session = %d %v", rec.Code, rec.Header())
	}
	// 不存在的原型也一样跳转，不暴露存在性
	if rec := b.get("http://nope-aaaaaa.localhost:7780/"); rec.Code != http.StatusFound {
		t.Errorf("unknown prototype = %d, want 302", rec.Code)
	}
	// 管理面板页面要求登录
	if rec := b.get(app + "/"); rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/login") {
		t.Errorf("admin UI without login = %d %v", rec.Code, rec.Header())
	}
	// 还没设密码时登录页提示去设
	if rec := b.get(app + "/auth/login"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "easel admin passwd") {
		t.Errorf("login without password = %d %q", rec.Code, rec.Body.String())
	}

	// 用 CLI 设密码（只能经 socket）
	if code, _, errs := s.run(testAdminPassword+"\n", "admin", "passwd", "--password-stdin", "--socket", s.socket); code != 0 {
		t.Fatalf("admin passwd = %d %s", code, errs)
	}
	if code, _, _ := s.run("short\n", "admin", "passwd", "--password-stdin", "--socket", s.socket); code == 0 {
		t.Error("too-short password accepted")
	}

	// 跨站登录拒绝；错密码 401；对的密码 303 回到 next
	if rec := b.do("POST", app+"/auth/login", strings.NewReader("password="+url.QueryEscape(testAdminPassword)),
		"Content-Type", "application/x-www-form-urlencoded", "Origin", "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site login = %d, want 403", rec.Code)
	}
	// 登录页不能是 no-referrer（否则浏览器发 Origin: null）；万一是 null，靠 Sec-Fetch-Site 放行
	if rp := b.get(app + "/auth/login").Header().Get("Referrer-Policy"); rp != "same-origin" {
		t.Errorf("login page Referrer-Policy = %q", rp)
	}
	nb := newBrowser(t, s.inst.tcp)
	if rec := nb.do("POST", app+"/auth/login", strings.NewReader("next=%2F&password="+url.QueryEscape(testAdminPassword)),
		"Content-Type", "application/x-www-form-urlencoded", "Origin", "null", "Sec-Fetch-Site", "same-origin"); rec.Code != http.StatusSeeOther {
		t.Errorf("login with Origin: null + Sec-Fetch-Site: same-origin = %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.login(app, "wrong password"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password = %d, want 401", rec.Code)
	}
	next := strings.TrimPrefix(authorize, app)
	rec = b.postForm(app+"/auth/login", url.Values{"password": {testAdminPassword}, "next": {next}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != next {
		t.Fatalf("login = %d %v", rec.Code, rec.Header())
	}
	sc := rec.Header().Get("Set-Cookie")
	if !strings.Contains(sc, "easel_session=") || !strings.Contains(sc, "HttpOnly") || !strings.Contains(sc, "SameSite=Lax") || strings.Contains(sc, "Domain") {
		t.Errorf("session cookie = %q", sc)
	}

	// 换票：authorize → /_easel/auth → 原页面
	rec = b.get(authorize)
	ticketURL := rec.Header().Get("Location")
	if rec.Code != http.StatusFound || !strings.HasPrefix(ticketURL, preview+"/_easel/auth?") || !strings.Contains(ticketURL, "ticket=") {
		t.Fatalf("authorize = %d %q", rec.Code, ticketURL)
	}
	rec = b.get(ticketURL)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/page?x=1" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("redeem = %d %v", rec.Code, rec.Header())
	}
	if rec := b.get(preview + "/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "hello admin") {
		t.Fatalf("content after sign-in = %d %q", rec.Code, rec.Body.String())
	}
	// 票据一次性
	other := newBrowser(t, s.inst.tcp)
	if rec := other.get(ticketURL); rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/auth/authorize") {
		t.Errorf("reused ticket = %d %v", rec.Code, rec.Header())
	}
	// 票据绑定主机：给 site 的票据不能在别的原型上用
	s.pushSite("other", "other")
	rec = b.get(authorize)
	u, _ := url.Parse(rec.Header().Get("Location"))
	label := s.labelOf("other")
	u.Host = label + ".localhost:7780"
	if rec := other.get(u.String()); rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/auth/authorize") {
		t.Errorf("ticket on wrong host = %d %v", rec.Code, rec.Header())
	}
	// 内容主机的 cookie 只对本主机有效
	if rec := b.get("http://" + label + ".localhost:7780/"); rec.Code != http.StatusFound {
		t.Errorf("cookie leaked to another prototype host: %d", rec.Code)
	}

	// 后台会话也能调 API；非 GET 必须同源
	if rec := b.get(app + "/api/v1/whoami"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"kind":"admin"`) {
		t.Errorf("whoami with session = %d %s", rec.Code, rec.Body.String())
	}
	body := `{"name":"x"}`
	if rec := b.do("POST", app+"/api/v1/tokens", strings.NewReader(body), "Content-Type", "application/json", "Origin", "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site API POST = %d, want 403", rec.Code)
	}
	if rec := b.do("POST", app+"/api/v1/tokens", strings.NewReader(body), "Content-Type", "application/json", "Origin", app); rec.Code != http.StatusCreated {
		t.Errorf("same-origin API POST = %d %s", rec.Code, rec.Body.String())
	}

	// 登出：后台会话和它派生的内容访问会话一起失效
	if rec := b.do("POST", app+"/auth/logout", nil, "Origin", app); rec.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := b.get(preview + "/"); rec.Code != http.StatusFound {
		t.Errorf("content after logout = %d, want 302", rec.Code)
	}

	// 改密码注销所有会话
	b2 := newBrowser(t, s.inst.tcp)
	b2.login(app, testAdminPassword)
	if rec := b2.get(app + "/api/v1/whoami"); rec.Code != 200 {
		t.Fatalf("whoami = %d", rec.Code)
	}
	if code, _, errs := s.run("another password 2\n", "admin", "passwd", "--password-stdin", "--socket", s.socket); code != 0 {
		t.Fatalf("admin passwd = %d %s", code, errs)
	}
	if rec := b2.get(app + "/api/v1/whoami"); rec.Code != http.StatusUnauthorized {
		t.Errorf("whoami after password change = %d, want 401", rec.Code)
	}
}

func (s *testServer) labelOf(name string) string {
	s.t.Helper()
	p, err := s.inst.store.Q.GetPrototypeByName(s.t.Context(), name)
	if err != nil {
		s.t.Fatal(err)
	}
	return p.Label
}

func TestLoginRateLimit(t *testing.T) {
	s := newTestServer(t)
	s.admin() // 设好密码
	app := s.inst.hosts.AppOrigin()
	b := newBrowser(t, s.inst.tcp)
	for i := range 10 {
		if rec := b.login(app, "nope nope"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", i, rec.Code)
		}
	}
	rec := b.login(app, testAdminPassword)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("after 10 failures = %d, want 429", rec.Code)
	}
	s.inst.auth.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if rec := b.login(app, testAdminPassword); rec.Code != http.StatusSeeOther {
		t.Errorf("after lockout = %d", rec.Code)
	}
}

func TestShareLinks(t *testing.T) {
	s := newTestServer(t)
	preview := s.pushSite("site", "hello visitor")
	s.pushSite("other", "other")

	code, sh := s.runJSON("", "share", "create", "site", "--note", "for bob")
	if code != 0 || sh["ok"] != true {
		t.Fatalf("share create = %d %v", code, sh)
	}
	link := sh["url"].(string)
	if !strings.HasPrefix(link, preview+"/_easel/s/") || sh["has_password"] != false || sh["status"] != "active" {
		t.Fatalf("share = %v", sh)
	}
	if code, res := s.runJSON("", "share", "create", "missing"); code != ExitNotFound {
		t.Errorf("share for missing prototype = %d %v", code, res)
	}

	// 访客：入口换 cookie，302 到干净的 URL
	v := newBrowser(t, s.inst.tcp)
	rec := v.get(link)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("open share = %d %v", rec.Code, rec.Header())
	}
	if rec := v.get(preview + "/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "hello visitor") {
		t.Fatalf("visitor content = %d %q", rec.Code, rec.Body.String())
	}
	// 只对这个原型的主地址有效
	otherHost := "http://" + s.labelOf("other") + ".localhost:7780"
	if rec := v.get(otherHost + "/"); rec.Code != http.StatusFound {
		t.Errorf("visitor on other prototype = %d", rec.Code)
	}
	if rec := v.get(otherHost + strings.TrimPrefix(link, preview)); rec.Code != http.StatusNotFound {
		t.Errorf("share token on other host = %d, want 404", rec.Code)
	}
	if rec := v.get(preview + "/_easel/s/bogus"); rec.Code != http.StatusNotFound {
		t.Errorf("bogus share = %d", rec.Code)
	}

	// 列表里能再次拿到同一个链接
	_, list := s.runJSON("", "share", "list", "site")
	shares := list["shares"].([]any)
	if len(shares) != 1 || shares[0].(map[string]any)["url"] != link {
		t.Errorf("share list = %v", list)
	}
	if code, out, _ := s.run("", "share", "list", "--socket", s.socket); code != 0 || !strings.Contains(out, link) || !strings.Contains(out, "for bob") {
		t.Errorf("share list table = %d %q", code, out)
	}

	// 撤销立即生效
	id := int64(sh["id"].(float64))
	if code, _ := s.runJSON("", "share", "revoke", itoa(id)); code != 0 {
		t.Fatalf("revoke = %d", code)
	}
	if rec := v.get(preview + "/"); rec.Code != http.StatusFound {
		t.Errorf("content after revoke = %d, want 302", rec.Code)
	}
	if rec := newBrowser(t, s.inst.tcp).get(link); rec.Code != http.StatusNotFound {
		t.Errorf("revoked link = %d, want 404", rec.Code)
	}
	if code, _ := s.runJSON("", "share", "revoke", itoa(id)); code != ExitNotFound {
		t.Errorf("revoke twice = %d", code)
	}

	// 带密码、带有效期
	code, sh = s.runJSON("visitor secret\n", "share", "create", "site", "--password-stdin", "--expires", "2h")
	if code != 0 || sh["has_password"] != true || sh["expires_at"] == nil {
		t.Fatalf("password share = %d %v", code, sh)
	}
	link = sh["url"].(string)
	v = newBrowser(t, s.inst.tcp)
	if rec := v.get(link); rec.Code != 200 || !strings.Contains(rec.Body.String(), `type="password"`) {
		t.Fatalf("password page = %d %q", rec.Code, rec.Body.String())
	}
	if rec := v.do("POST", link, strings.NewReader("password=visitor+secret"), "Content-Type", "application/x-www-form-urlencoded", "Origin", "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site password POST = %d", rec.Code)
	}
	if rec := v.postForm(link, url.Values{"password": {"wrong"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong share password = %d", rec.Code)
	}
	if rec := v.get(preview + "/"); rec.Code != http.StatusFound {
		t.Errorf("content before password = %d", rec.Code)
	}
	if rec := v.postForm(link, url.Values{"password": {"visitor secret"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("right share password = %d %q", rec.Code, rec.Body.String())
	}
	if rec := v.get(preview + "/"); rec.Code != 200 {
		t.Errorf("content after password = %d", rec.Code)
	}
	// 过期后立即失效（访问会话还没到 24 小时也不行）
	s.inst.auth.Now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	if rec := v.get(preview + "/"); rec.Code != http.StatusFound {
		t.Errorf("content after expiry = %d, want 302", rec.Code)
	}
	s.inst.auth.Now = time.Now

	// 密码限速按链接计数
	v2 := newBrowser(t, s.inst.tcp)
	for range 10 {
		v2.postForm(link, url.Values{"password": {"wrong"}})
	}
	if rec := v2.postForm(link, url.Values{"password": {"visitor secret"}}); rec.Code != http.StatusTooManyRequests {
		t.Errorf("after 10 failures = %d, want 429", rec.Code)
	}
}

func TestTokensOverTCP(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(s.inst.tcp)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("remote"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 无 token：退出码 3 并提示登录
	code, _, errs := s.run("", "push", dir, "--name", "remote", "--server", srv.URL)
	if code != ExitAuth || !strings.Contains(errs, "easel login") {
		t.Errorf("push without token = %d %q", code, errs)
	}
	// 没有 socket、也没登录
	if code, _, errs := s.run("", "push", dir, "--name", "remote"); code != ExitAuth || !strings.Contains(errs, "easel login") {
		t.Errorf("push with nothing configured = %d %q", code, errs)
	}
	// 明文 http 只允许回环地址
	if code, _, _ := s.run("", "login", "--server", "http://easel.example.com", "--token-stdin"); code != ExitUsage {
		t.Errorf("login over plain http = %d, want 2", code)
	}

	// 经 socket 创建 token
	code, tok := s.runJSON("", "token", "create", "--name", "laptop", "--expires", "30d")
	if code != 0 || !strings.HasPrefix(tok["token"].(string), "easel_pat_") {
		t.Fatalf("token create = %d %v", code, tok)
	}
	if scopes := tok["scopes"].([]any); len(scopes) != 2 {
		t.Errorf("default scopes = %v", scopes)
	}
	plain := tok["token"].(string)

	// login 校验 token 并保存凭据（0600）；之后不带参数就用它
	code, out, errs := s.run(plain+"\n", "login", "--server", srv.URL, "--token-stdin")
	if code != 0 {
		t.Fatalf("login = %d %s %s", code, out, errs)
	}
	st, err := os.Stat(os.Getenv("EASEL_CREDENTIALS"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("credentials file: %v %v", err, st)
	}
	code, out, errs = s.run("", "push", dir, "--name", "remote", "--json")
	if code != 0 || !strings.Contains(out, `"preview_requires_login": true`) {
		t.Fatalf("remote push = %d %s %s", code, out, errs)
	}
	// 错误的 token 登录失败、不覆盖凭据
	if code, _, _ := s.run("easel_pat_bogus\n", "login", "--server", srv.URL, "--token-stdin"); code != ExitAuth {
		t.Errorf("login with bad token = %d, want 3", code)
	}

	// token 不能管理 token，也没有 share 权限
	if code, _, errs := s.run("", "token", "list"); code != ExitAuth || !strings.Contains(errs, "API tokens cannot") {
		t.Errorf("token list with token = %d %q", code, errs)
	}
	if code, _, errs := s.run("", "share", "create", "remote"); code != ExitAuth || !strings.Contains(errs, "share scope") {
		t.Errorf("share without scope = %d %q", code, errs)
	}
	// 只读 token 不能推送
	_, ro := s.runJSON("", "token", "create", "--name", "ro", "--scope", "read")
	t.Setenv("EASEL_TOKEN", ro["token"].(string))
	if code, _, errs := s.run("", "push", dir, "--name", "remote"); code != ExitAuth || !strings.Contains(errs, "write scope") {
		t.Errorf("push with read-only token = %d %q", code, errs)
	}
	t.Setenv("EASEL_TOKEN", "")

	// 撤销后立即失效
	if code, _ := s.runJSON("", "token", "revoke", itoa(int64(tok["id"].(float64)))); code != 0 {
		t.Fatal("revoke failed")
	}
	if code, _, errs := s.run("", "push", dir, "--name", "remote"); code != ExitAuth || !strings.Contains(errs, "invalid API token") {
		t.Errorf("push with revoked token = %d %q", code, errs)
	}
	_, list := s.runJSON("", "token", "list")
	if ts := list["tokens"].([]any); len(ts) != 2 || ts[0].(map[string]any)["status"] != "revoked" || ts[0].(map[string]any)["last_used_at"] == nil {
		t.Errorf("token list = %v", list)
	}
	// 改密码只能经 socket
	req, _ := http.NewRequest("PUT", srv.URL+"/api/v1/admin/password", strings.NewReader(`{"password":"whatever123"}`))
	req.Header.Set("Authorization", "Bearer "+ro["token"].(string))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("password over TCP = %d, want 403", resp.StatusCode)
	}

	// logout 删除凭据
	if code, _, _ := s.run("", "logout"); code != 0 {
		t.Error("logout failed")
	}
	if _, err := os.Stat(os.Getenv("EASEL_CREDENTIALS")); !os.IsNotExist(err) {
		t.Errorf("credentials still there: %v", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
