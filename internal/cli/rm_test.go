package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/smokecat/easel/internal/client"
	"github.com/smokecat/easel/internal/publish"
)

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestListAndRemove(t *testing.T) {
	s := newTestServer(t)
	previewA := s.pushSite("alpha", "alpha only")
	s.pushSite("beta", "beta only")

	code, list := s.runJSON("", "list")
	ps := list["prototypes"].([]any)
	if code != 0 || len(ps) != 2 {
		t.Fatalf("list = %d %v", code, list)
	}
	first := ps[0].(map[string]any)
	if first["name"] != "beta" || first["current"].(map[string]any)["number"].(float64) != 1 || first["serve_kind"] != "static" {
		t.Errorf("list order / fields = %v", first)
	}
	if code, out, _ := s.run("", "list", "--socket", s.socket); code != 0 || !strings.Contains(out, "alpha") || !strings.Contains(out, previewA) {
		t.Errorf("list table = %d %q", code, out)
	}

	_, sh := s.runJSON("", "share", "create", "alpha")
	link := sh["url"].(string)
	if rec := s.get(previewA + "/"); rec.Code != 200 {
		t.Fatalf("preview before rm = %d", rec.Code)
	}

	// 非交互环境不加 --yes 拒绝
	if code, _, errs := s.run("", "rm", "alpha", "--socket", s.socket); code != ExitUsage || !strings.Contains(errs, "--yes") {
		t.Errorf("rm without --yes = %d %q", code, errs)
	}
	if code, res := s.runJSON("", "rm", "alpha", "--yes"); code != 0 || res["deleted"] != "alpha" {
		t.Fatalf("rm = %d %v", code, res)
	}
	if code, _ := s.runJSON("", "rm", "alpha", "--yes"); code != ExitNotFound {
		t.Errorf("rm twice = %d, want 4", code)
	}
	_, list = s.runJSON("", "list")
	if ps := list["prototypes"].([]any); len(ps) != 1 {
		t.Errorf("list after rm = %v", list)
	}

	// 预览地址、分享链接全部失效（管理员已有的访问会话还在，但内容没了）
	if rec := s.admin().get(previewA + "/"); rec.Code != http.StatusNotFound {
		t.Errorf("preview after rm = %d, want 404", rec.Code)
	}
	if rec := newBrowser(t, s.inst.tcp).get(link); rec.Code != http.StatusNotFound {
		t.Errorf("share after rm = %d, want 404", rec.Code)
	}
	_, shares := s.runJSON("", "share", "list", "--all")
	if n := len(shares["shares"].([]any)); n != 0 {
		t.Errorf("shares after rm = %d", n)
	}
}

func TestBlobSweep(t *testing.T) {
	s := newTestServer(t)
	s.pushSite("alpha", "shared")        // index.html = "shared"
	s.pushSite("beta", "shared")         // 同样内容，blob 共用
	s.pushSite("gamma", "gamma content") // 删掉后应被回收
	blobs := s.inst.pub.Blobs
	later := func() time.Time { return time.Now().Add(2 * time.Hour) }

	if code, _ := s.runJSON("", "rm", "alpha", "--yes"); code != 0 {
		t.Fatal("rm alpha")
	}
	if code, _ := s.runJSON("", "rm", "gamma", "--yes"); code != 0 {
		t.Fatal("rm gamma")
	}

	// 宽限期内不删
	if n, err := s.inst.pub.SweepBlobs(t.Context()); err != nil || n != 0 {
		t.Errorf("sweep within grace = %d %v", n, err)
	}
	s.inst.pub.Now = later
	if n, err := s.inst.pub.SweepBlobs(t.Context()); err != nil || n != 1 {
		t.Errorf("sweep = %d %v, want 1", n, err)
	}
	if !blobs.Has(sha("shared")) {
		t.Error("blob still used by beta was removed")
	}
	if blobs.Has(sha("gamma content")) {
		t.Error("unreferenced blob not removed")
	}
	s.inst.pub.Now = nil

	// 待提交的上传引用的旧 blob 不删：删掉 beta 后，新的推送第 1 步声明了它（已存在，不在 missing 里）
	if code, _ := s.runJSON("", "rm", "beta", "--yes"); code != 0 {
		t.Fatal("rm beta")
	}
	c := client.NewSocket(s.socket)
	resp, err := c.CreateVersion(context.Background(), "delta", publish.CreateRequest{
		Files: []publish.FileEntry{{Path: "index.html", SHA256: sha("shared"), Size: int64(len("shared"))}},
	})
	if err != nil || len(resp.Missing) != 0 {
		t.Fatalf("create = %v %v", resp, err)
	}
	s.inst.pub.Now = later
	if n, err := s.inst.pub.SweepBlobs(t.Context()); err != nil || n != 0 || !blobs.Has(sha("shared")) {
		t.Fatalf("sweep with pending upload = %d %v", n, err)
	}
	s.inst.pub.Now = nil
	if _, err := c.Commit(context.Background(), resp.UploadID); err != nil {
		t.Fatalf("commit after sweep: %v", err)
	}
}

func TestRemoveThenRecreateServesNewContent(t *testing.T) {
	s := newTestServer(t)
	old := s.pushSite("again", "old content")
	if rec := s.get(old + "/"); !strings.Contains(rec.Body.String(), "old content") {
		t.Fatalf("old = %q", rec.Body.String()) // 让内容 handler 缓存这个 manifest
	}
	if code, _ := s.runJSON("", "rm", "again", "--yes"); code != 0 {
		t.Fatal("rm")
	}
	// 同名重建：新标签、SQLite 可能复用 revision ID，不能拿到旧的缓存
	fresh := s.pushSite("again", "new content")
	if fresh == old {
		t.Error("recreated prototype reused the old host label")
	}
	if rec := s.get(fresh + "/"); !strings.Contains(rec.Body.String(), "new content") {
		t.Errorf("recreated = %d %q", rec.Code, rec.Body.String())
	}
}
