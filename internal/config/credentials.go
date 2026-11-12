package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Credentials 是 CLI 登录远端服务端后保存的凭据。
type Credentials struct {
	// Server 是后台地址，如 https://easel.example.com。
	Server string `toml:"server"`
	Token  string `toml:"token"`
}

// CredentialsPath 是凭据文件的位置：EASEL_CREDENTIALS，否则 $XDG_CONFIG_HOME/easel/credentials.toml，
// 默认 ~/.config/easel/credentials.toml（macOS 也用这个位置，和数据目录的规则一致）。
func CredentialsPath() string {
	if p := os.Getenv("EASEL_CREDENTIALS"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "easel", "credentials.toml")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "easel", "credentials.toml")
	}
	return "easel-credentials.toml"
}

// LoadCredentials 读取凭据文件；不存在时返回 nil, nil。
func LoadCredentials() (*Credentials, error) {
	p := CredentialsPath()
	var c Credentials
	if _, err := toml.DecodeFile(p, &c); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if c.Server == "" || c.Token == "" {
		return nil, fmt.Errorf("%s: server and token are required; run `easel login` again", p)
	}
	return &c, nil
}

// SaveCredentials 原子地写入凭据文件（权限 0600，目录 0700）。
func SaveCredentials(c Credentials) error {
	p := CredentialsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".credentials-*.toml")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // rename 成功后这里是空操作
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := fmt.Fprintf(f, "# easel CLI credentials. Keep this file private.\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// RemoveCredentials 删除凭据文件；不存在时返回 false。
func RemoveCredentials() (bool, error) {
	err := os.Remove(CredentialsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
