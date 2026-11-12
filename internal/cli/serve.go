package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/smokecat/easel/internal/server"
)

// DefaultAddr 是服务端默认监听地址。easel 不内置 TLS，只监听本机，由前置的反向代理对外。
const DefaultAddr = "127.0.0.1:7780"

func runServe(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "serve")
	addr := fset.String("addr", DefaultAddr, "监听地址")
	if err := parseFlags(fset, args); err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(env.Stderr, nil))
	srv := &http.Server{
		Handler:           server.New(server.Options{Assets: env.WebAssets}),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Info("easel listening", "addr", "http://"+ln.Addr().String(), "version", versionInfo().Version)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
