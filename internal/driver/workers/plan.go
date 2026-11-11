package workers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"deployhub/internal/cloudflare"
	"deployhub/internal/detect"
	"deployhub/internal/driver"
)

// Config 是换算后的实际配置。
type Config struct {
	AccountID, Token string
	CredName         string // 选择的凭据
	WorkerName       string // 为空表示还不知道（配置文件要构建后才有，又没填名称）
	NameFlag         bool   // 名称是填写的，发布时用 --name 传给 wrangler
	ConfigFile       string // 相对仓库目录
	BuildCommand     string
	BuildEnv         []driver.KV
	WranglerVersion  string
	DeployArgs       []string
}

// Resolve 换算出实际配置：没填的 Worker 名称、配置文件、构建命令从仓库里读。
func (d Driver) Resolve(c *driver.PlanContext) (*Config, *driver.Resolver) {
	r := driver.NewResolver(c, d.Spec().Fields)
	cfg := &Config{
		CredName:        c.Str("account"),
		AccountID:       c.Cred("account", "account_id"),
		Token:           c.Cred("account", "api_token"),
		BuildEnv:        c.Map("build_env"),
		WranglerVersion: c.Str("wrangler_version"),
		DeployArgs:      c.List("deploy_args"),
	}
	repo := c.Repo.Path
	file := detect.FindWrangler(repo)
	cfg.ConfigFile = r.Value("config", "识别", file)
	if cfg.ConfigFile == "" {
		cfg.ConfigFile = "wrangler.jsonc"
		r.Warn("仓库目录里没有 wrangler 配置文件（wrangler.json / wrangler.jsonc / wrangler.toml）；构建后才生成的话，请填写配置文件和 Worker 名称")
	}
	var w *detect.Wrangler
	if got, err := detect.ReadWrangler(filepath.Join(repo, cfg.ConfigFile)); err == nil {
		w = got
	} else if !os.IsNotExist(err) {
		r.Warn("读取 %s：%v", cfg.ConfigFile, err)
	}
	if n := c.Str("worker_name"); n != "" {
		cfg.WorkerName, cfg.NameFlag = n, true
	} else if w != nil && w.Name != "" {
		cfg.WorkerName = r.Value("worker_name", "识别", w.Name)
	} else {
		r.Warn("不知道 Worker 名称：配置文件里没有 name（或要构建后才生成），请填写 Worker 名称")
	}
	cfg.BuildCommand = r.Value("build_command", "识别", detect.WorkersBuildCommand(repo, w))
	if cfg.WranglerVersion == "" {
		r.Value("wrangler_version", "默认", "项目依赖里的 wrangler")
	}
	for _, a := range cfg.DeployArgs {
		if !strings.HasPrefix(a, "-") {
			r.Warn("wrangler deploy 额外参数 %q 不是以 - 开头的选项", a)
		}
	}
	return cfg, r
}

var versionRe = regexp.MustCompile(`^\S+-([0-9a-f]{7,40}-\d{8}-\d{4}(?:-\d+)?)$`)

// Tag 是写进 Cloudflare 版本标签的短版本号：去掉分支，如 a1b2c3d-20261102-1530。接口限制标签最多 25 个字符。
func Tag(version string) string {
	if m := versionRe.FindStringSubmatch(version); m != nil && len(m[1]) <= cloudflare.MaxTag {
		return m[1]
	}
	return cloudflare.Truncate(version, cloudflare.MaxTag)
}

// Message 是版本说明：完整版本号在最前面（读取线上版本时靠它认出来），后面是提交说明，最多 100 个字符。
func Message(version, subject string) string {
	if subject == "" {
		return cloudflare.Truncate(version, cloudflare.MaxMessage)
	}
	return cloudflare.Truncate(version+" "+subject, cloudflare.MaxMessage)
}

// Label 是 Cloudflare 上一个版本在页面上的名字：部署平台发布的用完整版本号，其他的（控制台、旧工具发布）用标签或版本 ID 前 8 位。
func Label(v *cloudflare.Version) string {
	if f := strings.Fields(v.Message()); len(f) > 0 && versionRe.MatchString(f[0]) {
		return f[0]
	}
	if t := v.Tag(); t != "" {
		return t
	}
	return ShortID(v.ID)
}

// ShortID 是版本 ID 的前 8 位。
func ShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Matches 判断版本是不是 name 指的那个：页面上的名字、完整 ID 或至少 8 位的 ID 前缀。
func Matches(v *cloudflare.Version, name string) bool {
	return Label(v) == name || v.ID == name || (len(name) >= 8 && strings.HasPrefix(v.ID, name))
}

// envDisplay 是日志里显示的凭据环境变量（Token 不显示）。
func (cfg *Config) envDisplay() string {
	return fmt.Sprintf("CLOUDFLARE_ACCOUNT_ID=%s CLOUDFLARE_API_TOKEN=***", orPlaceholder(cfg.AccountID, "<账号 ID>"))
}

func orPlaceholder(v, p string) string {
	if v == "" {
		return p
	}
	return v
}

// deployArgs 返回 wrangler deploy 的参数。
func (cfg *Config) deployArgs(version, subject string) []string {
	args := []string{"deploy", "--config", cfg.ConfigFile, "--tag", Tag(version), "--message", Message(version, subject)}
	if cfg.NameFlag {
		args = append(args, "--name", cfg.WorkerName)
	}
	return append(args, cfg.DeployArgs...)
}

// wranglerDisplay 是计划里显示的 wrangler 命令。
func (cfg *Config) wranglerDisplay() string {
	if cfg.WranglerVersion != "" {
		return "npx --yes wrangler@" + cfg.WranglerVersion
	}
	return "node_modules/.bin/wrangler"
}

// healthDescription 描述健康检查做什么。
func healthDescription(c *driver.PlanContext) []driver.Command {
	timeout := c.Int("health_timeout", 60)
	cmds := []driver.Command{{Where: "local", Command: "读取 Cloudflare 上最新的部署，确认 100% 流量的是本次的版本", Note: "调用 Cloudflare 接口"}}
	switch c.Str("health_mode") {
	case "http":
		cmds = append(cmds, driver.Command{Where: "local", Command: fmt.Sprintf("每 2 秒请求一次 %s，最多 %d 秒", c.Str("health_url"), timeout), Note: "在本机请求，返回 2xx 或 3xx 即通过"})
	case "command":
		cmds = append(cmds, driver.Command{Where: "local", Command: c.Str("health_command"), Note: fmt.Sprintf("在本机的仓库副本里执行，每 2 秒一次，最多 %d 秒", timeout)})
	}
	return cmds
}

// Plan 生成部署计划。
func (d Driver) Plan(c *driver.PlanContext) *driver.Plan {
	cfg, r := d.Resolve(c)
	q := driver.Q
	name := orPlaceholder(cfg.WorkerName, "<Worker 名称>")
	acc := orPlaceholder(cfg.AccountID, "<账号 ID>")
	if cfg.CredName == "" {
		r.Warn("还没有选择 Cloudflare 账号")
	}

	build := []driver.Command{{Where: "local", Command: fmt.Sprintf("rsync -a --delete --exclude=/.git --exclude=node_modules --exclude=.wrangler %s/ <构建副本>/", q(c.Repo.Path)),
		Note: "复制工作区（含未提交改动），不动仓库自己的 node_modules"}}
	if detect.HasMise(c.Repo.Path) {
		build = append(build, driver.Command{Where: "local", Command: "mise trust --quiet <构建副本>/mise.toml"})
	}
	if cfg.BuildCommand != "" {
		env := []string{"CI=true"}
		for _, kv := range cfg.BuildEnv {
			if kv.Value != "" {
				env = append(env, kv.Key+"="+q(kv.Value))
			}
		}
		build = append(build, driver.Command{Where: "local", Command: "cd <构建副本> && " + strings.Join(env, " ") + " " + cfg.BuildCommand, Note: "${名称} 在执行时替换"})
	}
	build = append(build, driver.Command{Where: "local", Command: "确认 <构建副本>/" + cfg.ConfigFile + " 存在，Worker 名称是 " + name})

	deploy := strings.Join(append([]string{cfg.envDisplay(), cfg.wranglerDisplay()}, quoteAll(cfg.deployArgs(c.Version, c.Repo.Subject))...), " ")
	if detect.HasMise(c.Repo.Path) {
		deploy = cfg.envDisplay() + " mise exec -- " + strings.TrimPrefix(deploy, cfg.envDisplay()+" ")
	}

	p := &driver.Plan{Steps: []driver.Step{
		{Name: "检查账号", Commands: []driver.Command{{Where: "local", Command: fmt.Sprintf("验证 API Token，读取账号 %s 和 Worker %s 的线上版本", acc, name), Note: "调用 Cloudflare 接口，只读"}}},
		{Name: "构建", Commands: build},
		{Name: "发布", Commands: []driver.Command{{Where: "local", Command: "cd <构建副本> && " + deploy,
			Note: "上传新版本并让它接收 100% 流量；路由、自定义域名、定时触发器按配置更新。Token 只通过环境变量传给 wrangler"}}},
		{Name: "健康检查", Commands: healthDescription(c)},
		{Name: "失败时回滚", Commands: []driver.Command{{Where: "local", Command: fmt.Sprintf("POST /accounts/%s/workers/scripts/%s/deployments：把上一个版本设为 100%% 流量", acc, name),
			Note: "调用 Cloudflare 接口，不重新构建；手动回滚也是这样"}}},
	}}
	return r.Finish(p)
}

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = driver.Q(a)
	}
	return out
}

// Checks 返回部署前检查项。
func (d Driver) Checks(c *driver.PlanContext) []driver.Check {
	cfg, _ := d.Resolve(c)
	repo := c.Repo.Path
	checks := []driver.Check{
		{Group: "本机", Name: "wrangler 配置文件", Local: func() (bool, string) {
			if _, err := os.Stat(filepath.Join(repo, cfg.ConfigFile)); err != nil {
				if c.Str("config") != "" && cfg.WorkerName != "" {
					return false, cfg.ConfigFile + " 现在不存在，需要构建后生成"
				}
				return false, "仓库目录里没有 " + cfg.ConfigFile
			}
			if cfg.WorkerName == "" {
				return false, cfg.ConfigFile + " 里没有 name，请填写 Worker 名称"
			}
			return true, fmt.Sprintf("%s（Worker %s）", cfg.ConfigFile, cfg.WorkerName)
		}},
		{Group: "本机", Name: "Node.js", Warn: true, Local: func() (bool, string) {
			if detect.HasMise(repo) {
				return true, "通过 mise exec 执行"
			}
			if p, err := exec.LookPath("node"); err == nil {
				return true, p
			}
			return false, "本机找不到 node 命令，构建和 wrangler 都需要 Node.js"
		}},
		{Group: "本机", Name: "wrangler", Local: func() (bool, string) {
			if cfg.WranglerVersion != "" {
				return true, "用 npx wrangler@" + cfg.WranglerVersion
			}
			pkg, err := detect.ReadPackage(filepath.Dir(filepath.Join(repo, cfg.ConfigFile)))
			if err == nil && pkg == nil {
				pkg, err = detect.ReadPackage(repo)
			}
			switch {
			case err != nil:
				return false, err.Error()
			case pkg.HasDep("wrangler"):
				return true, "项目依赖里的 wrangler（构建命令安装依赖后使用）"
			}
			return false, "项目依赖里没有 wrangler，请在应用配置里填写 wrangler 版本"
		}},
	}
	checks = append(checks,
		driver.Check{Group: "Cloudflare", Name: "账号", NoRepo: true, NeedCreds: true, Local: func() (bool, string) {
			info, err := verify(context.Background(), cfg)
			if err != nil {
				return false, err.Error()
			}
			return true, info
		}},
		driver.Check{Group: "Cloudflare", Name: "Worker", NoRepo: true, NeedCreds: true, Local: func() (bool, string) {
			if cfg.WorkerName == "" {
				return false, "不知道 Worker 名称，请填写"
			}
			st, err := state(cfg, "")
			if err != nil {
				return false, err.Error()
			}
			if st.Current == "" {
				return true, cfg.WorkerName + " 还没有部署过，第一次部署会新建"
			}
			return true, fmt.Sprintf("%s 线上版本 %s", cfg.WorkerName, st.Current)
		}},
	)
	return checks
}
