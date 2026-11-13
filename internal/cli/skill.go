package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/smokecat/easel/skills"
)

const skillName = "easel"

func runSkill(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		return printSkill(env)
	}
	return runGroup(ctx, env, "skill", args, []command{
		{"print", "把 SKILL.md 打印到 stdout（不带子命令时也是这个）", runSkillPrint},
		{"install", "安装到用户级 skill 目录，Claude Code、Codex、Gemini CLI 都能用", runSkillInstall},
	})
}

func runSkillPrint(_ context.Context, env *Env, args []string) error {
	if err := parseFlags(newFlagSet(env, "skill print"), args); err != nil {
		return err
	}
	return printSkill(env)
}

func printSkill(env *Env) error {
	_, err := env.Stdout.Write(skills.Easel)
	return err
}

// skillResult 是 `easel skill install --json` 的输出，字段保持稳定。
type skillResult struct {
	OK     bool        `json:"ok"`
	Path   string      `json:"path"`   // 安装的 SKILL.md
	Status string      `json:"status"` // installed / updated / unchanged
	Links  []skillLink `json:"links"`
}

type skillLink struct {
	Path   string `json:"path"`
	Target string `json:"target"`
	Status string `json:"status"` // created / replaced / unchanged
}

func runSkillInstall(_ context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "skill install")
	fset.Usage = func() {
		fmt.Fprint(fset.Output(), "用法：easel skill install [--dir <目录>] [--force] [--no-claude]\n\n"+
			"把 SKILL.md 写到 <目录>/easel/（默认 ~/.agents/skills，Codex 和 Gemini CLI 读这里）；\n"+
			"用默认目录且存在 ~/.claude 时，再把 ~/.claude/skills/easel 软链过去，供 Claude Code 使用。\n"+
			"升级 easel 后再运行一次即可更新。\n\n")
		fset.PrintDefaults()
	}
	dir := fset.String("dir", "", "skill 根目录（缺省 ~/.agents/skills）")
	force := fset.Bool("force", false, "目标已被别的文件或软链占用时替换它")
	noClaude := fset.Bool("no-claude", false, "不创建 ~/.claude/skills/easel 软链")
	jsonOut := fset.Bool("json", false, "stdout 输出 JSON")
	if err := parseFlags(fset, args); err != nil {
		return err
	}
	res, err := installSkill(*dir, *force, *noClaude)
	if err := jsonResult(env, *jsonOut, err); err != nil {
		return err
	}
	if *jsonOut {
		writeJSONTo(env.Stdout, res)
		return nil
	}
	fmt.Fprintf(env.Stdout, "%s %s\n", res.Status, res.Path)
	for _, l := range res.Links {
		fmt.Fprintf(env.Stdout, "%s %s -> %s\n", map[string]string{"created": "linked", "replaced": "relinked", "unchanged": "unchanged"}[l.Status], l.Path, l.Target)
	}
	return nil
}

func installSkill(root string, force, noClaude bool) (*skillResult, error) {
	custom := root != ""
	var home string
	if !custom {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot find home directory: %w", err)
		}
		home = h
		root = filepath.Join(home, ".agents", "skills")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(root, skillName)
	file := filepath.Join(target, "SKILL.md")

	// 先检查所有目标，确认都能写再动手，免得装到一半。
	replaceTarget := false
	switch fi, err := os.Lstat(target); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	case fi.Mode()&fs.ModeSymlink != 0:
		// 常见于开发者把它软链到源码仓库：内容相同就当作已安装，不同则不覆盖别人的链接。
		if !force {
			if cur, err := os.ReadFile(file); err == nil && bytes.Equal(cur, skills.Easel) {
				break
			}
			dest, _ := os.Readlink(target)
			return nil, &conflictError{msg: target + " is a symlink to " + dest + " with different content",
				hint: "leave it if it points at your checkout, or rerun with --force to replace it with this version"}
		}
		replaceTarget = true
	case !fi.IsDir():
		if !force {
			return nil, &conflictError{msg: target + " exists and is not a directory", hint: "rerun with --force to replace it"}
		}
		replaceTarget = true
	}

	var link, linkTarget string
	replaceLink := false
	if !custom && !noClaude {
		if fi, err := os.Stat(filepath.Join(home, ".claude")); err == nil && fi.IsDir() {
			link = filepath.Join(home, ".claude", "skills", skillName)
			linkTarget = target
			switch fi, err := os.Lstat(link); {
			case errors.Is(err, fs.ErrNotExist):
			case err != nil:
				return nil, err
			case fi.Mode()&fs.ModeSymlink != 0:
				if !samePath(link, target) {
					if !force {
						dest, _ := os.Readlink(link)
						return nil, &conflictError{msg: link + " already points to " + dest,
							hint: "rerun with --force to relink it, or --no-claude to leave it alone"}
					}
					replaceLink = true
				}
			case fi.IsDir():
				// 目录可能是用户自己维护的，--force 也不删。
				return nil, &conflictError{msg: link + " is a directory, not a link to " + target,
					hint: "remove or rename it yourself, or rerun with --no-claude"}
			default:
				if !force {
					return nil, &conflictError{msg: link + " exists", hint: "rerun with --force to replace it, or --no-claude"}
				}
				replaceLink = true
			}
		}
	}

	res := &skillResult{OK: true, Path: file, Links: []skillLink{}}
	if replaceTarget {
		if err := os.Remove(target); err != nil {
			return nil, err
		}
	}
	switch cur, err := os.ReadFile(file); {
	case err == nil && bytes.Equal(cur, skills.Easel):
		res.Status = "unchanged"
	case err == nil:
		res.Status = "updated"
	default:
		res.Status = "installed"
	}
	if res.Status != "unchanged" {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return nil, err
		}
		if err := writeFileAtomic(file, skills.Easel); err != nil {
			return nil, err
		}
	}

	if link != "" {
		l := skillLink{Path: link, Target: linkTarget, Status: "unchanged"}
		if _, err := os.Lstat(link); errors.Is(err, fs.ErrNotExist) || replaceLink {
			l.Status = "created"
			if replaceLink {
				l.Status = "replaced"
				if err := os.Remove(link); err != nil {
					return nil, err
				}
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				return nil, err
			}
			if err := os.Symlink(linkTarget, link); err != nil {
				return nil, err
			}
		}
		res.Links = append(res.Links, l)
	}
	return res, nil
}

// samePath 判断两个路径解析软链后是否指向同一处。
func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		// b 可能还没创建：比较 a 的链接内容。
		dest, err := os.Readlink(a)
		if err != nil {
			return false
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(a), dest)
		}
		return filepath.Clean(dest) == filepath.Clean(b)
	}
	return ra == rb
}

func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".SKILL-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
