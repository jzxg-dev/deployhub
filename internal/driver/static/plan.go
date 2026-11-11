package static

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"deployhub/internal/driver"
)

// Config 是换算后的实际配置。
type Config struct {
	BuildCommand, Dist, RemoteDir, IndexCheckDomain string
	BuildEnv                                        []driver.KV
	Exclude                                         []string
	KeepReleases                                    int
}

// Resolve 换算出实际配置。
func (d Driver) Resolve(c *driver.PlanContext) (*Config, *driver.Resolver) {
	r := driver.NewResolver(c, d.Spec().Fields)
	cfg := &Config{
		BuildEnv:         c.Map("build_env"),
		Exclude:          c.List("exclude"),
		IndexCheckDomain: c.Str("index_check_domain"),
		KeepReleases:     c.Int("keep_releases", 5),
	}
	cfg.BuildCommand = r.Value("build_command", "识别", c.Repo.Hints["build_command"])
	if cfg.BuildCommand == "" {
		r.Warn("没识别出构建命令（仓库里没有带 build 脚本的 package.json），请填写构建命令")
	}
	cfg.Dist = r.Value("dist", "识别", c.Repo.Hints["dist"], "默认", "dist")
	cfg.RemoteDir = r.Value("remote_dir", "默认", "/srv/www/"+c.App)
	return cfg, r
}

// DefaultCheck 返回默认健康检查命令。
func (cfg *Config) DefaultCheck() string {
	if cfg.IndexCheckDomain != "" {
		return fmt.Sprintf("curl -fsS --max-time 5 --resolve %s:443:127.0.0.1 https://%s/index.html | cmp -s - %s",
			cfg.IndexCheckDomain, cfg.IndexCheckDomain, driver.Q(cfg.RemoteDir+"/current/index.html"))
	}
	return "test -f " + driver.Q(cfg.RemoteDir+"/current/index.html")
}

// Plan 生成部署计划。
func (d Driver) Plan(c *driver.PlanContext) *driver.Plan {
	cfg, r := d.Resolve(c)
	q := driver.Q
	rel := cfg.RemoteDir + "/releases/" + c.Version

	env := []string{"CI=true"}
	for _, kv := range cfg.BuildEnv {
		if kv.Value != "" {
			env = append(env, kv.Key+"="+q(kv.Value))
		}
	}
	excl := "无"
	if len(cfg.Exclude) > 0 {
		excl = strings.Join(cfg.Exclude, "、")
	}

	build := []driver.Command{
		{Where: "local", Command: fmt.Sprintf("rsync -a --delete --exclude=/.git --exclude=/node_modules --exclude=/%s %s/ <构建副本>/", cfg.Dist, q(c.Repo.Path)),
			Note: "复制工作区（含未提交改动），不动仓库自己的 node_modules"},
	}
	if _, err := os.Stat(filepath.Join(c.Repo.Path, "mise.toml")); err == nil {
		build = append(build, driver.Command{Where: "local", Command: "mise trust --quiet <构建副本>/mise.toml"})
	}
	build = append(build,
		driver.Command{Where: "local", Command: "cd <构建副本> && " + strings.Join(env, " ") + " " + cfg.BuildCommand, Note: "${名称} 在执行时替换"},
		driver.Command{Where: "local", Command: fmt.Sprintf("复制 <构建副本>/%s 到产物目录，跳过 %s；产物里必须有 index.html", cfg.Dist, excl)},
	)

	prepare := []driver.Command{{Where: "server", Command: fmt.Sprintf("mkdir -p %s && test -w %s", q(cfg.RemoteDir+"/releases"), q(cfg.RemoteDir)),
		Note: "没有权限时用免密 sudo 创建并交给部署用户"}}
	prepare = append(prepare, driver.ConfigFileCommands(c, cfg.RemoteDir)...)

	p := &driver.Plan{Steps: []driver.Step{
		{Name: "构建", Commands: build},
		{Name: "准备服务器", Commands: prepare},
		{Name: "上传", Commands: []driver.Command{
			{Where: "server", Command: fmt.Sprintf("mkdir -p %s && cp -al %s/. %s/", q(rel), q(cfg.RemoteDir+"/current"), q(rel)), Note: "以当前版本为底（硬链接，不占空间）"},
			{Where: "local", Command: fmt.Sprintf("rsync -az --partial --checksum --delete <产物目录>/ %s:%s/", c.Server.Dest, rel), Note: "只传改动的文件；没有 rsync 时改用 scp"},
		}},
		{Name: "切换", Commands: []driver.Command{
			{Where: "server", Command: fmt.Sprintf("cd %s && test -f releases/%s/index.html && ln -sfn releases/%s current.tmp && mv -T current.tmp current", q(cfg.RemoteDir), c.Version, c.Version),
				Note: "原子切换 current，Nginx 下一个请求就读新版本，不用重启"},
		}},
		{Name: "健康检查", Commands: []driver.Command{{Where: "server", Command: driver.HealthCommand(c, cfg.DefaultCheck()), Note: "失败时切回上一个版本"}}},
		{Name: "清理", Commands: []driver.Command{
			{Where: "server", Command: fmt.Sprintf("保留最近 %d 个版本，删除 %s/releases 下更早的（当前版本永远不删）", cfg.KeepReleases, cfg.RemoteDir)},
		}},
	}}
	return r.Finish(p)
}

// Checks 返回部署前检查项。
func (d Driver) Checks(c *driver.PlanContext) []driver.Check {
	cfg, _ := d.Resolve(c)
	tool := firstTool(cfg.BuildCommand)
	checks := []driver.Check{
		{Group: "本机", Name: "构建命令", Local: func() (bool, string) {
			if cfg.BuildCommand == "" {
				return false, "没有构建命令：仓库里没识别出来，请填写"
			}
			if tool == "" {
				return true, cfg.BuildCommand
			}
			if p, err := exec.LookPath(tool); err == nil {
				return true, tool + "：" + p
			}
			return false, "本机找不到 " + tool
		}},
		{Group: "本机", Name: "rsync", Local: func() (bool, string) {
			if _, err := exec.LookPath("rsync"); err == nil {
				return true, "构建副本用 rsync 复制"
			}
			return false, "本机找不到 rsync"
		}},
		{Group: "服务器", Name: "站点目录", Remote: driver.WritableCheck(cfg.RemoteDir), OK: cfg.RemoteDir + " 可写（或可以创建）",
			Fail: cfg.RemoteDir + " 不可写，也没有免密 sudo：请执行 sudo mkdir -p " + cfg.RemoteDir + " && sudo chown <部署用户> " + cfg.RemoteDir},
	}
	return append(checks, driver.ConfigFileChecks(c, cfg.RemoteDir)...)
}

// firstTool 取构建命令的第一个程序名，跳过环境变量赋值。
func firstTool(cmd string) string {
	for _, w := range strings.Fields(cmd) {
		if strings.Contains(w, "=") {
			continue
		}
		return w
	}
	return ""
}
