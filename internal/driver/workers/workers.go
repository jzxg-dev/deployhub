// Package workers 是 Cloudflare Workers 部署方式：在仓库副本里构建，用 wrangler deploy 发布，
// 线上版本和回滚直接调 Cloudflare 接口（把旧版本设为 100% 流量），不需要服务器。
package workers

import "deployhub/internal/driver"

// Driver 是 Cloudflare Workers 部署方式。
type Driver struct{}

// Spec 声明参数。
func (Driver) Spec() driver.Spec {
	return driver.Spec{
		Name:    "workers",
		Title:   "Cloudflare Workers",
		Summary: "用 wrangler 发布到 Cloudflare Workers，回滚使用 Workers 自带的版本，不需要服务器",
		Description: "在仓库工作区的副本里安装依赖、构建，用 wrangler deploy 发布新版本（版本标签和说明里带上部署平台的版本号），" +
			"路由、自定义域名按 wrangler 配置更新。回滚不重新构建，直接通过 Cloudflare 接口把旧版本设为 100% 流量。",
		Icon:         "cloud",
		DefaultCheck: "确认 Cloudflare 上 100% 流量的是本次部署的版本",
		Omit:         []string{"server", "keep_releases", "config_files", "required_files", "required_config"},
		Steps: []driver.StepLabel{
			{Key: driver.StepPrepare, Label: "检查账号"},
			{Key: driver.StepBuild, Label: "构建"},
			{Key: driver.StepActivate, Label: "发布"},
			{Key: driver.StepHealth, Label: "健康检查"},
		},
		Fields: []driver.Field{
			{
				Key: "account", Label: "Cloudflare 账号", Section: "account", Type: driver.TypeSelect, Required: true,
				OptionsFrom: "credentials:cloudflare",
				Help:        "在「凭据」里添加账号 ID 和 API Token",
			},
			{
				Key: "worker_name", Label: "Worker 名称", Section: "account", Type: driver.TypeText,
				DefaultHint: "wrangler 配置文件里的 name",
				Pattern:     `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`,
				PatternHint: "小写字母、数字和 -，最多 63 个字符",
				Help:        "用来读取线上版本和回滚；填写时会用 --name 传给 wrangler。配置文件要构建后才生成时必须填写",
			},
			{
				Key: "config", Label: "wrangler 配置文件", Section: "build", Type: driver.TypeText,
				DefaultHint: "自动识别 wrangler.json / wrangler.jsonc / wrangler.toml",
				Placeholder: "wrangler.jsonc",
				Pattern:     `^[^/\s][^\s]*$`, PatternHint: "相对仓库目录的路径，不能以 / 开头",
				Help: "相对仓库目录；也可以是构建后才生成的文件，如 dist/server/wrangler.json",
			},
			{
				Key: "build_command", Label: "构建命令", Section: "build", Type: driver.TypeText,
				DefaultHint: "按锁文件安装依赖；配置里没有 build.command 时再执行 build 脚本",
				Placeholder: "pnpm install --frozen-lockfile && node --test worker.test.mjs",
				Help:        "在仓库副本里执行（不动仓库自己的 node_modules），可以加上测试。wrangler 配置里的 build.command 由 wrangler 发布前执行，不用写在这里",
			},
			{
				Key: "build_env", Label: "构建环境变量", Section: "build", Type: driver.TypeKeyValue,
				KeyLabel: "变量名", ValueLabel: "值",
				Help: "构建命令和 wrangler（包括配置里的 build.command）都能读到；值里可以用 {version}、{commit}",
			},
			{
				Key: "wrangler_version", Label: "wrangler 版本", Section: "build", Type: driver.TypeText,
				DefaultHint: "项目依赖里安装的 wrangler",
				Placeholder: "4.133.0",
				Pattern:     `^\d+\.\d+\.\d+$`, PatternHint: "写完整的版本号，如 4.133.0",
				Help: "项目没有把 wrangler 装成依赖时必须填写，用 npx wrangler@<版本> 执行",
			},
			{
				Key: "deploy_args", Label: "wrangler deploy 额外参数", Section: "advanced", Type: driver.TypeList,
				Placeholder: "--keep-vars",
				Help:        "追加在 wrangler deploy 后面，如 --keep-vars（保留在控制台设置的变量）、--env production",
			},
		},
	}
}
