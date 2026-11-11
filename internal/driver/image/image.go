// Package image 是「Docker 镜像」部署方式：本地 docker buildx build，传到服务器后修改
// compose 目录下 .env 里的镜像标签变量，执行 docker compose up -d <服务名>。
package image

import (
	_ "embed"

	"deployhub/internal/driver"
)

// ComposeTemplate 是内置的 compose 文件模板。
// 占位符：{service} {image} {tag_var}
//
//go:embed compose.yml
var ComposeTemplate string

// Driver 是 Docker 镜像部署方式。
type Driver struct{}

// Spec 返回参数声明。
func (Driver) Spec() driver.Spec {
	return driver.Spec{
		Name:    "image",
		Title:   "Docker 镜像",
		Summary: "本地构建镜像，传到服务器，用 docker compose 运行",
		Description: "本地 docker buildx build，通过 SSH 直传或镜像仓库送到服务器，修改 compose 目录下 .env 里的镜像标签变量，" +
			"再执行 docker compose up -d <服务名>。compose 文件只在不存在时按模板安装一次，之后部署只改标签变量。",
		Icon:         "container",
		DefaultCheck: "容器处于 running 状态即通过",
		Fields: []driver.Field{
			// 构建
			{
				Key: "dockerfile", Label: "Dockerfile", Section: "build", Type: driver.TypeText,
				DefaultHint: "自动识别，否则 Dockerfile",
				Help:        "路径相对仓库",
			},
			{
				Key: "context", Label: "构建上下文", Section: "build", Type: driver.TypeText, Default: ".",
				Help: "路径相对仓库",
			},
			{
				Key: "platform", Label: "目标平台", Section: "build", Type: driver.TypeSelect,
				DefaultHint: "按服务器检测结果",
				Options: []driver.Option{
					{Value: "linux/amd64", Label: "linux/amd64"},
					{Value: "linux/arm64", Label: "linux/arm64"},
				},
			},
			{
				Key: "build_args", Label: "构建参数", Section: "build", Type: driver.TypeKeyValue,
				KeyLabel: "参数名", ValueLabel: "值",
				Help: "生成 --build-arg，如 VITE_API_BASE = https://api.example.com/api",
			},
			{
				Key: "image", Label: "镜像名", Section: "build", Type: driver.TypeText,
				DefaultHint: "<应用>",
				Pattern:     `^[a-z0-9][a-z0-9._/-]*$`, PatternHint: "小写字母、数字、.、_、- 和 /",
			},

			// 传输
			{
				Key: "transfer", Label: "传输方式", Section: "transfer", Type: driver.TypeSelect, Default: "ssh",
				Options: []driver.Option{
					{Value: "ssh", Label: "SSH 直传", Help: "docker save | gzip | ssh docker load，不需要镜像仓库"},
					{Value: "registry", Label: "镜像仓库", Help: "docker push 后服务器 docker pull"},
				},
			},
			{
				Key: "registry", Label: "镜像仓库地址", Section: "transfer", Type: driver.TypeText, Required: true,
				Placeholder: "registry.example.com/team",
				Help:        "本机和服务器需要先 docker login；第二期在「凭据」里统一管理登录信息",
				ShowIf:      &driver.Condition{Key: "transfer", In: []string{"registry"}},
			},

			// 服务器
			{
				Key: "compose_dir", Label: "compose 目录", Section: "server", Type: driver.TypeText,
				DefaultHint: "/opt/<项目>/<应用>",
				Pattern:     `^/\S*$`, PatternHint: "服务器上的绝对路径",
				Help: "compose 文件、.env、上传的配置文件都放这里，不存在时自动创建",
			},
			{
				Key: "services", Label: "compose 服务名", Section: "server", Type: driver.TypeList,
				DefaultHint: "<应用>",
				Help:        "部署时只重启这些服务；compose 文件里缺少时在构建前停止",
			},
			{
				Key: "tag_var", Label: "镜像标签变量", Section: "server", Type: driver.TypeText,
				DefaultHint: "<镜像名大写>_TAG",
				Pattern:     `^[A-Z][A-Z0-9_]*$`, PatternHint: "大写字母、数字和下划线",
				Help: "compose 文件里写 image: <镜像名>:${变量名}，部署时只改 .env 里的这个变量",
			},
			{
				Key: "compose_mode", Label: "compose 文件", Section: "server", Type: driver.TypeSelect, Default: "builtin",
				Options: []driver.Option{
					{Value: "builtin", Label: "内置模板", Help: "按镜像名、服务名生成，只适合单个服务"},
					{Value: "custom", Label: "自定义内容"},
					{Value: "server", Label: "以服务器上已有的为准", Help: "不安装，只检查存在"},
				},
				Help: "只在 compose 目录下没有 compose 文件时安装，已有的不会被覆盖",
			},
			{
				Key: "compose_file", Label: "自定义内容", Section: "server", Type: driver.TypeCode, Language: "yaml",
				Default: ComposeTemplate,
				Help:    "占位符：{service} {image} {tag_var}",
				ShowIf:  &driver.Condition{Key: "compose_mode", In: []string{"custom"}},
			},
		},
	}
}
