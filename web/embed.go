// Package web 嵌入前端构建产物（web/dist），由 Go 服务直接提供。
// 前端源码也在本目录，用 pnpm build 构建后再编译 Go 程序。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist 返回前端构建产物。没有构建时只有 .gitkeep，没有 index.html。
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
