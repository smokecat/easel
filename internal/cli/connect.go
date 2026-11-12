package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/smokecat/easel/internal/client"
	"github.com/smokecat/easel/internal/config"
)

// connFlags 是各子命令共用的连接参数。
type connFlags struct {
	server, socket string
	fset           *flag.FlagSet
}

func addConnFlags(fset *flag.FlagSet) *connFlags {
	c := &connFlags{fset: fset}
	fset.StringVar(&c.server, "server", "", "远端服务端的后台地址（也可用 EASEL_SERVER）；缺省时优先用本机 socket，再用 `easel login` 保存的服务端")
	fset.StringVar(&c.socket, "socket", config.DefaultSocket(), "本机服务端的 unix socket（也可用 EASEL_SOCKET）")
	return c
}

func (c *connFlags) socketSet() bool {
	set := false
	c.fset.Visit(func(f *flag.Flag) {
		if f.Name == "socket" {
			set = true
		}
	})
	return set
}

// authError 是没有可用凭据（对应退出码 ExitAuth）。
type authError struct{ msg, hint string }

func (e *authError) Error() string { return e.msg }

// connect 按以下顺序选连接：
//  1. 显式的 --server / EASEL_SERVER → TCP，token 取 EASEL_TOKEN 或凭据文件里同一服务端的
//  2. 显式的 --socket，或默认位置的 socket 文件存在 → 本机 unix socket（免登录）
//  3. 凭据文件里有服务端 → TCP
//  4. 都没有 → 失败，提示 easel login
func (c *connFlags) connect() (*client.Client, error) {
	server := c.server
	if server == "" {
		server = os.Getenv("EASEL_SERVER")
	}
	envToken := os.Getenv("EASEL_TOKEN")
	creds, err := config.LoadCredentials()
	if err != nil {
		return nil, err
	}
	if server != "" {
		server, err = normalizeServer(server)
		if err != nil {
			return nil, err
		}
		token := envToken
		if token == "" && creds != nil && creds.Server == server {
			token = creds.Token
		}
		if token == "" {
			return nil, &authError{msg: "no API token for " + server,
				hint: "run `easel login --server " + server + "`, or set EASEL_TOKEN"}
		}
		return client.NewRemote(server, token), nil
	}
	if c.socketSet() || os.Getenv("EASEL_SOCKET") != "" || socketExists(c.socket) {
		return client.NewSocket(c.socket), nil
	}
	if creds != nil {
		token := creds.Token
		if envToken != "" {
			token = envToken
		}
		return client.NewRemote(creds.Server, token), nil
	}
	return nil, &authError{
		msg:  "no easel server: no local socket at " + c.socket + " and not logged in to a remote server",
		hint: "run `easel serve` on this machine, or `easel login --server <url>` to use a remote one",
	}
}

func socketExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().Type() == fs.ModeSocket
}

// normalizeServer 校验并规范化后台地址：只保留 scheme 和 host。
func normalizeServer(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", &usageError{msg: fmt.Sprintf("invalid server URL %q", raw), hint: "use the admin URL, like https://easel.example.com"}
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return "", &usageError{msg: "refusing to send an API token over plain http to " + u.Host, hint: "use https://"}
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

// readSecret 读一个秘密值：fromStdin 时读 stdin 的第一行；否则 stdin 是终端时无回显提示；
// 都不行时以参数错误失败（非交互环境下不提示）。confirm 为 true 时在终端上要求输两次。
func readSecret(env *Env, what, stdinFlag string, fromStdin, confirm bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(env.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return "", &usageError{msg: what + " on stdin is empty"}
		}
		return line, nil
	}
	f, ok := env.Stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", &usageError{msg: "stdin is not a terminal; cannot prompt for the " + what, hint: "pass " + stdinFlag + " and pipe it in"}
	}
	fmt.Fprintf(env.Stderr, "%s: ", capitalize(what))
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(env.Stderr)
	if err != nil {
		return "", err
	}
	if confirm {
		fmt.Fprintf(env.Stderr, "Repeat %s: ", what)
		b2, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(env.Stderr)
		if err != nil {
			return "", err
		}
		if string(b) != string(b2) {
			return "", &usageError{msg: "the two entries do not match"}
		}
	}
	if len(b) == 0 {
		return "", &usageError{msg: what + " is empty"}
	}
	return string(b), nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// parseExpiry 解析有效期：Go 的 duration（12h、30m）或天数（7d）。空串表示不过期。
func parseExpiry(s string) (time.Duration, error) {
	if s == "" || s == "never" {
		return 0, nil
	}
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 {
			return 0, &usageError{msg: fmt.Sprintf("invalid expiry %q", s), hint: "use e.g. 7d, 12h or 30m"}
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil || d <= 0 {
			return 0, &usageError{msg: fmt.Sprintf("invalid expiry %q", s), hint: "use e.g. 7d, 12h or 30m"}
		}
	}
	if d < time.Minute {
		return 0, &usageError{msg: "expiry must be at least 1m"}
	}
	return d, nil
}
