package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/smokecat/easel/internal/config"
	"github.com/smokecat/easel/internal/publish"
)

// devSuffix 加在推断出的名字后面：同一个仓库常常既推构建产物、又暴露 dev server，两者不能共用一个原型。
const devSuffix = "-dev"

func runExpose(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "expose")
	fset.Usage = func() {
		fmt.Fprint(fset.Output(), "用法：easel expose <端口> [参数]\n\n"+
			"把服务器本机上正在运行的 dev server（如 vite、next dev）注册成原型，stdout 输出预览链接。\n"+
			"注册长期有效：dev server 重启、暂时停掉都不用重新注册；换端口或换项目时再运行一次。\n"+
			"原型名按 --name > EASEL_NAME > easel.toml > git 仓库名 + \"-dev\" 的顺序确定（对当前目录推断）。\n\n")
		fset.PrintDefaults()
	}
	name := fset.String("name", "", "原型名（缺省按规则推断）")
	fset.StringVar(name, "n", "", "--name 的简写")
	preserve := fset.Bool("preserve-host", false, "把公网 Host 原样转给 dev server（默认伪装成 localhost:<端口>，Vite / Next 零配置可用）")
	jsonOut := fset.Bool("json", false, "stdout 输出 JSON")
	conn := addConnFlags(fset)
	pos, err := parseInterspersed(fset, args)
	if err != nil {
		return err
	}
	return jsonResult(env, *jsonOut, func() error {
		if len(pos) != 1 {
			return &usageError{msg: "expose takes exactly one port", hint: "easel expose 5173"}
		}
		port, err := strconv.Atoi(pos[0])
		if err != nil || port < 1 || port > 65535 {
			return &usageError{msg: fmt.Sprintf("invalid port %q", pos[0])}
		}
		id, err := exposeIdentity(ctx, *name)
		if err != nil {
			return err
		}
		if err := publish.ValidName(id.Name); err != nil {
			return &usageError{msg: publish.AsError(err).Message}
		}
		fmt.Fprintf(env.Stderr, "原型 %s（来自 %s）\n", id.Name, id.Source)
		c, err := conn.connect()
		if err != nil {
			return err
		}
		mode := "localhost"
		if *preserve {
			mode = "preserve"
		}
		res, err := c.Expose(ctx, id.Name, publish.ExposeRequest{
			Port: port, HostMode: mode, Source: publish.Source{Fingerprint: id.Fingerprint, Inferred: id.Inferred},
		})
		if err != nil {
			return err
		}
		for _, w := range res.Warnings {
			fmt.Fprintf(env.Stderr, "warning: %s\n", w)
		}
		if res.Resumed {
			fmt.Fprintln(env.Stderr, "已解除之前的分享暂停")
		}
		if *jsonOut {
			writeJSONTo(env.Stdout, struct {
				OK bool `json:"ok"`
				*publish.ExposeResult
			}{true, res})
			return nil
		}
		verb := "已更新"
		if res.Prototype.Created {
			verb = "已注册"
		}
		fmt.Fprintf(env.Stderr, "%s：端口 %d → %s（Host 模式 %s）\n", verb, port, res.URLs.Preview, res.HostMode)
		fmt.Fprintln(env.Stdout, res.URLs.Preview)
		return nil
	}())
}

// exposeIdentity 按 --name > EASEL_NAME > easel.toml > 推断（加 -dev 后缀）确定原型名，推断时以当前目录为准。
func exposeIdentity(ctx context.Context, flagName string) (identity, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return identity{}, err
	}
	stop := ""
	if g := lookupGit(ctx, cwd); g != nil {
		stop = g.Root
	}
	proj, err := config.FindProject(cwd, stop)
	if err != nil {
		return identity{}, &usageError{msg: err.Error()}
	}
	id, inferErr := inferIdentity(ctx, cwd, true)
	switch {
	case flagName != "":
		return identity{Name: flagName, Source: "--name", Fingerprint: id.Fingerprint}, nil
	case os.Getenv("EASEL_NAME") != "":
		return identity{Name: os.Getenv("EASEL_NAME"), Source: "EASEL_NAME", Fingerprint: id.Fingerprint}, nil
	case proj != nil && proj.Name != "":
		return identity{Name: proj.Name + devSuffix, Source: config.ProjectFile + " + " + devSuffix, Fingerprint: id.Fingerprint}, nil
	case inferErr != nil:
		return identity{}, inferErr
	}
	id.Name = publish.Slugify(id.Name + devSuffix)
	id.Source += " + " + devSuffix
	return id, nil
}
