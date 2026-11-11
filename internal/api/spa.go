package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// newSPA 提供前端页面：有对应文件就返回文件，否则返回 index.html，由前端路由处理。
// 前端还没构建（没有 index.html）时返回一段提示。
func newSPA(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
			return
		}
		index, err := fs.ReadFile(ui, "index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(notBuiltPage))
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(ui, name); err == nil && !st.IsDir() {
				// Vite 产物的 assets/ 文件名带内容哈希，可以长期缓存
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			// 看起来是静态文件但不存在，直接 404，避免把 index.html 当成脚本返回
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

const notBuiltPage = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>deployhub</title>
<style>body{font-family:system-ui,sans-serif;max-width:640px;margin:15vh auto;padding:0 16px;color:#222}code{background:#f2f2f2;padding:2px 6px;border-radius:4px}</style>
</head><body>
<h1>前端还没有构建</h1>
<p>在项目目录运行 <code>make build</code>（会先构建前端再编译 Go 程序），然后重新启动 deployhub。</p>
<p>开发时也可以运行 <code>make dev-web</code>，用前端开发服务器打开页面。</p>
</body></html>`
