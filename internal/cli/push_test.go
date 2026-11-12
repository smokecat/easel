package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/smokecat/easel/internal/publish"
)

// testServer 起一个完整的服务端：unix socket 上是后台 API，TCP handler 直接调用（按 Host 分流）。
type testServer struct {
	t      *testing.T
	inst   *instance
	socket string
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	return newTestServerWith(t, "http://127.0.0.1:7780", "http://localhost:7780")
}

func newTestServerWith(t *testing.T, appURL, contentURL string) *testServer {
	t.Helper()
	t.Setenv("EASEL_NAME", "")
	t.Setenv("EASEL_SOCKET", "")
	dir := t.TempDir()
	inst, err := openInstance(context.Background(), serveConfig{
		DataDir: filepath.Join(dir, "data"), AppURL: appURL, ContentURL: contentURL,
		Assets: fstest.MapFS{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s.sock")
	ln, err := listenSocket(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: inst.socket}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = inst.Close() })
	return &testServer{t: t, inst: inst, socket: sock}
}

// push 运行 easel push --json，返回退出码和解析后的结果。
func (s *testServer) push(args ...string) (int, map[string]any, string) {
	s.t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), &Env{Stdout: &out, Stderr: &errb}, append([]string{"push", "--json", "--socket", s.socket}, args...))
	var res map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		s.t.Fatalf("push %v: stdout is not JSON: %q (stderr %q)", args, out.String(), errb.String())
	}
	return code, res, errb.String()
}

// get 以内容主机的 Host 发请求。
func (s *testServer) get(rawURL string, hdr ...string) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequest("GET", rawURL, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	s.inst.tcp.ServeHTTP(rec, req)
	return rec
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, data := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func dig(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		v = v.(map[string]any)[k]
	}
	return v
}

func num(m map[string]any, keys ...string) int {
	return int(dig(m, keys...).(float64))
}

func TestPushRepeatedUploadsOnlyMissing(t *testing.T) {
	s := newTestServer(t)
	site := t.TempDir()
	writeFiles(t, site, map[string]string{
		"index.html":              "<h1>v1</h1>",
		"assets/app.js":           "console.log(1)",
		"assets/a.css":            "h1{}",
		".env":                    "SECRET=1",
		"node_modules/x/index.js": "x",
	})

	// 第一次：三个文件全传，.env 和 node_modules 被排除
	code, res, stderr := s.push(site, "--name", "demo")
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if num(res, "version", "number") != 1 || dig(res, "prototype", "created") != true {
		t.Fatalf("first push: %v", res)
	}
	if num(res, "stats", "files") != 3 || num(res, "stats", "uploaded_files") != 3 {
		t.Fatalf("first push stats: %v", res["stats"])
	}
	skipped := dig(res, "stats", "skipped").([]any)
	if len(skipped) != 2 {
		t.Errorf("skipped = %v, want .env and node_modules/", skipped)
	}
	preview := dig(res, "urls", "preview").(string)
	if !strings.HasPrefix(preview, "http://demo-") || !strings.HasSuffix(preview, ".localhost:7780/") {
		t.Fatalf("preview URL = %q", preview)
	}

	// 第二次：内容没变，不产生新版本，不上传
	_, res, _ = s.push(site, "--name", "demo")
	if dig(res, "version", "unchanged") != true || num(res, "version", "number") != 1 || num(res, "stats", "uploaded_files") != 0 {
		t.Fatalf("unchanged push: %v", res)
	}

	// 第三次：改一个文件，再加一个与已有文件内容相同的文件，只传改过的那一个
	writeFiles(t, site, map[string]string{"index.html": "<h1>v2</h1>", "copy.css": "h1{}"})
	_, res, _ = s.push(site, "--name", "demo", "-m", "second")
	if num(res, "version", "number") != 2 || num(res, "stats", "files") != 4 || num(res, "stats", "uploaded_files") != 1 {
		t.Fatalf("third push: version %v stats %v", res["version"], res["stats"])
	}
	if dig(res, "version", "note") != "second" {
		t.Errorf("note = %v", dig(res, "version", "note"))
	}

	// 预览跟随最新版本，版本固定的地址仍是旧内容
	if rec := s.get(preview); rec.Code != 200 || rec.Body.String() != "<h1>v2</h1>" {
		t.Fatalf("preview = %d %q", rec.Code, rec.Body.String())
	}
	v1 := strings.Replace(preview, ".localhost", "--v1.localhost", 1)
	rec := s.get(v1)
	if rec.Body.String() != "<h1>v1</h1>" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("pinned v1 = %q, Cache-Control %q", rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
}

func TestContentServing(t *testing.T) {
	s := newTestServer(t)
	site := t.TempDir()
	writeFiles(t, site, map[string]string{
		"index.html":      "home",
		"about.html":      "about",
		"docs/index.html": "docs",
		"docs/404.html":   "docs 404",
		"video.mp4":       "0123456789",
		"data.bin":        "raw",
		"assets/app.js":   "js",
	})
	_, res, stderr := s.push(site, "--name", "site")
	preview, ok := dig(res, "urls", "preview").(string)
	if !ok {
		t.Fatalf("push failed: %v %s", res, stderr)
	}
	u := strings.TrimSuffix(preview, "/")

	tests := []struct {
		path, wantBody string
		wantCode       int
		hdr            []string
	}{
		{"/", "home", 200, nil},
		{"/about", "about", 200, nil},
		{"/about.html", "about", 200, nil},
		{"/docs", "", 308, nil},
		{"/docs/", "docs", 200, nil},
		{"/docs/missing", "docs 404", 404, nil},
		{"/app/route", "home", 200, nil}, // 没有顶层 404.html：SPA 回退
		{"/missing.js", "404", 404, nil}, // 有扩展名的不回退
		{"/video.mp4", "2345", 206, []string{"Range", "bytes=2-5"}},
		{"/assets/app.js", "", 403, []string{"Service-Worker", "script"}},
		{"/_easel/anything", "404", 404, nil},
	}
	for _, tt := range tests {
		rec := s.get(u+tt.path, tt.hdr...)
		if rec.Code != tt.wantCode || !strings.Contains(rec.Body.String(), tt.wantBody) {
			t.Errorf("GET %s = %d %q, want %d %q", tt.path, rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("GET %s: missing security headers %v", tt.path, rec.Header())
		}
	}
	if rec := s.get(u + "/docs"); rec.Header().Get("Location") != "/docs/" {
		t.Errorf("redirect Location = %q", rec.Header().Get("Location"))
	}
	if rec := s.get(u + "/data.bin"); !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("unknown type should download: %v", rec.Header())
	}
	rec := s.get(u + "/assets/app.js")
	etag := rec.Header().Get("ETag")
	if rec := s.get(u+"/assets/app.js", "If-None-Match", etag); rec.Code != 304 {
		t.Errorf("conditional GET = %d, want 304", rec.Code)
	}

	// 内容域里的未知主机、多级子域都不会落到后台
	for _, host := range []string{"http://nope-aaaaaa.localhost:7780/", "http://a.b.localhost:7780/", "http://localhost:7780/healthz"} {
		if rec := s.get(host); rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404", host, rec.Code)
		}
	}
	// TCP 上的写 API 在鉴权落地前一律拒绝
	req := httptest.NewRequest("POST", "http://127.0.0.1:7780/api/v1/push?name=x", strings.NewReader(""))
	rec = httptest.NewRecorder()
	s.inst.tcp.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("TCP API = %d, want 401", rec.Code)
	}
}

func TestPushArchiveEndpoint(t *testing.T) {
	s := newTestServer(t)
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", s.socket)
	}}}
	post := func(name string, body []byte) (int, map[string]any) {
		resp, err := hc.Post("http://easel/api/v1/push?name="+name, "application/octet-stream", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}

	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	for name, data := range map[string]string{"./shot.png": "png", "./.env": "x", "./notes.md": "# hi"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(data))
	}
	_ = tw.Close()
	code, res := post("shots", tb.Bytes())
	if code != 200 || dig(res, "prototype", "kind") != "assets" || num(res, "stats", "files") != 2 {
		t.Fatalf("tar push = %d %v", code, res)
	}
	listing := s.get(dig(res, "urls", "preview").(string))
	if !strings.Contains(listing.Body.String(), `<img src="/shot.png"`) {
		t.Errorf("asset listing = %q", listing.Body.String())
	}

	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("index.html")
	_, _ = w.Write([]byte("zipped"))
	_ = zw.Close()
	if code, res := post("zipped", zb.Bytes()); code != 200 || dig(res, "prototype", "kind") != "site" {
		t.Fatalf("zip push = %d %v", code, res)
	}

	var evil bytes.Buffer
	tw = tar.NewWriter(&evil)
	_ = tw.WriteHeader(&tar.Header{Name: "../escape.html", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	if code, _ := post("evil", evil.Bytes()); code != 400 {
		t.Errorf("path traversal accepted: %d", code)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestPushInferredName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	s := newTestServer(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	writeFiles(t, repo, map[string]string{"dist/index.html": "app", "mocks/login.html": "login"})
	git(t, repo, "init", "-q")
	git(t, repo, "remote", "add", "origin", "https://user:token@github.com/acme/My-App.git")

	_, res, stderr := s.push(filepath.Join(repo, "dist"))
	if dig(res, "prototype", "name") != "my-app" {
		t.Fatalf("dist push: %v %s", res, stderr)
	}
	if !strings.Contains(stderr, "git 仓库名") {
		t.Errorf("stderr should say where the name came from: %s", stderr)
	}
	_, res, _ = s.push(filepath.Join(repo, "mocks", "login.html"))
	if dig(res, "prototype", "name") != "my-app-mocks-login" {
		t.Fatalf("file push: %v", res)
	}

	// 指纹里不能带远端 URL 的凭据
	proto, err := s.inst.store.Q.GetPrototypeByName(context.Background(), "my-app")
	if err != nil {
		t.Fatal(err)
	}
	if proto.SourceFingerprint != "git:github.com/acme/My-App" {
		t.Errorf("fingerprint = %q", proto.SourceFingerprint)
	}

	// 另一个来源推断出同样的名字：拒绝，退出码 5；显式 --name 可以
	other := filepath.Join(t.TempDir(), "My-App")
	writeFiles(t, other, map[string]string{"index.html": "other"})
	code, res, _ := s.push(other)
	if code != ExitConflict || dig(res, "error", "code") != publish.CodeNameConflict {
		t.Fatalf("conflict: exit %d %v", code, res)
	}
	if code, _, _ := s.push(other, "--name", "my-app"); code != ExitOK {
		t.Fatalf("explicit --name: exit %d", code)
	}

	// easel.toml 提供名字和默认目录
	writeFiles(t, repo, map[string]string{"easel.toml": "name = \"from-toml\"\ndir = \"dist\"\n"})
	t.Chdir(repo)
	_, res, _ = s.push()
	if dig(res, "prototype", "name") != "from-toml" || num(res, "stats", "files") != 1 {
		t.Fatalf("easel.toml push: %v", res)
	}
}

func TestPushErrors(t *testing.T) {
	s := newTestServer(t)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{".env": "x"})
	site := t.TempDir()
	writeFiles(t, site, map[string]string{"index.html": "x"})
	tests := []struct {
		args []string
		exit int
	}{
		{[]string{filepath.Join(dir, "missing")}, ExitUsage},
		{[]string{dir, "--name", "Bad Name"}, ExitUsage},
		{[]string{dir, "--name", "empty"}, ExitUsage}, // 全被排除
		{[]string{site, "--name", "a", "--socket", filepath.Join(dir, "none.sock")}, ExitUnreachable},
	}
	for _, tt := range tests {
		code, res, _ := s.push(tt.args...)
		if code != tt.exit || res["ok"] != false {
			t.Errorf("push %v: exit %d (want %d), %v", tt.args, code, tt.exit, res)
		}
	}
}
