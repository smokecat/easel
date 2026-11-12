// Package web 嵌入管理面板的构建产物。
//
// dist/ 由 `make web`（vite build）生成；仓库里只保留 dist/.gitkeep，
// 这样没构建前端时 Go 代码也能编译和测试。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist 返回以 dist/ 为根的管理面板文件。
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist 是编译期固定的目录，不会出错
	}
	return sub
}
