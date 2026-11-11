// Package static 是「静态站点」部署方式：在仓库工作区副本里构建，上传到 <站点目录>/releases/<版本>/，
// 切换 current 软链接。宿主机 Nginx 的 root 指向 <站点目录>/current，不重启任何进程。
package static

import "deployhub/internal/driver"

// Driver 是静态站点部署方式。
type Driver struct{}

// Spec 返回参数声明。
func (Driver) Spec() driver.Spec {
	return driver.Spec{
		Name:    "static",
		Title:   "静态站点",
		Summary: "本地构建前端，上传静态文件，由服务器上的 Nginx 直接提供",
		Description: "在仓库工作区的副本里构建（包含未提交的改动，不动仓库自己的 node_modules），" +
			"上传到 <站点目录>/releases/<版本>/，切换 current 软链接。Nginx 的 root 指向 <站点目录>/current，不重启任何进程。",
		Icon:         "globe",
		DefaultCheck: "<站点目录>/current/index.html 存在即通过",
		Fields: []driver.Field{
			// 构建
			{
				Key: "build_command", Label: "构建命令", Section: "build", Type: driver.TypeTextarea,
				DefaultHint: "按锁文件自动识别，如 pnpm install --frozen-lockfile && pnpm build",
				Help:        "在仓库副本里执行；仓库有 mise.toml 时自动加 mise exec --",
			},
			{
				Key: "dist", Label: "产物目录", Section: "build", Type: driver.TypeText,
				DefaultHint: "自动识别，否则 dist",
				Help:        "路径相对仓库",
			},
			{
				Key: "build_env", Label: "构建环境变量", Section: "build", Type: driver.TypeKeyValue,
				KeyLabel: "变量名", ValueLabel: "值",
				Help: "如 VITE_APP_API_URL = ${PAYMENT_API_URL}；值留空表示不设置，用仓库里的 .env.production",
			},
			{
				Key: "exclude", Label: "不上传的文件", Section: "build", Type: driver.TypeList, Default: []string{"*.map"},
				Help: "通配符，如 *.map、dist.zip",
			},

			// 服务器
			{
				Key: "remote_dir", Label: "站点目录", Section: "server", Type: driver.TypeText,
				DefaultHint: "/srv/www/<应用>",
				Pattern:     `^/\S*$`, PatternHint: "服务器上的绝对路径",
				Help: "Nginx 的 root 指向 <站点目录>/current；部署用户需要有写权限",
			},

			// 健康检查
			{
				Key: "index_check_domain", Label: "首页一致性检查", Section: "health", Type: driver.TypeText,
				Placeholder: "pay.example.com",
				Pattern:     `^[A-Za-z0-9.-]+$`, PatternHint: "只填域名，不带 https:// 和路径",
				Help: "填站点域名后，在服务器上通过本机 Nginx 取首页，和 current/index.html 比对，一致才算切换成功",
			},
		},
	}
}
