// Package cli 实现 easel 的各个子命令、输出格式和退出码。
//
// 约定：结果写 stdout，进度和日志写 stderr；--json 的字段保持稳定；
// 非交互环境下不提示，缺信息时以非零退出码失败并说明该加哪个参数。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
)

// 退出码。
const (
	ExitOK    = 0
	ExitError = 1 // 运行时错误
	ExitUsage = 2 // 参数错误
)

// Env 是子命令运行所需的外部依赖，由 cmd/easel 装配。
type Env struct {
	Stdout, Stderr io.Writer
	// WebAssets 是嵌入的管理面板文件。
	WebAssets fs.FS
}

type command struct {
	name, summary string
	run           func(ctx context.Context, env *Env, args []string) error
}

func commands() []command {
	return []command{
		{"serve", "启动 easel 服务端", runServe},
		{"version", "显示版本信息", runVersion},
	}
}

// usageError 表示参数错误，对应退出码 ExitUsage。
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// Run 执行一条命令行（不含程序名），返回退出码。
func Run(ctx context.Context, env *Env, args []string) int {
	if len(args) == 0 {
		printUsage(env.Stderr)
		return ExitUsage
	}
	name := args[0]
	switch name {
	case "-h", "-help", "--help", "help":
		printUsage(env.Stdout)
		return ExitOK
	case "-v", "--version":
		name = "version"
	}
	for _, c := range commands() {
		if c.name != name {
			continue
		}
		err := c.run(ctx, env, args[1:])
		var ue *usageError
		switch {
		case err == nil, errors.Is(err, flag.ErrHelp):
			return ExitOK
		case errors.As(err, &ue):
			fmt.Fprintf(env.Stderr, "easel %s: %v\n", name, err)
			return ExitUsage
		default:
			fmt.Fprintf(env.Stderr, "easel %s: %v\n", name, err)
			return ExitError
		}
	}
	fmt.Fprintf(env.Stderr, "easel: unknown command %q; run `easel help` for usage\n", name)
	return ExitUsage
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, "easel：上传、管理、预览和分享原型\n\n用法：easel <命令> [参数]\n\n命令：\n")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprint(w, "\n运行 `easel <命令> -h` 查看命令参数。\n")
}

// newFlagSet 创建子命令的 FlagSet：解析错误不自动退出，帮助信息写 stderr。
func newFlagSet(env *Env, name string) *flag.FlagSet {
	fset := flag.NewFlagSet("easel "+name, flag.ContinueOnError)
	fset.SetOutput(env.Stderr)
	return fset
}

// parseFlags 解析参数；参数错误时 flag 包已经把原因和用法写到 stderr。
func parseFlags(fset *flag.FlagSet, args []string) error {
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return &usageError{msg: "invalid arguments"}
	}
	if fset.NArg() > 0 {
		return &usageError{msg: fmt.Sprintf("unexpected argument %q", fset.Arg(0))}
	}
	return nil
}
