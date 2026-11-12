package cli

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/smokecat/easel/internal/publish"
)

// identity 是一次推送的原型身份。
type identity struct {
	Name string
	// Source 是名字的来源，在 stderr 里告诉用户（排查「推到了错的原型」时用）。
	Source      string
	Inferred    bool
	Fingerprint string
}

// buildDirs 是常见的构建输出目录：推送它们时原型名就是仓库名，不再加路径后缀。
var buildDirs = map[string]bool{"dist": true, "build": true, "out": true, "public": true, "_site": true}

type gitInfo struct {
	Root     string // 当前 worktree 的根
	Origin   string // origin 远端 URL，可能为空
	RepoName string
}

// lookupGit 返回 dir 所在的 git 仓库信息；不在仓库里或没装 git 时返回 nil。
func lookupGit(ctx context.Context, dir string) *gitInfo {
	run := func(args ...string) string {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	root := run("rev-parse", "--show-toplevel")
	if root == "" {
		return nil
	}
	g := &gitInfo{Root: root, Origin: run("remote", "get-url", "origin")}
	if g.Origin != "" {
		g.RepoName = repoNameFromURL(g.Origin)
	}
	if g.RepoName == "" {
		// 取主 worktree 的目录名，在 linked worktree 里推送也得到同一个名字
		common := run("rev-parse", "--path-format=absolute", "--git-common-dir")
		switch {
		case common == "":
			g.RepoName = filepath.Base(root)
		case filepath.Base(common) == ".git":
			g.RepoName = filepath.Base(filepath.Dir(common))
		default:
			g.RepoName = strings.TrimSuffix(filepath.Base(common), ".git")
		}
	}
	return g
}

// repoNameFromURL 取远端 URL 的末段：git@github.com:a/b.git、https://x/a/b 都得到 b。
func repoNameFromURL(u string) string {
	u = strings.TrimRight(u, "/")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	return strings.TrimSuffix(u, ".git")
}

// normalizeRemote 去掉远端 URL 里的凭据，统一大小写和 .git 后缀，用作来源指纹（会存到服务端）。
func normalizeRemote(raw string) string {
	raw = strings.TrimSuffix(strings.TrimRight(raw, "/"), ".git")
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		return strings.ToLower(u.Host) + u.Path
	}
	// scp 形式：user@host:path
	if at := strings.Index(raw, "@"); at >= 0 {
		raw = raw[at+1:]
	}
	if host, p, ok := strings.Cut(raw, ":"); ok {
		return strings.ToLower(host) + "/" + strings.TrimPrefix(p, "/")
	}
	return raw
}

// inferIdentity 按「git 仓库名 + 相对路径」确定性地推断原型名，不写任何文件。
func inferIdentity(ctx context.Context, target string, isDir bool) (identity, error) {
	dir := target
	if !isDir {
		dir = filepath.Dir(target)
	}
	var base, rel, fingerprint, source string
	if g := lookupGit(ctx, dir); g != nil {
		base, source = g.RepoName, "git 仓库名"
		r, err := filepath.Rel(g.Root, target)
		if err != nil {
			return identity{}, err
		}
		rel = filepath.ToSlash(r)
		if g.Origin != "" {
			fingerprint = "git:" + normalizeRemote(g.Origin)
		} else {
			fingerprint = "path:" + hostname() + ":" + g.Root
		}
	} else {
		base, source = filepath.Base(dir), "目录名"
		if !isDir {
			rel = filepath.Base(target)
		}
		fingerprint = "path:" + hostname() + ":" + dir
	}
	if rel == "." || buildDirs[rel] {
		rel = ""
	}
	if !isDir {
		rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	}
	name := publish.Slugify(base)
	if rel != "" {
		name = publish.Slugify(base + "-" + rel)
		source += " + 路径"
	}
	if name == "" {
		return identity{}, &usageError{msg: "cannot infer a prototype name from " + target + "; pass --name <slug>"}
	}
	return identity{Name: name, Source: source, Inferred: true, Fingerprint: fingerprint}, nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
