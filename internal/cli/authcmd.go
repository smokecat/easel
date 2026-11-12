package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/smokecat/easel/internal/api"
	"github.com/smokecat/easel/internal/client"
	"github.com/smokecat/easel/internal/config"
)

// runGroup 分发 `easel <group> <sub> …` 形式的子命令。
func runGroup(ctx context.Context, env *Env, group string, args []string, subs []command) error {
	usage := func(w io.Writer) {
		fmt.Fprintf(w, "用法：easel %s <子命令> [参数]\n\n子命令：\n", group)
		for _, c := range subs {
			fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
		}
		fmt.Fprintf(w, "\n运行 `easel %s <子命令> -h` 查看参数。\n", group)
	}
	if len(args) == 0 {
		usage(env.Stderr)
		return &usageError{msg: "missing subcommand"}
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		usage(env.Stdout)
		return flag.ErrHelp
	}
	for _, c := range subs {
		if c.name == args[0] {
			return c.run(ctx, env, args[1:])
		}
	}
	return &usageError{msg: fmt.Sprintf("unknown subcommand %q", args[0]), hint: "run `easel " + group + " help`"}
}

// jsonResult 在 --json 下把错误也写成 JSON 对象。
func jsonResult(env *Env, jsonOut bool, err error) error {
	if err != nil && jsonOut && !errors.Is(err, flag.ErrHelp) {
		writeJSONTo(env.Stdout, struct {
			OK    bool    `json:"ok"`
			Error failure `json:"error"`
		}{false, classify(err)})
	}
	return err
}

// ---- admin ----

func runAdmin(ctx context.Context, env *Env, args []string) error {
	return runGroup(ctx, env, "admin", args, []command{
		{"passwd", "设置或更换管理员密码（只能在服务端所在的机器上运行）", runAdminPasswd},
	})
}

func runAdminPasswd(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "admin passwd")
	fromStdin := fset.Bool("password-stdin", false, "从 stdin 读密码（第一行）")
	socket := fset.String("socket", config.DefaultSocket(), "本机服务端的 unix socket（也可用 EASEL_SOCKET）")
	if err := parseFlags(fset, args); err != nil {
		return err
	}
	pw, err := readSecret(env, "new admin password", "--password-stdin", *fromStdin, true)
	if err != nil {
		return err
	}
	if err := client.NewSocket(*socket).SetAdminPassword(ctx, pw); err != nil {
		return err
	}
	fmt.Fprintln(env.Stderr, "管理员密码已更新，所有已登录的后台会话已注销")
	return nil
}

// ---- login / logout ----

func runLogin(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "login")
	fset.Usage = func() {
		fmt.Fprint(fset.Output(), "用法：easel login --server <url> [--token-stdin]\n\n"+
			"保存远端服务端的地址和 API token（在服务端上用 `easel token create` 创建）。\n"+
			"终端里会提示粘贴 token；非交互环境用 --token-stdin。\n\n")
		fset.PrintDefaults()
	}
	server := fset.String("server", "", "后台地址，如 https://easel.example.com（必填）")
	fromStdin := fset.Bool("token-stdin", false, "从 stdin 读 token（第一行）")
	jsonOut := fset.Bool("json", false, "stdout 输出 JSON")
	if err := parseFlags(fset, args); err != nil {
		return err
	}
	return jsonResult(env, *jsonOut, login(ctx, env, *server, *fromStdin, *jsonOut))
}

func login(ctx context.Context, env *Env, server string, fromStdin, jsonOut bool) error {
	if server == "" {
		return &usageError{msg: "--server is required", hint: "easel login --server https://easel.example.com"}
	}
	server, err := normalizeServer(server)
	if err != nil {
		return err
	}
	token, err := readSecret(env, "API token", "--token-stdin", fromStdin, false)
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	who, err := client.NewRemote(server, token).WhoAmI(ctx)
	if err != nil {
		return err
	}
	if err := config.SaveCredentials(config.Credentials{Server: server, Token: token}); err != nil {
		return err
	}
	name := ""
	if who.Token != nil {
		name = who.Token.Name
	}
	if jsonOut {
		writeJSONTo(env.Stdout, map[string]any{"ok": true, "server": server, "token": map[string]any{"name": name, "scopes": who.Scopes},
			"credentials": config.CredentialsPath()})
		return nil
	}
	fmt.Fprintf(env.Stderr, "已登录 %s（token %q，权限 %s），凭据保存在 %s\n", server, name, strings.Join(who.Scopes, ","), config.CredentialsPath())
	return nil
}

func runLogout(_ context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "logout")
	if err := parseFlags(fset, args); err != nil {
		return err
	}
	removed, err := config.RemoveCredentials()
	if err != nil {
		return err
	}
	if removed {
		fmt.Fprintln(env.Stderr, "已删除保存的凭据；token 本身仍然有效，需要时在服务端运行 `easel token revoke <id>`")
	} else {
		fmt.Fprintln(env.Stderr, "没有保存的凭据")
	}
	return nil
}

// ---- token ----

func runToken(ctx context.Context, env *Env, args []string) error {
	return runGroup(ctx, env, "token", args, []command{
		{"create", "创建 API token（明文只显示这一次）", runTokenCreate},
		{"list", "列出 API token", runTokenList},
		{"revoke", "撤销 API token", runTokenRevoke},
	})
}

func runTokenCreate(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "token create")
	name := fset.String("name", "", "token 名字，用来辨认（必填）")
	scopes := fset.String("scope", "read,write", "权限，逗号分隔：read, write, share")
	expires := fset.String("expires", "", "有效期，如 90d、12h（缺省不过期）")
	jsonOut := fset.Bool("json", false, "stdout 输出 JSON")
	conn := addConnFlags(fset)
	if err := parseFlags(fset, args); err != nil {
		return err
	}
	return jsonResult(env, *jsonOut, func() error {
		if *name == "" {
			return &usageError{msg: "--name is required", hint: "easel token create --name laptop"}
		}
		ttl, err := parseExpiry(*expires)
		if err != nil {
			return err
		}
		c, err := conn.connect()
		if err != nil {
			return err
		}
		t, err := c.CreateToken(ctx, api.CreateTokenRequest{Name: *name, Scopes: strings.Split(*scopes, ","), ExpiresIn: int64(ttl.Seconds())})
		if err != nil {
			return err
		}
		if *jsonOut {
			writeJSONTo(env.Stdout, struct {
				OK bool `json:"ok"`
				*api.TokenInfo
			}{true, t})
			return nil
		}
		fmt.Fprintf(env.Stderr, "已创建 token #%d %q（权限 %s）。明文只显示这一次：\n", t.ID, t.Name, strings.Join(t.Scopes, ","))
		fmt.Fprintln(env.Stdout, t.Token)
		return nil
	}())
}

func runTokenList(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "token list")
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
		ts, err := c.ListTokens(ctx)
		if err != nil {
			return err
		}
		if *jsonOut {
			writeJSONTo(env.Stdout, map[string]any{"ok": true, "tokens": ts})
			return nil
		}
		tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tTOKEN\tSCOPES\tSTATUS\tLAST USED\tEXPIRES")
		for _, t := range ts {
			fmt.Fprintf(tw, "%d\t%s\t%s…\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Hint, strings.Join(t.Scopes, ","), t.Status, orDash(t.LastUsedAt), orDash(t.ExpiresAt))
		}
		return tw.Flush()
	}())
}

func runTokenRevoke(ctx context.Context, env *Env, args []string) error {
	return revoke(ctx, env, "token revoke", args, func(c *client.Client, id int64) error { return c.RevokeToken(ctx, id) })
}

// ---- share ----

func runShare(ctx context.Context, env *Env, args []string) error {
	return runGroup(ctx, env, "share", args, []command{
		{"create", "给原型创建分享链接（stdout 输出链接）", runShareCreate},
		{"list", "列出分享链接（链接可以再次复制）", runShareList},
		{"revoke", "撤销分享链接，立即生效", runShareRevoke},
	})
}

func runShareCreate(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "share create")
	fset.Usage = func() {
		fmt.Fprint(fset.Output(), "用法：easel share create <原型名> [参数]\n\n")
		fset.PrintDefaults()
	}
	expires := fset.String("expires", "", "有效期，如 7d、12h（缺省不过期）")
	note := fset.String("note", "", "备注，比如给谁的")
	password := fset.Bool("password", false, "要求访客输入密码（在终端里提示输入）")
	pwStdin := fset.Bool("password-stdin", false, "要求访客输入密码，密码从 stdin 读（第一行）")
	jsonOut := fset.Bool("json", false, "stdout 输出 JSON")
	conn := addConnFlags(fset)
	pos, err := parseInterspersed(fset, args)
	if err != nil {
		return err
	}
	return jsonResult(env, *jsonOut, func() error {
		if len(pos) != 1 {
			return &usageError{msg: "share create takes exactly one prototype name", hint: "easel share create <name>"}
		}
		ttl, err := parseExpiry(*expires)
		if err != nil {
			return err
		}
		var pw string
		if *password || *pwStdin {
			if pw, err = readSecret(env, "share password", "--password-stdin", *pwStdin, true); err != nil {
				return err
			}
		}
		c, err := conn.connect()
		if err != nil {
			return err
		}
		sh, err := c.CreateShare(ctx, pos[0], api.CreateShareRequest{Note: *note, Password: pw, ExpiresIn: int64(ttl.Seconds())})
		if err != nil {
			return err
		}
		if *jsonOut {
			writeJSONTo(env.Stdout, struct {
				OK bool `json:"ok"`
				*api.ShareInfo
			}{true, sh})
			return nil
		}
		desc := "不过期"
		if sh.ExpiresAt != nil {
			desc = "有效期至 " + *sh.ExpiresAt
		}
		if sh.HasPassword {
			desc += "，需要密码"
		}
		fmt.Fprintf(env.Stderr, "分享链接 #%d（%s）；撤销用 `easel share revoke %d`\n", sh.ID, desc, sh.ID)
		fmt.Fprintln(env.Stdout, sh.URL)
		return nil
	}())
}

func runShareList(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "share list")
	fset.Usage = func() {
		fmt.Fprint(fset.Output(), "用法：easel share list [原型名] [参数]\n\n")
		fset.PrintDefaults()
	}
	all := fset.Bool("all", false, "也列出已过期和已撤销的")
	jsonOut := fset.Bool("json", false, "stdout 输出 JSON（总是包含全部状态）")
	conn := addConnFlags(fset)
	pos, err := parseInterspersed(fset, args)
	if err != nil {
		return err
	}
	return jsonResult(env, *jsonOut, func() error {
		if len(pos) > 1 {
			return &usageError{msg: "share list takes at most one prototype name"}
		}
		name := ""
		if len(pos) == 1 {
			name = pos[0]
		}
		c, err := conn.connect()
		if err != nil {
			return err
		}
		shares, err := c.ListShares(ctx, name)
		if err != nil {
			return err
		}
		if *jsonOut {
			writeJSONTo(env.Stdout, map[string]any{"ok": true, "shares": shares})
			return nil
		}
		tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tPROTOTYPE\tSTATUS\tPASSWORD\tEXPIRES\tNOTE\tURL")
		for _, sh := range shares {
			if !*all && sh.Status != "active" {
				continue
			}
			pw := "no"
			if sh.HasPassword {
				pw = "yes"
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", sh.ID, sh.Prototype, sh.Status, pw, orDash(sh.ExpiresAt), sh.Note, sh.URL)
		}
		return tw.Flush()
	}())
}

func runShareRevoke(ctx context.Context, env *Env, args []string) error {
	return revoke(ctx, env, "share revoke", args, func(c *client.Client, id int64) error { return c.RevokeShare(ctx, id) })
}

// revoke 是 `token revoke` / `share revoke` 的共同实现：一个数字 ID。
func revoke(_ context.Context, env *Env, name string, args []string, do func(*client.Client, int64) error) error {
	fset := newFlagSet(env, name)
	fset.Usage = func() {
		fmt.Fprintf(fset.Output(), "用法：easel %s <id> [参数]\n\n", name)
		fset.PrintDefaults()
	}
	jsonOut := fset.Bool("json", false, "stdout 输出 JSON")
	conn := addConnFlags(fset)
	pos, err := parseInterspersed(fset, args)
	if err != nil {
		return err
	}
	return jsonResult(env, *jsonOut, func() error {
		if len(pos) != 1 {
			return &usageError{msg: name + " takes exactly one id"}
		}
		id, err := strconv.ParseInt(pos[0], 10, 64)
		if err != nil || id <= 0 {
			return &usageError{msg: fmt.Sprintf("invalid id %q", pos[0]), hint: "see the ID column of the list command"}
		}
		c, err := conn.connect()
		if err != nil {
			return err
		}
		if err := do(c, id); err != nil {
			return err
		}
		if *jsonOut {
			writeJSONTo(env.Stdout, map[string]any{"ok": true, "id": id, "revoked": true})
			return nil
		}
		fmt.Fprintf(env.Stderr, "已撤销 #%d\n", id)
		return nil
	}())
}

func orDash(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}
