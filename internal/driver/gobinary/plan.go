package gobinary

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"deployhub/internal/driver"
)

// Config 是换算后的实际配置，部署执行也用它。
type Config struct {
	Main, GOOS, GOARCH, BinaryName, BuildFlags string
	CGO                                        bool
	LDFlagsX                                   []driver.KV
	BuildCommand, ArtifactPath                 string
	RemoteDir, WorkingDir                      string
	Service, ServiceUser, Args, Restart        string
	Env                                        []driver.KV
	TemplateMode, Template                     string
	KeepReleases                               int
}

// Resolve 换算出实际配置。
func (d Driver) Resolve(c *driver.PlanContext) (*Config, *driver.Resolver) {
	r := driver.NewResolver(c, d.Spec().Fields)
	cfg := &Config{
		Main:         c.Str("main"),
		GOOS:         r.Value("goos", "默认", "linux"),
		CGO:          c.Bool("cgo"),
		LDFlagsX:     c.Map("ldflags_x"),
		BuildFlags:   c.Str("build_flags"),
		BuildCommand: c.Str("build_command"),
		ArtifactPath: c.Str("artifact_path"),
		Args:         c.Str("args"),
		Env:          c.Map("env"),
		TemplateMode: c.Str("service_template_mode"),
		KeepReleases: c.Int("keep_releases", 5),
	}
	cfg.GOARCH = r.Value("goarch", "服务器检测", c.Server.Arch)
	if cfg.GOARCH == "" {
		cfg.GOARCH = r.Value("goarch", "默认", "amd64")
		r.Warn("服务器还没检测过架构，按 amd64 编译；在「服务器」页测试连接后会自动按检测结果编译")
	}
	cfg.BinaryName = r.Value("binary_name", "默认", c.App)
	cfg.RemoteDir = r.Value("remote_dir", "默认", fmt.Sprintf("/opt/%s/%s", c.Project, c.App))
	cfg.WorkingDir = r.Value("working_dir", "默认", cfg.RemoteDir+"/shared")
	cfg.Service = r.Value("service", "默认", c.App)
	cfg.ServiceUser = r.Value("service_user", "SSH 登录用户", c.Server.User, "默认", "root")
	cfg.Restart = r.Value("restart", "默认", "sudo systemctl restart "+cfg.Service)
	switch cfg.TemplateMode {
	case "custom":
		cfg.Template = c.Str("service_template")
	case "none":
	default:
		cfg.Template = ServiceTemplate
	}
	return cfg, r
}

// RenderService 渲染 systemd 服务文件。
func (cfg *Config) RenderService() string {
	var env []string
	for _, kv := range cfg.Env {
		env = append(env, fmt.Sprintf("Environment=%s=%s", kv.Key, kv.Value))
	}
	exec := cfg.RemoteDir + "/current/" + cfg.BinaryName
	if cfg.Args != "" {
		exec += " " + cfg.Args
	}
	tpl := cfg.Template
	if len(env) == 0 {
		// 没有环境变量时去掉整行占位符
		var lines []string
		for _, l := range strings.Split(tpl, "\n") {
			if strings.TrimSpace(l) != "{environment}" {
				lines = append(lines, l)
			}
		}
		tpl = strings.Join(lines, "\n")
	}
	return strings.NewReplacer(
		"{service}", cfg.Service,
		"{user}", cfg.ServiceUser,
		"{working_dir}", cfg.WorkingDir,
		"{exec_start}", exec,
		"{environment}", strings.Join(env, "\n"),
	).Replace(tpl)
}

// BuildCommandLine 返回本地编译命令（在仓库目录执行），out 是产物路径。
func (cfg *Config) BuildCommandLine(out string) string {
	if cfg.BuildCommand != "" {
		return cfg.BuildCommand
	}
	ld := []string{"-s", "-w"}
	for _, kv := range cfg.LDFlagsX {
		ld = append(ld, fmt.Sprintf("-X %s=%s", kv.Key, kv.Value))
	}
	cgo := "0"
	if cfg.CGO {
		cgo = "1"
	}
	parts := []string{
		fmt.Sprintf("CGO_ENABLED=%s GOOS=%s GOARCH=%s go build -trimpath", cgo, cfg.GOOS, cfg.GOARCH),
		"-ldflags=" + driver.Q(strings.Join(ld, " ")),
	}
	if cfg.BuildFlags != "" {
		parts = append(parts, cfg.BuildFlags)
	}
	parts = append(parts, "-o", driver.Q(out), driver.Q(cfg.Main))
	return strings.Join(parts, " ")
}

// Plan 生成部署计划。
func (d Driver) Plan(c *driver.PlanContext) *driver.Plan {
	cfg, r := d.Resolve(c)
	rel := cfg.RemoteDir + "/releases/" + c.Version
	local := "<构建目录>/" + cfg.BinaryName
	q := driver.Q

	build := []driver.Command{{Where: "local", Command: "cd " + q(c.Repo.Path) + " && " + cfg.BuildCommandLine(local),
		Note: "{version} {commit} {app} 和 ${名称} 在执行时替换"}}
	if cfg.BuildCommand != "" {
		build = append(build, driver.Command{Where: "local", Command: fmt.Sprintf("cp %s %s", q(filepath.Join(c.Repo.Path, cfg.ArtifactPath)), q(local))})
	}
	build = append(build, driver.Command{Where: "local", Command: "sha256sum " + cfg.BinaryName + " > SHA256SUMS"})

	prepare := []driver.Command{{Where: "server", Command: fmt.Sprintf("mkdir -p %s %s", q(cfg.RemoteDir+"/releases"), q(cfg.WorkingDir)),
		Note: "没有权限时用免密 sudo 创建并交给部署用户"}}
	prepare = append(prepare, driver.ConfigFileCommands(c, cfg.WorkingDir)...)
	if cfg.Template != "" {
		prepare = append(prepare, driver.Command{Where: "server",
			Command: fmt.Sprintf("systemctl cat %s >/dev/null 2>&1 || { <服务文件> | sudo -n tee /etc/systemd/system/%s.service && sudo -n systemctl daemon-reload && sudo -n systemctl enable %s; }", q(cfg.Service), cfg.Service, q(cfg.Service)),
			Note:    "服务不存在时才安装，已有的不覆盖"})
	}

	p := &driver.Plan{Steps: []driver.Step{
		{Name: "构建", Commands: build},
		{Name: "准备服务器", Commands: prepare},
		{Name: "上传", Commands: []driver.Command{
			{Where: "local", Command: fmt.Sprintf("rsync -az --partial <构建目录>/ %s:%s/", c.Server.Dest, rel), Note: "服务器没有 rsync 时改用 scp"},
			{Where: "server", Command: fmt.Sprintf("cd %s && sha256sum -c SHA256SUMS && chmod +x %s", q(rel), q(cfg.BinaryName))},
		}},
		{Name: "切换", Commands: []driver.Command{
			{Where: "server", Command: fmt.Sprintf("cd %s && ln -sfn releases/%s current.tmp && mv -T current.tmp current", q(cfg.RemoteDir), c.Version), Note: "原子切换 current"},
			{Where: "server", Command: cfg.Restart},
		}},
		{Name: "健康检查", Commands: []driver.Command{
			{Where: "server", Command: driver.HealthCommand(c, "systemctl is-active --quiet "+q(cfg.Service)), Note: "失败时切回上一个版本并重启"},
		}},
		{Name: "清理", Commands: []driver.Command{
			{Where: "server", Command: fmt.Sprintf("保留最近 %d 个版本，删除 %s/releases 下更早的（当前版本永远不删）", cfg.KeepReleases, cfg.RemoteDir)},
		}},
	}}
	if cfg.Template != "" {
		p.Steps = append(p.Steps, driver.Step{Name: "服务文件", Commands: []driver.Command{
			{Where: "server", Command: cfg.RenderService(), Note: "/etc/systemd/system/" + cfg.Service + ".service 的内容（服务不存在时安装）"},
		}})
	}
	return r.Finish(p)
}

// Checks 返回部署前检查项。
func (d Driver) Checks(c *driver.PlanContext) []driver.Check {
	cfg, _ := d.Resolve(c)
	q := driver.Q
	var checks []driver.Check
	if cfg.BuildCommand == "" {
		checks = append(checks,
			driver.Check{Group: "本机", Name: "go 命令", Local: func() (bool, string) {
				if p, err := exec.LookPath("go"); err == nil {
					return true, p
				}
				return false, "本机找不到 go 命令"
			}},
			driver.Check{Group: "本机", Name: "编译入口 " + cfg.Main, Local: func() (bool, string) {
				dir := filepath.Join(c.Repo.Path, cfg.Main)
				if st, err := os.Stat(dir); err == nil && st.IsDir() {
					return true, dir + " 存在"
				}
				return false, dir + " 不存在"
			}},
		)
	}
	checks = append(checks,
		driver.Check{Group: "服务器", Name: "systemd", Remote: "command -v systemctl", OK: "有 systemctl", Fail: "服务器上没有 systemctl，Go 服务需要 systemd 运行"},
		driver.Check{Group: "服务器", Name: "部署目录", Remote: driver.WritableCheck(cfg.RemoteDir), OK: cfg.RemoteDir + " 可写（或可以创建）",
			Fail: cfg.RemoteDir + " 不可写，也没有免密 sudo：请在服务器上创建并交给部署用户"},
		driver.Check{Group: "服务器", Name: "systemd 服务 " + cfg.Service, Remote: "systemctl cat " + q(cfg.Service) + " >/dev/null 2>&1", Warn: true,
			OK: "服务已存在", Fail: serviceMissingHint(cfg)},
	)
	if strings.HasPrefix(cfg.Restart, "sudo ") {
		checks = append(checks, driver.Check{Group: "服务器", Name: "重启权限", Remote: "sudo -n true", Warn: true,
			OK: "sudo 免密可用", Fail: "sudo 需要密码：重启命令「" + cfg.Restart + "」会失败，需要给部署用户配置免密 sudo（至少放行 systemctl restart " + cfg.Service + "）"})
	}
	return append(checks, driver.ConfigFileChecks(c, cfg.WorkingDir)...)
}

func serviceMissingHint(cfg *Config) string {
	if cfg.Template == "" {
		return "服务不存在，且设置为不自动安装：请先在服务器上手动安装"
	}
	return "服务不存在，首次部署时按模板安装（需要免密 sudo）"
}
