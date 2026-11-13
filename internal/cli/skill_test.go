package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smokecat/easel/skills"
)

func TestSkillPrint(t *testing.T) {
	for _, args := range [][]string{{"skill"}, {"skill", "print"}} {
		code, out, _ := run(args...)
		if code != ExitOK || out != string(skills.Easel) {
			t.Fatalf("%v: exit = %d, stdout matches = %v", args, code, out == string(skills.Easel))
		}
	}
	if !strings.HasPrefix(string(skills.Easel), "---\nname: easel\n") {
		t.Error("embedded SKILL.md should start with frontmatter name: easel")
	}
}

func installJSON(t *testing.T, args ...string) (int, skillResult, string) {
	t.Helper()
	code, out, errOut := run(append([]string{"skill", "install", "--json"}, args...)...)
	var res skillResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid json %q: %v", out, err)
	}
	return code, res, errOut
}

func TestSkillInstallDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".agents", "skills", "easel")
	link := filepath.Join(home, ".claude", "skills", "easel")

	code, res, _ := installJSON(t)
	if code != ExitOK || res.Status != "installed" || res.Path != filepath.Join(target, "SKILL.md") {
		t.Fatalf("exit = %d, res = %+v", code, res)
	}
	if len(res.Links) != 1 || res.Links[0].Path != link || res.Links[0].Status != "created" {
		t.Fatalf("links = %+v", res.Links)
	}
	if b, err := os.ReadFile(filepath.Join(link, "SKILL.md")); err != nil || string(b) != string(skills.Easel) {
		t.Fatalf("claude link does not serve SKILL.md: %v", err)
	}

	// 再装一次：什么都不变。
	code, res, _ = installJSON(t)
	if code != ExitOK || res.Status != "unchanged" || res.Links[0].Status != "unchanged" {
		t.Fatalf("second install: exit = %d, res = %+v", code, res)
	}

	// 旧版本被更新，目录里的其他文件保留。
	if err := os.WriteFile(res.Path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(target, "notes.txt")
	if err := os.WriteFile(extra, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, res, _ = installJSON(t)
	if code != ExitOK || res.Status != "updated" {
		t.Fatalf("update: exit = %d, res = %+v", code, res)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Errorf("extra file removed: %v", err)
	}
}

func TestSkillInstallNoClaudeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	code, res, _ := installJSON(t)
	if code != ExitOK || len(res.Links) != 0 {
		t.Fatalf("exit = %d, res = %+v", code, res)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude")); err == nil {
		t.Error("should not create ~/.claude")
	}
}

func TestSkillInstallCustomDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Mkdir(filepath.Join(home, ".claude"), 0o755)
	dir := filepath.Join(t.TempDir(), "skills")
	code, res, _ := installJSON(t, "--dir", dir)
	if code != ExitOK || res.Path != filepath.Join(dir, "easel", "SKILL.md") || len(res.Links) != 0 {
		t.Fatalf("exit = %d, res = %+v", code, res)
	}
}

func TestSkillInstallSymlinkedCheckout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Mkdir(filepath.Join(home, ".claude"), 0o755)
	checkout := filepath.Join(t.TempDir(), "easel")
	os.Mkdir(checkout, 0o755)
	os.WriteFile(filepath.Join(checkout, "SKILL.md"), skills.Easel, 0o644)
	target := filepath.Join(home, ".agents", "skills", "easel")
	os.MkdirAll(filepath.Dir(target), 0o755)
	if err := os.Symlink(checkout, target); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude", "skills", "easel")
	os.MkdirAll(filepath.Dir(link), 0o755)
	os.Symlink(target, link)

	// 内容相同：两处都不动。
	code, res, _ := installJSON(t)
	if code != ExitOK || res.Status != "unchanged" || res.Links[0].Status != "unchanged" {
		t.Fatalf("exit = %d, res = %+v", code, res)
	}

	// 开发中的 SKILL.md 和二进制里的不同：不覆盖，除非 --force。
	os.WriteFile(filepath.Join(checkout, "SKILL.md"), []byte("work in progress"), 0o644)
	code, _, errOut := installJSON(t)
	if code != ExitConflict || !strings.Contains(errOut, "--force") {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, "SKILL.md")); string(b) != "work in progress" {
		t.Fatal("checkout modified without --force")
	}
	code, res, _ = installJSON(t, "--force")
	if code != ExitOK || res.Status != "installed" {
		t.Fatalf("--force: exit = %d, res = %+v", code, res)
	}
	if fi, _ := os.Lstat(target); fi.Mode()&os.ModeSymlink != 0 {
		t.Error("target should now be a real directory")
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, "SKILL.md")); string(b) != "work in progress" {
		t.Error("--force must replace the link, not write through it")
	}
}

func TestSkillInstallClaudeConflict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	other := t.TempDir()
	link := filepath.Join(home, ".claude", "skills", "easel")
	os.MkdirAll(filepath.Dir(link), 0o755)
	os.Symlink(other, link)

	code, _, _ := installJSON(t)
	if code != ExitConflict {
		t.Fatalf("exit = %d, want conflict", code)
	}
	// 冲突时什么都不写。
	if _, err := os.Stat(filepath.Join(home, ".agents")); err == nil {
		t.Error("should not install anything on conflict")
	}
	if code, _, _ := installJSON(t, "--no-claude"); code != ExitOK {
		t.Errorf("--no-claude exit = %d", code)
	}
	code, res, _ := installJSON(t, "--force")
	if code != ExitOK || res.Links[0].Status != "replaced" {
		t.Fatalf("--force: exit = %d, res = %+v", code, res)
	}

	// 用户自己的目录 --force 也不删。
	os.Remove(link)
	os.Mkdir(link, 0o755)
	if code, _, _ := installJSON(t, "--force"); code != ExitConflict {
		t.Errorf("directory: exit = %d, want conflict", code)
	}
}
