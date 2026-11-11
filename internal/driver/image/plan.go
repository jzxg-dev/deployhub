package image

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"deployhub/internal/driver"
)

// ComposeFiles 是 compose 认的文件名，按优先级排列。
var ComposeFiles = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}

// Config 是换算后的实际配置。
type Config struct {
	Dockerfile, Context, Platform, Image string
	BuildArgs                            []driver.KV
	Transfer, Registry                   string
	ComposeDir, TagVar, ComposeMode      string
	Services                             []string
	ComposeContent                       string
	KeepImages                           int
}

// Resolve 换算出实际配置。
func (d Driver) Resolve(c *driver.PlanContext) (*Config, *driver.Resolver) {
	r := driver.NewResolver(c, d.Spec().Fields)
	cfg := &Config{
		BuildArgs:   c.Map("build_args"),
		Transfer:    c.Str("transfer"),
		Registry:    strings.TrimRight(c.Str("registry"), "/"),
		ComposeMode: c.Str("compose_mode"),
		KeepImages:  c.Int("keep_releases", 5),
		Services:    c.List("services"),
	}
	cfg.Dockerfile = r.Value("dockerfile", "识别", c.Repo.Hints["dockerfile"], "默认", "Dockerfile")
	cfg.Context = r.Value("context", "默认", ".")
	arch := c.Server.Arch
	if arch != "" {
		arch = "linux/" + arch
	}
	cfg.Platform = r.Value("platform", "服务器检测", arch)
	if cfg.Platform == "" {
		cfg.Platform = r.Value("platform", "默认", "linux/amd64")
		r.Warn("服务器还没检测过架构，按 linux/amd64 构建；在「服务器」页测试连接后会自动按检测结果构建")
	}
	cfg.Image = r.Value("image", "默认", c.App)
	cfg.ComposeDir = r.Value("compose_dir", "默认", fmt.Sprintf("/opt/%s/%s", c.Project, c.App))
	if len(cfg.Services) == 0 {
		cfg.Services = []string{c.App}
		r.Value("services", "默认", c.App)
	}
	cfg.TagVar = r.Value("tag_var", "默认", tagVar(cfg.Image))
	switch cfg.ComposeMode {
	case "custom":
		cfg.ComposeContent = c.Str("compose_file")
	case "server":
	default:
		cfg.ComposeContent = ComposeTemplate
		if len(cfg.Services) > 1 {
			r.Warn("内置 compose 模板只适合单个服务，有多个服务时请选择「自定义内容」或「以服务器上已有的为准」")
		}
	}
	return cfg, r
}

// tagVar 由镜像名生成标签变量名：取最后一段，非字母数字换成下划线，大写，加 _TAG。
func tagVar(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 {
		image = image[i+1:]
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(image) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String() + "_TAG"
}

// RenderCompose 渲染 compose 文件内容。
func (cfg *Config) RenderCompose() string {
	return strings.NewReplacer("{service}", cfg.Services[0], "{image}", cfg.imageName(), "{tag_var}", cfg.TagVar).Replace(cfg.ComposeContent)
}

// imageName 是服务器上使用的镜像名：用镜像仓库时带仓库前缀。
func (cfg *Config) imageName() string {
	if cfg.Transfer == "registry" && cfg.Registry != "" {
		return cfg.Registry + "/" + cfg.Image
	}
	return cfg.Image
}

// Plan 生成部署计划。
func (d Driver) Plan(c *driver.PlanContext) *driver.Plan {
	cfg, r := d.Resolve(c)
	q := driver.Q
	ref := cfg.imageName() + ":" + c.Version

	args := []string{"docker buildx build", "--platform " + cfg.Platform, "-f " + q(cfg.Dockerfile), "-t " + q(ref)}
	for _, kv := range cfg.BuildArgs {
		args = append(args, "--build-arg "+q(kv.Key+"="+kv.Value))
	}
	if cfg.Transfer == "registry" {
		args = append(args, "--push")
	} else {
		args = append(args, "--load")
	}
	args = append(args, q(cfg.Context))

	var transfer []driver.Command
	if cfg.Transfer == "registry" {
		transfer = []driver.Command{{Where: "server", Command: "docker pull " + q(ref)}}
	} else {
		transfer = []driver.Command{{Where: "local", Command: fmt.Sprintf("docker save %s | gzip | ssh %s 'gunzip | docker load'", q(ref), c.Server.Dest), Note: "不需要镜像仓库"}}
	}

	dir := cfg.ComposeDir
	prepare := []driver.Command{
		{Where: "server", Command: "docker info >/dev/null && docker compose version >/dev/null", Note: "Docker 和 compose 插件可用"},
		{Where: "server", Command: "mkdir -p " + q(dir), Note: "没有权限时用免密 sudo 创建并交给部署用户"},
	}
	if cfg.ComposeContent != "" {
		prepare = append(prepare, driver.Command{Where: "server", Command: fmt.Sprintf("cd %s && [ -e docker-compose.yml ] || [ -e compose.yml ] || <写入 compose 文件>", q(dir)), Note: "没有 compose 文件时才安装，已有的不覆盖"})
	}
	prepare = append(prepare, driver.ConfigFileCommands(c, dir)...)
	prepare = append(prepare, driver.Command{Where: "server", Command: fmt.Sprintf("cd %s && docker compose config --services", q(dir)), Note: "必须包含 " + strings.Join(cfg.Services, "、")})

	services := make([]string, len(cfg.Services))
	for i, s := range cfg.Services {
		services[i] = q(s)
	}
	p := &driver.Plan{Steps: []driver.Step{
		{Name: "准备服务器", Commands: prepare},
		{Name: "构建", Commands: []driver.Command{{Where: "local", Command: "cd " + q(c.Repo.Path) + " && " + strings.Join(args, " "), Note: "${名称} 在执行时替换"}}},
		{Name: "传输", Commands: transfer},
		{Name: "切换", Commands: []driver.Command{
			{Where: "server", Command: fmt.Sprintf("cd %s && touch .env && sed -i '/^%s=/d' .env && echo %s=%s >> .env", q(dir), cfg.TagVar, cfg.TagVar, c.Version), Note: "只改 .env 里的镜像标签变量"},
			{Where: "server", Command: fmt.Sprintf("cd %s && docker compose up -d %s", q(dir), strings.Join(services, " "))},
		}},
		{Name: "健康检查", Commands: []driver.Command{{Where: "server", Command: driver.HealthCommand(c,
			fmt.Sprintf("cd %s && test \"$(docker compose ps --status running -q %s | wc -l)\" -ge %d", q(dir), strings.Join(services, " "), len(services))),
			Note: "失败时把标签改回上一个版本并重启"}}},
		{Name: "清理", Commands: []driver.Command{{Where: "server", Command: fmt.Sprintf("保留最近 %d 个 %s 镜像标签，删除更早的（当前版本永远不删）", cfg.KeepImages, cfg.imageName())}}},
	}}
	if cfg.ComposeContent != "" {
		p.Steps = append(p.Steps, driver.Step{Name: "compose 文件", Commands: []driver.Command{
			{Where: "server", Command: cfg.RenderCompose(), Note: dir + "/docker-compose.yml 的内容（没有 compose 文件时安装）"},
		}})
	}
	return r.Finish(p)
}

// Checks 返回部署前检查项。
func (d Driver) Checks(c *driver.PlanContext) []driver.Check {
	cfg, _ := d.Resolve(c)
	q := driver.Q
	dir := cfg.ComposeDir
	anyCompose := make([]string, len(ComposeFiles))
	for i, f := range ComposeFiles {
		anyCompose[i] = "[ -e " + q(dir+"/"+f) + " ]"
	}
	checks := []driver.Check{
		{Group: "本机", Name: "Dockerfile", Local: func() (bool, string) {
			p := filepath.Join(c.Repo.Path, cfg.Dockerfile)
			if _, err := os.Stat(p); err == nil {
				return true, p + " 存在"
			}
			return false, p + " 不存在"
		}},
		{Group: "本机", Name: "docker buildx", Local: func() (bool, string) {
			if _, err := exec.LookPath("docker"); err != nil {
				return false, "本机找不到 docker 命令"
			}
			if err := exec.Command("docker", "buildx", "version").Run(); err != nil {
				return false, "docker buildx 不可用"
			}
			return true, "可用"
		}},
		{Group: "服务器", Name: "Docker", Remote: "docker info >/dev/null 2>&1 && docker compose version >/dev/null 2>&1", OK: "docker 和 compose 插件可用",
			Fail: "docker 或 docker compose 不可用：需要安装 Docker（含 compose 插件），并把部署用户加入 docker 组"},
		{Group: "服务器", Name: "compose 目录", Remote: driver.WritableCheck(dir), OK: dir + " 可写（或可以创建）",
			Fail: dir + " 不可写，也没有免密 sudo：请在服务器上创建并交给部署用户"},
	}
	switch {
	case cfg.ComposeMode == "server":
		checks = append(checks, driver.Check{Group: "服务器", Name: "compose 文件", Remote: strings.Join(anyCompose, " || "),
			OK: "compose 文件存在", Fail: dir + " 下没有 compose 文件（设置为以服务器上已有的为准）"})
	default:
		checks = append(checks, driver.Check{Group: "服务器", Name: "compose 文件", Remote: strings.Join(anyCompose, " || "), Warn: true,
			OK: "compose 文件已存在，部署时不会覆盖", Fail: "compose 文件不存在，首次部署时按模板安装"})
	}
	for _, svc := range cfg.Services {
		checks = append(checks, driver.Check{Group: "服务器", Name: "compose 服务 " + svc, Warn: cfg.ComposeMode != "server",
			Remote: fmt.Sprintf("cd %s && docker compose config --services 2>/dev/null | grep -qx %s", q(dir), q(svc)),
			OK:     "compose 文件里有服务 " + svc, Fail: "compose 文件里没有服务 " + svc + "（或 compose 文件还不存在）"})
	}
	return append(checks, driver.ConfigFileChecks(c, dir)...)
}
