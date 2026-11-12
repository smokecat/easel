package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smokecat/easel/internal/proxy"
)

// devServer 模拟一个 dev server：记录收到的请求头，提供重定向、Set-Cookie、HSTS 和一个回显 WebSocket。
type devServer struct {
	srv  *httptest.Server
	port int

	mu   sync.Mutex
	last http.Header
	host string
}

func newDevServer(t *testing.T) *devServer {
	d := &devServer{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.last, d.host = r.Header.Clone(), r.Host
		d.mu.Unlock()
		switch r.URL.Path {
		case "/redir":
			http.Redirect(w, r, fmt.Sprintf("http://localhost:%d/target", d.port), http.StatusFound)
		case "/ws":
			// 极简的 WebSocket 握手：只校验 Upgrade 头，然后按行回显
			if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				http.Error(w, "upgrade required", http.StatusBadRequest)
				return
			}
			conn, rw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			_ = rw.Flush()
			for {
				line, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				_, _ = rw.WriteString("echo " + line)
				_ = rw.Flush()
			}
		default:
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "1", Domain: "localhost", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "__Host-easel_session", Value: "evil", Path: "/", Secure: true})
			w.Header().Set("Strict-Transport-Security", "max-age=1; includeSubDomains")
			w.Header().Set("Content-Security-Policy", "script-src 'self'")
			fmt.Fprintf(w, "dev server says hi at %s", r.URL.Path)
		}
	}))
	d.port = d.srv.Listener.Addr().(*net.TCPAddr).Port
	t.Cleanup(d.srv.Close)
	return d
}

func (d *devServer) seen() (string, http.Header) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.host, d.last
}

func TestExposeAndProxy(t *testing.T) {
	s := newTestServer(t)
	dev := newDevServer(t)

	code, res := s.runJSON("", "expose", fmt.Sprint(dev.port), "--name", "dev")
	if code != 0 || res["reachable"] != true || dig(res, "prototype", "kind") != "proxy" {
		t.Fatalf("expose = %d %v", code, res)
	}
	preview := strings.TrimSuffix(dig(res, "urls", "preview").(string), "/")

	// 管理员访问：Host 伪装成 localhost:<port>，easel 的 cookie 和 Authorization 不给上游
	rec := s.get(preview+"/page", "Authorization", "Bearer secret", "Origin", preview, "Referer", preview+"/from")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "dev server says hi at /page") {
		t.Fatalf("proxied = %d %q", rec.Code, rec.Body.String())
	}
	host, hdr := dev.seen()
	local := fmt.Sprintf("localhost:%d", dev.port)
	if host != local {
		t.Errorf("upstream Host = %q, want %q", host, local)
	}
	if hdr.Get("Origin") != "http://"+local || hdr.Get("Referer") != "http://"+local+"/from" {
		t.Errorf("Origin/Referer not rewritten: %q %q", hdr.Get("Origin"), hdr.Get("Referer"))
	}
	if hdr.Get("Authorization") != "" || strings.Contains(hdr.Get("Cookie"), "easel_view") || hdr.Get("X-Forwarded-Host") != "" {
		t.Errorf("easel credentials or XFH leaked upstream: %v", hdr)
	}
	// 响应：Domain 去掉、冒充 easel 的 cookie 丢弃、HSTS 不透传、CSP 叠加
	cookies := strings.Join(rec.Header().Values("Set-Cookie"), " | ")
	if strings.Contains(cookies, "Domain") || strings.Contains(cookies, "__Host-easel") || !strings.Contains(cookies, "sid=1") {
		t.Errorf("Set-Cookie = %q", cookies)
	}
	if strings.Contains(rec.Header().Get("Strict-Transport-Security"), "includeSubDomains") {
		t.Errorf("upstream HSTS leaked: %q", rec.Header().Get("Strict-Transport-Security"))
	}
	if csp := rec.Header().Values("Content-Security-Policy"); len(csp) != 2 {
		t.Errorf("CSP headers = %q, want upstream + easel", csp)
	}
	// 第三方 Origin 原样透传（上游自己的 CSRF 防护照常生效）
	s.get(preview+"/", "Origin", "https://evil.example")
	if _, hdr := dev.seen(); hdr.Get("Origin") != "https://evil.example" {
		t.Errorf("foreign Origin rewritten to %q", hdr.Get("Origin"))
	}
	// Location 改回公网源
	if rec := s.get(preview + "/redir"); rec.Header().Get("Location") != preview+"/target" {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
	// 代理原型没有版本地址；TLS ask 放行
	if rec := s.get(strings.Replace(preview, ".localhost", "--v1.localhost", 1) + "/"); rec.Code != http.StatusNotFound {
		t.Errorf("--v1 on a proxy = %d", rec.Code)
	}
	u, _ := url.Parse(preview)
	if rec := s.get("http://127.0.0.1:7780/_tls/ask?domain=" + u.Host); rec.Code != 200 {
		t.Errorf("tls ask for proxy = %d", rec.Code)
	}

	// list 里能看到端口
	_, list := s.runJSON("", "list")
	p := list["prototypes"].([]any)[0].(map[string]any)
	if p["serve_kind"] != "proxy" || p["proxy"].(map[string]any)["port"].(float64) != float64(dev.port) || p["proxy"].(map[string]any)["status"] != "active" {
		t.Errorf("list = %v", p)
	}

	// 互斥：推送到代理原型、把静态原型改成代理、同一端口挂两个原型，都拒绝（退出码 5）
	s.pushSite("static", "static")
	if code, _ := s.runJSON("", "expose", fmt.Sprint(dev.port+1), "--name", "static"); code != ExitConflict {
		t.Errorf("expose over static = %d", code)
	}
	if code, _ := s.runJSON("", "expose", fmt.Sprint(dev.port), "--name", "other"); code != ExitConflict {
		t.Errorf("same port twice = %d", code)
	}

	// dev server 停了：页面导航得到说明页
	dev.srv.Close()
	rec = s.get(preview+"/", "Accept", "text/html")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "Dev server not reachable") {
		t.Errorf("down upstream = %d %q", rec.Code, rec.Body.String())
	}
}

func TestPushToProxyRejected(t *testing.T) {
	s := newTestServer(t)
	dev := newDevServer(t)
	if code, _ := s.runJSON("", "expose", fmt.Sprint(dev.port), "--name", "dev"); code != 0 {
		t.Fatal("expose")
	}
	site := t.TempDir()
	writeFiles(t, site, map[string]string{"index.html": "x"})
	code, res, _ := s.push(site, "--name", "dev")
	if code != ExitConflict || dig(res, "error", "code") != "kind_conflict" {
		t.Errorf("push to proxy = %d %v", code, res)
	}
}

// wsDial 经 easel 的 TCP handler 对 rawURL 发起 WebSocket 升级，带上浏览器 b 的 cookie。
func wsDial(t *testing.T, srv *httptest.Server, b *browser, rawURL string) (net.Conn, *bufio.Reader) {
	t.Helper()
	u, _ := url.Parse(rawURL)
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var cookie []string
	for _, c := range b.jar.Cookies(u) {
		cookie = append(cookie, c.Name+"="+c.Value)
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nCookie: %s\r\n\r\n",
		u.RequestURI(), u.Host, strings.Join(cookie, "; "))
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upgrade = %d %q", resp.StatusCode, body)
	}
	return conn, br
}

func TestProxyWebSocketRevocation(t *testing.T) {
	old := proxy.WatchInterval
	proxy.WatchInterval = 100 * time.Millisecond
	t.Cleanup(func() { proxy.WatchInterval = old })

	s := newTestServer(t)
	dev := newDevServer(t)
	_, res := s.runJSON("", "expose", fmt.Sprint(dev.port), "--name", "dev")
	preview := strings.TrimSuffix(dig(res, "urls", "preview").(string), "/")
	srv := httptest.NewServer(s.inst.tcp)
	t.Cleanup(srv.Close)

	// 访客经分享链接拿到访问 cookie，建立 WebSocket
	_, sh := s.runJSON("", "share", "create", "dev")
	v := newBrowser(t, s.inst.tcp)
	if rec := v.get(sh["url"].(string)); rec.Code != http.StatusSeeOther {
		t.Fatalf("open share = %d", rec.Code)
	}
	conn, br := wsDial(t, srv, v, preview+"/ws")
	defer conn.Close()
	fmt.Fprint(conn, "hello\n")
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if line, err := br.ReadString('\n'); err != nil || line != "echo hello\n" {
		t.Fatalf("echo = %q %v", line, err)
	}

	// 撤销分享：连接在看门狗的下一次检查时断开
	if code, _ := s.runJSON("", "share", "revoke", itoa(int64(sh["id"].(float64)))); code != 0 {
		t.Fatal("revoke")
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadString('\n'); err == nil || isTimeout(err) {
		t.Errorf("WebSocket still open after revoke: %v", err)
	}

	// 管理员：删除原型后断开
	a := s.admin()
	s.get(preview + "/") // 换好票
	conn2, br2 := wsDial(t, srv, a, preview+"/ws")
	defer conn2.Close()
	fmt.Fprint(conn2, "x\n")
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if line, _ := br2.ReadString('\n'); line != "echo x\n" {
		t.Fatalf("admin echo = %q", line)
	}
	if code, _ := s.runJSON("", "rm", "dev", "--yes"); code != 0 {
		t.Fatal("rm")
	}
	_ = conn2.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br2.ReadString('\n'); err == nil || isTimeout(err) {
		t.Errorf("WebSocket still open after rm: %v", err)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func TestProxyPauseAndResume(t *testing.T) {
	s := newTestServer(t)
	dev := newDevServer(t)
	_, res := s.runJSON("", "expose", fmt.Sprint(dev.port), "--name", "dev")
	preview := strings.TrimSuffix(dig(res, "urls", "preview").(string), "/")
	_, sh := s.runJSON("", "share", "create", "dev")
	v := newBrowser(t, s.inst.tcp)
	v.get(sh["url"].(string))
	if rec := v.get(preview + "/"); rec.Code != 200 {
		t.Fatalf("visitor = %d", rec.Code)
	}

	reg := s.inst.upstreams
	// 进程身份没变：不暂停
	if n, err := reg.Check(t.Context()); err != nil || n != 0 {
		t.Fatalf("check = %d %v", n, err)
	}
	// 换了进程（别的项目占了这个端口）
	reg.Listener = func(int) (proxy.Process, bool) { return proxy.Process{Exe: "/usr/bin/other", Cwd: "/elsewhere"}, true }
	if _, ok := proxy.ListenerProcess(dev.port); !ok {
		// 拿不到进程身份的平台上，注册时没记录身份，不会检查
		t.Skip("process identity is not available on this platform")
	}
	if n, err := reg.Check(t.Context()); err != nil || n != 1 {
		t.Fatalf("check after process change = %d %v", n, err)
	}
	if rec := v.get(preview + "/"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "paused") {
		t.Errorf("visitor while paused = %d %q", rec.Code, rec.Body.String())
	}
	if rec := newBrowser(t, s.inst.tcp).get(sh["url"].(string)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("share entry while paused = %d", rec.Code)
	}
	if rec := s.get(preview + "/"); rec.Code != 200 {
		t.Errorf("admin while paused = %d", rec.Code)
	}
	_, list := s.runJSON("", "list")
	if px := list["prototypes"].([]any)[0].(map[string]any)["proxy"].(map[string]any); px["status"] != "paused" || px["paused_reason"] != "process_changed" {
		t.Errorf("list while paused = %v", px)
	}

	// 重新 expose：记录新身份、解除暂停
	reg.Listener = nil
	if code, res := s.runJSON("", "expose", fmt.Sprint(dev.port), "--name", "dev"); code != 0 || res["resumed"] != true {
		t.Fatalf("re-expose = %d %v", code, res)
	}
	if rec := v.get(preview + "/"); rec.Code != 200 {
		t.Errorf("visitor after resume = %d", rec.Code)
	}

	// 长期不可达
	dev.srv.Close()
	reg.Now = func() time.Time { return time.Now().Add(proxy.UnreachableAfter + time.Hour) }
	if n, err := reg.Check(t.Context()); err != nil || n != 1 {
		t.Fatalf("check unreachable = %d %v", n, err)
	}
	if rec := v.get(preview + "/"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("visitor after unreachable = %d", rec.Code)
	}
}

func TestExposeNeedsProxyScope(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(s.inst.tcp)
	t.Cleanup(srv.Close)
	_, tok := s.runJSON("", "token", "create", "--name", "rw")
	t.Setenv("EASEL_TOKEN", tok["token"].(string))
	if code, _, errs := s.run("", "expose", "5173", "--name", "dev", "--server", srv.URL); code != ExitAuth || !strings.Contains(errs, "proxy scope") {
		t.Errorf("expose without proxy scope = %d %q", code, errs)
	}
	_, tok = s.runJSON("", "token", "create", "--name", "px", "--scope", "proxy")
	t.Setenv("EASEL_TOKEN", tok["token"].(string))
	if code, out, errs := s.run("", "expose", "5173", "--name", "dev", "--server", srv.URL); code != 0 || !strings.Contains(out, "dev-") {
		t.Errorf("expose with proxy scope = %d %q %q", code, out, errs)
	}
}

func TestProxyDualStack(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback")
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "v6 only") })}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	s := newTestServer(t)
	port := ln.Addr().(*net.TCPAddr).Port
	_, res := s.runJSON("", "expose", fmt.Sprint(port), "--name", "v6")
	if res["reachable"] != true {
		t.Errorf("v6-only server not reachable: %v", res)
	}
	preview := strings.TrimSuffix(dig(res, "urls", "preview").(string), "/")
	if rec := s.get(preview + "/"); rec.Code != 200 || rec.Body.String() != "v6 only" {
		t.Errorf("v6-only upstream = %d %q", rec.Code, rec.Body.String())
	}
}
