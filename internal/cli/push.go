package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/smokecat/easel/internal/client"
	"github.com/smokecat/easel/internal/config"
	"github.com/smokecat/easel/internal/publish"
)

// stringList 是可重复的 flag。
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type pushOptions struct {
	name, note, socket string
	spa, jsonOut, dry  bool
	include, exclude   stringList
}

func runPush(ctx context.Context, env *Env, args []string) error {
	var o pushOptions
	fset := newFlagSet(env, "push")
	fset.Usage = func() {
		fmt.Fprint(fset.Output(), "用法：easel push [path] [参数]\n\n"+
			"推送目录或单个文件，stdout 输出预览链接。path 缺省为 easel.toml 的 dir，再缺省为当前目录。\n"+
			"原型名按 --name > EASEL_NAME > easel.toml > git 仓库名 + 相对路径 的顺序确定。\n\n")
		fset.PrintDefaults()
	}
	fset.StringVar(&o.name, "name", "", "原型名（缺省按规则推断）")
	fset.StringVar(&o.name, "n", "", "--name 的简写")
	fset.StringVar(&o.note, "note", "", "版本备注")
	fset.StringVar(&o.note, "m", "", "--note 的简写")
	fset.BoolVar(&o.spa, "spa", false, "未命中的页面路径回退到入口页")
	fset.BoolVar(&o.jsonOut, "json", false, "stdout 输出一个 JSON 对象（成功和失败都是）")
	fset.BoolVar(&o.dry, "dry-run", false, "只列出会上传哪些文件")
	fset.Var(&o.include, "include", "把默认排除的文件加回来（gitignore 语法，可重复）")
	fset.Var(&o.exclude, "exclude", "追加排除规则（gitignore 语法，可重复）")
	fset.StringVar(&o.socket, "socket", config.DefaultSocket(), "服务端的 unix socket（也可用 EASEL_SOCKET）")

	paths, err := parseInterspersed(fset, args)
	if err != nil {
		return err
	}
	if len(paths) > 1 {
		return &usageError{msg: "push takes one path; pushing several paths at once is not supported yet"}
	}
	err = push(ctx, env, o, paths)
	if err != nil && o.jsonOut && !errors.Is(err, flag.ErrHelp) {
		writeJSONTo(env.Stdout, struct {
			OK    bool    `json:"ok"`
			Error failure `json:"error"`
		}{false, classify(err)})
	}
	return err
}

func push(ctx context.Context, env *Env, o pushOptions, paths []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	g := lookupGit(ctx, cwd)
	stop := ""
	if g != nil {
		stop = g.Root
	}
	proj, err := config.FindProject(cwd, stop)
	if err != nil {
		return &usageError{msg: err.Error()}
	}

	// 推送目标
	var target string
	switch {
	case len(paths) == 1:
		target = paths[0]
	case proj != nil && proj.Dir != "":
		target = filepath.Join(filepath.Dir(proj.Path), proj.Dir)
	default:
		target = "."
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return &usageError{msg: fmt.Sprintf("%s does not exist", target)}
	}
	if err != nil {
		return err
	}
	if home, _ := os.UserHomeDir(); target == "/" || (home != "" && target == home) {
		return &usageError{msg: fmt.Sprintf("refusing to push %s", target), hint: "push a project or build directory instead"}
	}

	// 原型身份
	id, err := inferIdentity(ctx, target, info.IsDir())
	if err != nil && o.name == "" && os.Getenv("EASEL_NAME") == "" && (proj == nil || proj.Name == "") {
		return err
	}
	switch {
	case o.name != "":
		id = identity{Name: o.name, Source: "--name", Fingerprint: id.Fingerprint}
	case os.Getenv("EASEL_NAME") != "":
		id = identity{Name: os.Getenv("EASEL_NAME"), Source: "EASEL_NAME", Fingerprint: id.Fingerprint}
	case proj != nil && proj.Name != "":
		id = identity{Name: proj.Name, Source: config.ProjectFile, Fingerprint: id.Fingerprint}
	}
	if err := publish.ValidName(id.Name); err != nil {
		return &usageError{msg: publish.AsError(err).Message}
	}
	spa := o.spa || (proj != nil && proj.SPA)

	// 忽略规则：默认 → .easelignore → easel.toml → --exclude → --include
	ig, err := publish.NewIgnore()
	if err != nil {
		return err
	}
	if info.IsDir() {
		if f, err := os.Open(filepath.Join(target, ".easelignore")); err == nil {
			err = ig.AddFile(f)
			_ = f.Close()
			if err != nil {
				return &usageError{msg: ".easelignore: " + err.Error()}
			}
		}
	}
	if proj != nil {
		if err := ig.Add(proj.Ignore...); err != nil {
			return &usageError{msg: config.ProjectFile + ": " + err.Error()}
		}
	}
	if err := ig.Add(o.exclude...); err != nil {
		return &usageError{msg: err.Error()}
	}
	if err := ig.Include(o.include...); err != nil {
		return &usageError{msg: err.Error()}
	}

	col, err := publish.Collect(target, ig)
	if err != nil {
		return &usageError{msg: err.Error()}
	}
	for _, w := range col.Warnings {
		fmt.Fprintf(env.Stderr, "warning: %s\n", w)
	}
	if len(col.Skipped) > 0 {
		fmt.Fprintf(env.Stderr, "skipped %d path(s) by ignore rules: %s\n", len(col.Skipped), summarize(col.Skipped, 5))
	}
	if len(col.Files) == 0 {
		return &usageError{msg: fmt.Sprintf("nothing to push in %s", target), hint: "all files were excluded; see --include"}
	}
	fmt.Fprintf(env.Stderr, "原型 %s（来自 %s）\n", id.Name, id.Source)

	if o.dry {
		return dryRun(env, o, id, col)
	}

	if err := publish.HashFiles(col.Files); err != nil {
		return err
	}
	entries := make([]publish.FileEntry, len(col.Files))
	for i, f := range col.Files {
		entries[i] = f.FileEntry
	}

	c := client.NewSocket(o.socket)
	resp, err := c.CreateVersion(ctx, id.Name, publish.CreateRequest{
		Files: entries, Note: o.note, SPA: spa,
		Source: publish.Source{Fingerprint: id.Fingerprint, Inferred: id.Inferred},
	})
	if err != nil {
		return err
	}

	var res *publish.Result
	if resp.Unchanged {
		res = resp.Result
	} else {
		missing := map[string]bool{}
		for _, h := range resp.Missing {
			missing[h] = true
		}
		var upload []publish.LocalFile
		for _, f := range col.Files {
			if missing[f.SHA256] {
				upload = append(upload, f)
				delete(missing, f.SHA256) // 内容相同的文件只传一次
			}
		}
		if len(upload) > 0 {
			fmt.Fprintf(env.Stderr, "上传 %d 个文件（%s）…\n", len(upload), humanBytes(resp.MissingBytes))
			if _, err := c.PutBlobs(ctx, resp.UploadID, upload); err != nil {
				return err
			}
		}
		res, err = c.Commit(ctx, resp.UploadID)
		if err != nil {
			return err
		}
		if !res.Version.Unchanged {
			res.Stats.UploadedFiles, res.Stats.UploadedBytes = len(upload), resp.MissingBytes
		}
	}
	res.Stats.Skipped = append([]string{}, col.Skipped...)
	res.Warnings = append(res.Warnings, col.Warnings...)

	if res.Version.Unchanged {
		fmt.Fprintf(env.Stderr, "内容没有变化，仍是 v%d\n", res.Version.Number)
	} else {
		verb := "新版本"
		if res.Prototype.Created {
			verb = "新原型"
		}
		fmt.Fprintf(env.Stderr, "%s v%d：%d 个文件（%s），上传 %d 个（%s）\n", verb, res.Version.Number,
			res.Stats.Files, humanBytes(res.Stats.Bytes), res.Stats.UploadedFiles, humanBytes(res.Stats.UploadedBytes))
	}
	if !res.Access.PreviewRequiresLogin {
		fmt.Fprintln(env.Stderr, "注意：预览链接目前不需要登录，能访问内容主机的人都能打开")
	}

	if o.jsonOut {
		writeJSONTo(env.Stdout, struct {
			OK bool `json:"ok"`
			*publish.Result
		}{true, res})
		return nil
	}
	fmt.Fprintln(env.Stdout, res.URLs.Preview)
	return nil
}

func dryRun(env *Env, o pushOptions, id identity, col *publish.Collected) error {
	var total int64
	for _, f := range col.Files {
		total += f.Size
	}
	if o.jsonOut {
		type file struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
		}
		files := make([]file, len(col.Files))
		for i, f := range col.Files {
			files[i] = file{f.Path, f.Size}
		}
		writeJSONTo(env.Stdout, map[string]any{
			"ok": true, "dry_run": true, "prototype": map[string]any{"name": id.Name},
			"files": files, "stats": map[string]any{"files": len(files), "bytes": total, "skipped": append([]string{}, col.Skipped...)},
		})
		return nil
	}
	for _, f := range col.Files {
		fmt.Fprintf(env.Stdout, "%s\t%d\n", f.Path, f.Size)
	}
	fmt.Fprintf(env.Stderr, "dry run：%d 个文件（%s），没有上传\n", len(col.Files), humanBytes(total))
	return nil
}

// parseInterspersed 允许 flag 和位置参数混排（easel push dist --json）。
func parseInterspersed(fset *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fset.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, &usageError{msg: "invalid arguments"}
		}
		args = fset.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func summarize(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + ", … (+" + strconv.Itoa(len(items)-n) + ")"
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "B"
}

func writeJSONTo(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
