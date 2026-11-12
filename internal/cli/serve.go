package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/smokecat/easel/internal/api"
	"github.com/smokecat/easel/internal/auth"
	"github.com/smokecat/easel/internal/blob"
	"github.com/smokecat/easel/internal/config"
	"github.com/smokecat/easel/internal/content"
	"github.com/smokecat/easel/internal/publish"
	"github.com/smokecat/easel/internal/server"
	"github.com/smokecat/easel/internal/store"
)

// DefaultAddr 是服务端默认监听地址。easel 不内置 TLS，只监听本机，由前置的反向代理对外。
const DefaultAddr = "127.0.0.1:7780"

type serveConfig struct {
	DataDir    string
	AppURL     string
	ContentURL string
	Assets     fs.FS
	Log        *slog.Logger
}

// instance 是装配好的服务端。
type instance struct {
	store  *store.Store
	auth   *auth.Service
	tcp    http.Handler
	socket http.Handler
	hosts  config.Hosts
}

func (i *instance) Close() error { return i.store.Close() }

func openInstance(ctx context.Context, c serveConfig) (*instance, error) {
	hosts, err := config.ParseHosts(c.AppURL, c.ContentURL)
	if err != nil {
		return nil, &usageError{msg: err.Error()}
	}
	if err := os.MkdirAll(c.DataDir, 0o750); err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, filepath.Join(c.DataDir, "easel.db"))
	if err != nil {
		return nil, err
	}
	blobs, err := blob.Open(filepath.Join(c.DataDir, "blobs"))
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	key, err := auth.LoadOrCreateKey(filepath.Join(c.DataDir, "secret.key"))
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("server key: %w", err)
	}
	authn := auth.New(st, hosts, key, c.Log)
	pub := &publish.Service{Store: st, Blobs: blobs, Hosts: hosts, Limits: publish.DefaultLimits, PreviewRequiresLogin: true}
	opts := server.Options{
		Assets:  c.Assets,
		Hosts:   hosts,
		Auth:    authn,
		API:     &api.API{Publish: pub, Auth: authn, Log: c.Log, TempDir: filepath.Join(c.DataDir, "blobs", "tmp")},
		Content: &content.Handler{Store: st, Blobs: blobs, Hosts: hosts, Log: c.Log},
	}
	return &instance{store: st, auth: authn, tcp: server.New(opts), socket: server.NewSocket(opts), hosts: hosts}, nil
}

// sweepLoop 每小时清理一次过期的会话和票据。
func (i *instance) sweepLoop(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := i.auth.Sweep(ctx); err != nil && ctx.Err() == nil {
			log.Warn("sweep expired sessions", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func runServe(ctx context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "serve")
	addr := fset.String("addr", DefaultAddr, "TCP 监听地址")
	dataDir := fset.String("data", config.DefaultDataDir(), "数据目录（SQLite 和 blob 库）")
	socket := fset.String("socket", config.DefaultSocket(), "本机 unix socket 路径，同机的 CLI 经它免登录；设为空串关闭")
	appURL := fset.String("app-url", "", "后台的公开地址（默认 http://127.0.0.1:<端口>）")
	contentURL := fset.String("content-url", "", "内容域的公开地址，原型在它的一级子域上（默认 http://localhost:<端口>）")
	if err := parseFlags(fset, args); err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(env.Stderr, nil))
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if *appURL == "" {
		*appURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	if *contentURL == "" {
		*contentURL = fmt.Sprintf("http://localhost:%d", port)
	}

	inst, err := openInstance(ctx, serveConfig{
		DataDir: *dataDir, AppURL: *appURL, ContentURL: *contentURL, Assets: env.WebAssets, Log: log,
	})
	if err != nil {
		return err
	}
	defer inst.Close()

	servers := []*http.Server{newHTTPServer(inst.tcp, log)}
	listeners := []net.Listener{ln}
	if *socket != "" {
		sln, err := listenSocket(*socket)
		if err != nil {
			return fmt.Errorf("socket: %w", err)
		}
		defer os.Remove(*socket)
		servers = append(servers, newHTTPServer(inst.socket, log))
		listeners = append(listeners, sln)
	}

	log.Info("easel listening", "addr", "http://"+ln.Addr().String(), "socket", *socket,
		"app", inst.hosts.AppOrigin(), "content", "*."+inst.hosts.Content.Host, "data", *dataDir,
		"version", versionInfo().Version)
	if ok, err := inst.auth.HasAdminPassword(ctx); err != nil {
		return err
	} else if !ok {
		log.Warn("no admin password set: web sign-in is disabled until you run `easel admin passwd` on this machine")
	}
	go inst.sweepLoop(ctx, log)

	errc := make(chan error, len(servers))
	for i, srv := range servers {
		go func() { errc <- srv.Serve(listeners[i]) }()
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	for range servers {
		if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}

func newHTTPServer(h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// listenSocket 监听 unix socket（权限 0660）。旧的 socket 文件连不上时视为残留并删除；连得上说明已有实例在跑。
func listenSocket(p string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(p); err == nil {
		if c, err := net.DialTimeout("unix", p, time.Second); err == nil {
			_ = c.Close()
			return nil, fmt.Errorf("%s is in use: is another easel serve running?", p)
		}
		if err := os.Remove(p); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", p)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(p, 0o660); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}
