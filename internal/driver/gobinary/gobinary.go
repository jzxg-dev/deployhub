// Package gobinary 是「Go 服务」部署方式：本地交叉编译，上传到 <部署目录>/releases/<版本>/，
// 切换 current 软链接，重启 systemd 服务。
package gobinary

import (
	_ "embed"

	"deployhub/internal/driver"
)

// ServiceTemplate 是内置的 systemd 服务模板。
// 占位符：{service} {user} {working_dir} {exec_start} {environment}
//
//go:embed binary.service
var ServiceTemplate string

// Driver 是 Go 服务部署方式。
type Driver struct{}

// Spec 返回参数声明。
func (Driver) Spec() driver.Spec {
	return driver.Spec{
		Name:    "gobinary",
		Title:   "Go 服务",
		Summary: "本地交叉编译 Go 程序，上传到服务器，用 systemd 运行",
		Description: "本地 go build 交叉编译，上传到 <部署目录>/releases/<版本>/，切换 current 软链接后重启 systemd 服务。" +
			"服务器上没有这个服务时按模板安装一次（已有的不覆盖）。回滚只需切回旧版本目录并重启，不用重新编译。",
		Icon:         "server-cog",
		DefaultCheck: "systemctl is-active <服务名>，服务处于运行状态即通过",
		Fields: []driver.Field{
			// 构建
			{
				Key: "main", Label: "编译入口", Section: "build", Type: driver.TypeSelect, Required: true,
				OptionsFrom: "detect:go_mains", AllowCustom: true,
				Placeholder: "./cmd/open-api",
				Help:        "仓库里有 main.go 的目录，可以从识别结果中选择或手填",
			},
			{
				Key: "goos", Label: "目标系统", Section: "build", Type: driver.TypeSelect, Default: "linux",
				Options: []driver.Option{{Value: "linux", Label: "linux"}},
			},
			{
				Key: "goarch", Label: "目标架构", Section: "build", Type: driver.TypeSelect,
				DefaultHint: "按服务器检测结果",
				Options: []driver.Option{
					{Value: "amd64", Label: "amd64（x86_64）"},
					{Value: "arm64", Label: "arm64（aarch64）"},
				},
			},
			{
				Key: "cgo", Label: "启用 CGO", Section: "build", Type: driver.TypeBool, Default: false,
				Help: "交叉编译时一般关闭；开启需要本机有目标平台的 C 工具链",
			},
			{
				Key: "ldflags_x", Label: "版本注入", Section: "build", Type: driver.TypeKeyValue,
				KeyLabel: "变量", ValueLabel: "值",
				Help: "生成 -ldflags \"-X 变量=值\"，值里可用 {version} {commit} {app}，如 go-service-api/global.Version = {version}",
			},
			{
				Key: "build_flags", Label: "额外编译参数", Section: "build", Type: driver.TypeText,
				Placeholder: "-tags prod",
			},
			{
				Key: "binary_name", Label: "产物文件名", Section: "build", Type: driver.TypeText,
				DefaultHint: "<应用>",
				Pattern:     `^[A-Za-z0-9._-]+$`, PatternHint: "只能包含字母、数字、.、_ 和 -",
				Help: "上传到服务器上的可执行文件名",
			},

			// 服务器
			{
				Key: "remote_dir", Label: "部署目录", Section: "server", Type: driver.TypeText,
				DefaultHint: "/opt/<项目>/<应用>",
				Pattern:     `^/\S*$`, PatternHint: "服务器上的绝对路径",
				Help: "下面有 releases/、current、deploy.log",
			},
			{
				Key: "working_dir", Label: "工作目录", Section: "server", Type: driver.TypeText,
				DefaultHint: "<部署目录>/shared",
				Pattern:     `^/\S*$`, PatternHint: "服务器上的绝对路径",
				Help: "进程的 WorkingDirectory，配置文件上传到这里；多个程序可以共用一个目录",
			},

			// 运行
			{
				Key: "service", Label: "systemd 服务名", Section: "runtime", Type: driver.TypeText,
				DefaultHint: "<应用>",
				Pattern:     `^[A-Za-z0-9@._-]+$`, PatternHint: "只能包含字母、数字、@、.、_ 和 -",
			},
			{
				Key: "service_user", Label: "运行用户", Section: "runtime", Type: driver.TypeText,
				DefaultHint: "SSH 登录用户",
			},
			{
				Key: "args", Label: "启动参数", Section: "runtime", Type: driver.TypeText,
				Placeholder: "--config config.toml",
				Help:        "追加在可执行文件后面",
			},
			{
				Key: "env", Label: "环境变量", Section: "runtime", Type: driver.TypeKeyValue,
				KeyLabel: "变量名", ValueLabel: "值",
				Help: "写入服务文件的 Environment=，如 SERVER_PORT = 8889",
			},
			{
				Key: "service_template_mode", Label: "服务文件", Section: "runtime", Type: driver.TypeSelect, Default: "builtin",
				Options: []driver.Option{
					{Value: "builtin", Label: "内置模板", Help: "服务器上没有这个服务时按内置模板安装"},
					{Value: "custom", Label: "自定义模板"},
					{Value: "none", Label: "不自动安装", Help: "服务由你在服务器上手动维护"},
				},
				Help: "只在服务器上没有这个服务时安装，已有的服务不会被覆盖",
			},
			{
				Key: "service_template", Label: "自定义模板", Section: "runtime", Type: driver.TypeCode, Language: "ini",
				Default: ServiceTemplate,
				Help:    "占位符：{service} {user} {working_dir} {exec_start} {environment}",
				ShowIf:  &driver.Condition{Key: "service_template_mode", In: []string{"custom"}},
			},
			{
				Key: "restart", Label: "重启命令", Section: "runtime", Type: driver.TypeText,
				DefaultHint: "sudo systemctl restart <服务名>",
			},

			// 高级
			{
				Key: "build_command", Label: "自定义构建命令", Section: "advanced", Type: driver.TypeTextarea,
				Placeholder: "cargo build --release --target x86_64-unknown-linux-musl",
				Help:        "填了就不用默认的 go build，配合「产物路径」可以部署非 Go 程序；在仓库目录里执行",
			},
			{
				Key: "artifact_path", Label: "产物路径", Section: "advanced", Type: driver.TypeText, Required: true,
				Placeholder: "target/x86_64-unknown-linux-musl/release/app",
				Help:        "自定义构建命令生成的可执行文件，路径相对仓库",
				ShowIf:      &driver.Condition{Key: "build_command", NotEmpty: true},
			},
		},
	}
}
