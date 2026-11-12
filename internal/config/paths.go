package config

import (
	"os"
	"path/filepath"
)

// DefaultDataDir 是服务端数据目录：$XDG_DATA_HOME/easel，默认 ~/.local/share/easel。
func DefaultDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "easel")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "easel")
	}
	return "easel-data"
}

// DefaultSocket 是本机 unix socket 的位置：EASEL_SOCKET，否则 $XDG_RUNTIME_DIR/easel/easel.sock，
// 没有 XDG_RUNTIME_DIR（如 macOS）时放在数据目录里。服务端和 CLI 用同一规则，同机免配置。
func DefaultSocket() string {
	if s := os.Getenv("EASEL_SOCKET"); s != "" {
		return s
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "easel", "easel.sock")
	}
	return filepath.Join(DefaultDataDir(), "easel.sock")
}
