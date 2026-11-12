// easel 是服务端和 CLI 共用的唯一入口，只做参数解析和装配。
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/smokecat/easel/internal/cli"
	"github.com/smokecat/easel/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	env := &cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, WebAssets: web.Dist()}
	code := cli.Run(ctx, env, os.Args[1:])
	stop()
	os.Exit(code)
}
