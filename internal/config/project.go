package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ProjectFile 是项目级配置的文件名。只放原型身份和推送选项，永远不放 token。
const ProjectFile = "easel.toml"

// Project 是 easel.toml 的内容。
type Project struct {
	Name   string   `toml:"name"`
	Dir    string   `toml:"dir"` // easel push 不带参数时推这个目录（相对 easel.toml 所在目录）
	SPA    bool     `toml:"spa"`
	Ignore []string `toml:"ignore"`

	// Path 是读到的 easel.toml 的绝对路径。
	Path string `toml:"-"`
}

// FindProject 从 start 开始向上找 easel.toml，最多找到 stop（通常是 git 根；为空时只看 start）。
// 没找到时返回 nil, nil。
func FindProject(start, stop string) (*Project, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	for {
		p := filepath.Join(dir, ProjectFile)
		data, err := os.ReadFile(p)
		switch {
		case err == nil:
			var proj Project
			md, err := toml.Decode(string(data), &proj)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p, err)
			}
			if und := md.Undecoded(); len(und) > 0 {
				return nil, fmt.Errorf("%s: unknown key %q", p, und[0].String())
			}
			proj.Path = p
			return &proj, nil
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
		parent := filepath.Dir(dir)
		if stop == "" || dir == stop || parent == dir {
			return nil, nil
		}
		dir = parent
	}
}
