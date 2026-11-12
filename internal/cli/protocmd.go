package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

func runList(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "list")
	jsonOut := fset.Bool("json", false, "stdout 输出 JSON")
	conn := addConnFlags(fset)
	if err := parseFlags(fset, args); err != nil {
		return err
	}
	return jsonResult(env, *jsonOut, func() error {
		c, err := conn.connect()
		if err != nil {
			return err
		}
		ps, err := c.ListPrototypes(ctx)
		if err != nil {
			return err
		}
		if *jsonOut {
			writeJSONTo(env.Stdout, map[string]any{"ok": true, "prototypes": ps})
			return nil
		}
		tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tKIND\tVERSION\tUPDATED\tURL")
		for _, p := range ps {
			kind, version := p.ServeKind, "-"
			if p.Current != nil {
				if p.ServeKind == "static" {
					kind = p.Current.Kind
				}
				version = fmt.Sprintf("v%d", p.Current.Number)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, kind, version, p.UpdatedAt, p.PreviewURL)
		}
		return tw.Flush()
	}())
}

func runRm(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "rm")
	fset.Usage = func() {
		fmt.Fprint(fset.Output(), "用法：easel rm <原型名> [参数]\n\n"+
			"删除原型：所有版本、分享链接一并删除，预览地址立即失效，不可恢复。\n"+
			"终端里会要求再输入一遍原型名确认；非交互环境要加 --yes。\n\n")
		fset.PrintDefaults()
	}
	yes := fset.Bool("yes", false, "不确认，直接删除")
	jsonOut := fset.Bool("json", false, "stdout 输出 JSON")
	conn := addConnFlags(fset)
	pos, err := parseInterspersed(fset, args)
	if err != nil {
		return err
	}
	return jsonResult(env, *jsonOut, func() error {
		if len(pos) != 1 {
			return &usageError{msg: "rm takes exactly one prototype name", hint: "easel rm <name>"}
		}
		name := pos[0]
		c, err := conn.connect()
		if err != nil {
			return err
		}
		p, err := c.GetPrototype(ctx, name)
		if err != nil {
			return err
		}
		if !*yes {
			f, ok := env.Stdin.(*os.File)
			if !ok || !term.IsTerminal(int(f.Fd())) {
				return &usageError{msg: "refusing to delete without confirmation: stdin is not a terminal", hint: "pass --yes"}
			}
			fmt.Fprintf(env.Stderr, "将删除原型 %s（%d 个版本），预览地址和分享链接立即失效，不可恢复。\n输入原型名确认：", p.Name, p.Versions)
			line, _ := bufio.NewReader(f).ReadString('\n')
			if strings.TrimSpace(line) != p.Name {
				return &usageError{msg: "confirmation did not match; nothing deleted"}
			}
		}
		if err := c.DeletePrototype(ctx, name); err != nil {
			return err
		}
		if *jsonOut {
			writeJSONTo(env.Stdout, map[string]any{"ok": true, "deleted": name})
			return nil
		}
		fmt.Fprintf(env.Stderr, "已删除 %s\n", name)
		return nil
	}())
}
