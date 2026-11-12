package cli

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// apiCall 以已登录的管理员浏览器调后台 API（非 GET 带同源 Origin，和面板一样）。
func (s *testServer) apiCall(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	s.t.Helper()
	app := s.inst.hosts.AppOrigin()
	hdr := []string{"Origin", app}
	if body != "" {
		hdr = append(hdr, "Content-Type", "application/json")
	}
	rec := s.admin().do(method, app+path, strings.NewReader(body), hdr...)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

// 面板的拖拽上传走整包端点：没有被排除的文件时 stats.skipped 必须是 []，不能是 null（前端读 .length）。
func TestArchivePushSkippedIsArray(t *testing.T) {
	s := newTestServer(t)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hi"))
	_ = tw.Close()
	rec, _ := s.apiCall("POST", "/api/v1/push?name=panel", buf.String())
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"skipped":[]`) {
		t.Errorf("archive push = %d %s", rec.Code, rec.Body.String())
	}
}

func TestVersionsAndRollback(t *testing.T) {
	s := newTestServer(t)
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("one")
	if code, _, _ := s.push(dir, "--name", "site", "-m", "first"); code != 0 {
		t.Fatal("push 1")
	}
	write("two")
	_, res, _ := s.push(dir, "--name", "site")
	preview := strings.TrimSuffix(dig(res, "urls", "preview").(string), "/")

	rec, vs := s.apiCall("GET", "/api/v1/prototypes/site/versions", "")
	list := vs["versions"].([]any)
	if rec.Code != 200 || len(list) != 2 {
		t.Fatalf("versions = %d %v", rec.Code, vs)
	}
	v2, v1 := list[0].(map[string]any), list[1].(map[string]any)
	if v2["number"].(float64) != 2 || v2["current"] != true || v1["note"] != "first" || v1["origin"] != "upload" || v1["current"] != nil {
		t.Errorf("versions = %v", list)
	}
	if !strings.Contains(v1["url"].(string), "--v1.") {
		t.Errorf("pinned url = %v", v1["url"])
	}

	// 回滚到 v1：新建 v3，内容是 v1 的
	rec, rb := s.apiCall("POST", "/api/v1/prototypes/site/rollback", `{"version":1}`)
	if rec.Code != 200 || dig(rb, "version", "number").(float64) != 3 || dig(rb, "version", "unchanged") != false {
		t.Fatalf("rollback = %d %v", rec.Code, rb)
	}
	if rec := s.get(preview + "/"); !strings.Contains(rec.Body.String(), "one") {
		t.Errorf("after rollback = %q", rec.Body.String())
	}
	_, vs = s.apiCall("GET", "/api/v1/prototypes/site/versions", "")
	v3 := vs["versions"].([]any)[0].(map[string]any)
	if v3["origin"] != "rollback" || v3["note"] != "rollback to v1" || v3["current"] != true {
		t.Errorf("v3 = %v", v3)
	}
	// 回滚到当前版本：不产生新版本
	if _, rb := s.apiCall("POST", "/api/v1/prototypes/site/rollback", `{"version":3}`); dig(rb, "version", "unchanged") != true {
		t.Errorf("rollback to current = %v", rb)
	}
	if rec, _ := s.apiCall("POST", "/api/v1/prototypes/site/rollback", `{"version":9}`); rec.Code != http.StatusNotFound {
		t.Errorf("rollback to missing = %d", rec.Code)
	}
	if rec, _ := s.apiCall("POST", "/api/v1/prototypes/nope/rollback", `{"version":1}`); rec.Code != http.StatusNotFound {
		t.Errorf("rollback on missing prototype = %d", rec.Code)
	}
	// 跨站请求拒绝
	app := s.inst.hosts.AppOrigin()
	if rec := s.admin().do("POST", app+"/api/v1/prototypes/site/rollback", strings.NewReader(`{"version":1}`),
		"Content-Type", "application/json", "Origin", "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site rollback = %d", rec.Code)
	}
	// 回滚出的版本引用的 blob 不会被回收
	s.inst.pub.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if n, err := s.inst.pub.SweepBlobs(t.Context()); err != nil || n != 0 {
		t.Errorf("sweep after rollback = %d %v", n, err)
	}
}
