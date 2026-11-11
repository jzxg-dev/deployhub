package workers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deployhub/internal/cloudflare"
	"deployhub/internal/detect"
	"deployhub/internal/driver"
	"deployhub/internal/shell"
)

var (
	_ driver.Executor      = Driver{}
	_ driver.HealthChecker = Driver{}
)

func client(cfg *Config) *cloudflare.Client { return cloudflare.New(cfg.AccountID, cfg.Token) }

// ready 检查凭据和 Worker 名称都有了。
func (cfg *Config) ready() error {
	switch {
	case cfg.CredName == "":
		return errors.New("没有选择 Cloudflare 账号")
	case cfg.AccountID == "" || cfg.Token == "":
		return fmt.Errorf("凭据 %s 没有账号 ID 或 API Token，请在「凭据」里补上", cfg.CredName)
	case cfg.WorkerName == "":
		return errors.New("不知道 Worker 名称：wrangler 配置文件里没有 name（或要构建后才生成），请在应用配置里填写 Worker 名称")
	}
	return nil
}

// verify 验证 API Token 和账号，返回说明。
func verify(ctx context.Context, cfg *Config) (string, error) {
	if cfg.CredName == "" {
		return "", errors.New("没有选择 Cloudflare 账号")
	}
	if cfg.AccountID == "" || cfg.Token == "" {
		return "", fmt.Errorf("凭据 %s 没有账号 ID 或 API Token", cfg.CredName)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := client(cfg)
	tok, err := c.VerifyToken(ctx)
	if err != nil {
		return "", err
	}
	if tok.Status != "active" {
		return "", fmt.Errorf("API Token 的状态是 %s，不能使用", tok.Status)
	}
	acc, err := c.Account(ctx)
	if err != nil {
		return "", fmt.Errorf("Token 有效，但访问不了账号 %s：%w", cfg.AccountID, err)
	}
	return fmt.Sprintf("账号 %s（凭据 %s，Token 有效）", acc.Name, cfg.CredName), nil
}

// online 是 Worker 的线上情况。
type online struct {
	state    *driver.OnlineState
	versions []cloudflare.Version // 新的在前
	current  string               // 100% 流量（或流量最大）的版本 ID
	percent  float64
}

// fetch 读取 Worker 的版本和最新部署；还没部署过时返回空的状态。
func fetch(ctx context.Context, cfg *Config, app string) (*online, error) {
	if err := cfg.ready(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := client(cfg)
	o := &online{state: &driver.OnlineState{Releases: []string{}}}
	deps, err := c.Deployments(ctx, cfg.WorkerName)
	if cloudflare.IsNotFound(err) {
		return o, nil
	}
	if err != nil {
		return nil, err
	}
	if o.versions, err = c.Versions(ctx, cfg.WorkerName); err != nil && !cloudflare.IsNotFound(err) {
		return nil, err
	}
	labels := map[string]string{}
	for i := range o.versions {
		l := Label(&o.versions[i])
		labels[o.versions[i].ID] = l
		if i < 10 { // 页面上列出最近 10 个，可以回滚
			o.state.Releases = append(o.state.Releases, l)
		}
	}
	if len(deps) == 0 {
		return o, nil
	}
	d := deps[0]
	o.current, o.percent = d.Main()
	cur := labels[o.current]
	if cur == "" {
		cur = ShortID(o.current)
	}
	o.state.Current = cur
	action := "deploy"
	if d.Annotations["workers/triggered_by"] == "rollback" || strings.HasPrefix(d.Annotations["workers/message"], rollbackPrefix) {
		action = "rollback"
	}
	result := "success"
	if o.percent < 100 {
		result = fmt.Sprintf("%.0f%% 流量", o.percent)
	}
	when := d.CreatedOn
	if t, err := time.Parse(time.RFC3339Nano, d.CreatedOn); err == nil {
		when = t.Local().Format("2006-01-02 15:04:05")
	}
	o.state.Last = strings.Join([]string{when, app, cur, action, result, d.AuthorEmail}, "\t")
	return o, nil
}

func state(cfg *Config, app string) (*driver.OnlineState, error) {
	o, err := fetch(context.Background(), cfg, app)
	if err != nil {
		return nil, err
	}
	return o.state, nil
}

// rollbackPrefix 是部署平台切换版本时写的部署说明。
const rollbackPrefix = "deployhub 切换到 "

// Prepare 检查账号：验证 API Token，读取 Worker 的线上版本。
func (d Driver) Prepare(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	info, err := verify(x.Ctx, cfg)
	if err != nil {
		return err
	}
	x.Info("Cloudflare %s", info)
	o, err := fetch(x.Ctx, cfg, x.C.App)
	if err != nil {
		return fmt.Errorf("读取 Worker %s：%w", cfg.WorkerName, err)
	}
	if o.state.Current == "" {
		x.Info("Worker %s 还没有部署过，这次会新建", cfg.WorkerName)
	} else {
		x.Info("Worker %s 线上版本 %s", cfg.WorkerName, o.state.Current)
	}
	return nil
}

// srcDir 是构建副本：这个应用的各次部署共用，保留依赖。
func srcDir(x *driver.Exec) string { return filepath.Join(x.CacheDir, "src") }

// buildEnv 返回构建和 wrangler 都要的环境变量。
func (cfg *Config) buildEnv(x *driver.Exec) []string {
	short := x.Commit
	if len(short) > 7 {
		short = short[:7]
	}
	r := strings.NewReplacer("{version}", x.C.Version, "{commit}", short)
	env := []string{"CI=true", "DEPLOYHUB_VERSION=" + x.C.Version, "DEPLOYHUB_COMMIT=" + x.Commit}
	for _, kv := range cfg.BuildEnv {
		if kv.Value != "" {
			env = append(env, kv.Key+"="+r.Replace(kv.Value))
		}
	}
	return env
}

// wranglerEnv 在构建环境变量之外加上凭据。只把选中的账号传给 wrangler，清掉终端里可能残留的其他 Cloudflare 凭据。
func (cfg *Config) wranglerEnv(x *driver.Exec) []string {
	env := append(cfg.buildEnv(x), "WRANGLER_SEND_METRICS=false",
		"CLOUDFLARE_ACCOUNT_ID="+cfg.AccountID, "CLOUDFLARE_API_TOKEN="+cfg.Token,
		"CLOUDFLARE_API_KEY=", "CLOUDFLARE_EMAIL=", "CF_API_KEY=", "CF_EMAIL=", "CF_API_TOKEN=", "CF_ACCOUNT_ID=")
	return env
}

// wranglerCmd 返回执行 wrangler 的命令：填了版本用 npx wrangler@<版本>，否则用项目依赖里的（从配置文件所在目录往上找）。
// 仓库有 mise 配置时通过 mise exec 执行。
func (cfg *Config) wranglerCmd(src string) (string, []string, error) {
	name, args := "", []string(nil)
	if cfg.WranglerVersion != "" {
		name, args = "npx", []string{"--yes", "wrangler@" + cfg.WranglerVersion}
	} else {
		dir := filepath.Dir(filepath.Join(src, cfg.ConfigFile))
		for {
			bin := filepath.Join(dir, "node_modules", ".bin", "wrangler")
			if _, err := os.Stat(bin); err == nil {
				name = bin
				break
			}
			if dir == src || len(dir) <= len(src) {
				return "", nil, errors.New("项目里没有安装 wrangler（找不到 node_modules/.bin/wrangler）：请在构建命令里安装依赖，或在应用配置里填写 wrangler 版本")
			}
			dir = filepath.Dir(dir)
		}
	}
	if detect.HasMise(src) {
		return "mise", append([]string{"exec", "--", name}, args...), nil
	}
	return name, args, nil
}

// display 把命令换成日志里显示的样子：凭据只显示账号 ID，wrangler 的绝对路径换成相对构建副本的。
func display(cfg *Config, src, name string, args []string) string {
	parts := []string{cfg.envDisplay()}
	for _, p := range append([]string{name}, args...) {
		if rel, err := filepath.Rel(src, p); err == nil && filepath.IsAbs(p) && !strings.HasPrefix(rel, "..") {
			p = rel
		}
		parts = append(parts, driver.Q(p))
	}
	return strings.Join(parts, " ")
}

// Build 在仓库工作区的副本里构建（包含未提交的改动，不动仓库自己的 node_modules），确认 wrangler 配置文件和 wrangler 都在。
func (d Driver) Build(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	src := srcDir(x)
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}
	if err := x.Local(shell.Cmd{Name: "rsync", Args: []string{
		"-a", "--delete", "--exclude=/.git", "--exclude=node_modules", "--exclude=.wrangler",
		strings.TrimRight(x.C.Repo.Path, "/") + "/", src + "/",
	}, Display: fmt.Sprintf("rsync 仓库工作区 → 构建副本 %s（不含 .git、node_modules、.wrangler）", src)}); err != nil {
		return err
	}
	// 构建副本是新路径，mise 要求先信任其中的 mise.toml，否则 mise exec 会报错
	for _, name := range []string{"mise.toml", ".mise.toml"} {
		if _, err := os.Stat(filepath.Join(src, name)); err == nil {
			if err := x.Local(shell.Cmd{Name: "mise", Args: []string{"trust", "--quiet", filepath.Join(src, name)}}); err != nil {
				return err
			}
		}
	}
	if cfg.BuildCommand != "" {
		env := cfg.buildEnv(x)
		if err := x.Local(shell.Cmd{Name: "sh", Args: []string{"-c", cfg.BuildCommand}, Dir: src, Env: env,
			Display: strings.Join(append([]string{"CI=true"}, env[3:]...), " ") + " " + cfg.BuildCommand}); err != nil {
			return err
		}
	} else {
		x.Info("没有构建命令，由 wrangler 打包")
	}
	w, err := detect.ReadWrangler(filepath.Join(src, cfg.ConfigFile))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("构建副本里没有 wrangler 配置文件 %s", cfg.ConfigFile)
		}
		return err
	}
	switch {
	case cfg.NameFlag && w.Name != "" && w.Name != cfg.WorkerName:
		x.Info("配置文件里的名称是 %s，发布时用 --name %s", w.Name, cfg.WorkerName)
	case !cfg.NameFlag && w.Name != cfg.WorkerName:
		return fmt.Errorf("构建后 %s 里的 Worker 名称是 %q，和预检时读到的 %s 不一致，请在应用配置里填写 Worker 名称", cfg.ConfigFile, w.Name, cfg.WorkerName)
	}
	name, args, err := cfg.wranglerCmd(src)
	if err != nil {
		return err
	}
	x.Info("发布用 %s，配置文件 %s", strings.TrimPrefix(display(cfg, src, name, args), cfg.envDisplay()+" "), cfg.ConfigFile)
	if w.WorkersDev {
		x.Info("配置里开着 workers.dev 地址")
	}
	if len(w.Domains) > 0 {
		x.Info("路由和自定义域名：%s", strings.Join(w.Domains, "、"))
	}
	return nil
}

// Upload 没有这一步：wrangler deploy 在发布时一起上传。
func (d Driver) Upload(x *driver.Exec) error { return nil }

// Activate 让 version 接收 100% 流量。Cloudflare 上已有这个版本（回滚、自动回滚）时直接调接口切换；
// 还没有（本次新构建的）时用 wrangler deploy 发布。
func (d Driver) Activate(x *driver.Exec, version string) error {
	cfg, _ := d.Resolve(x.C)
	if err := cfg.ready(); err != nil {
		return err
	}
	c := client(cfg)
	ctx, cancel := context.WithTimeout(x.Ctx, 30*time.Second)
	vers, err := c.Versions(ctx, cfg.WorkerName)
	cancel()
	if err != nil && !cloudflare.IsNotFound(err) {
		return err
	}
	for i := range vers {
		v := &vers[i]
		if !Matches(v, version) {
			continue
		}
		x.Info("通过 Cloudflare 接口把版本 %s（ID %s）设为 100%% 流量", version, ShortID(v.ID))
		ctx, cancel := context.WithTimeout(x.Ctx, 30*time.Second)
		defer cancel()
		if err := c.Deploy(ctx, cfg.WorkerName, v.ID, rollbackPrefix+version); err != nil {
			return err
		}
		x.Activated = version
		return nil
	}
	if version != x.C.Version {
		return fmt.Errorf("Cloudflare 上没有版本 %s（可能已经不在 Cloudflare 保留的版本里）", version)
	}
	src := srcDir(x)
	name, args, err := cfg.wranglerCmd(src)
	if err != nil {
		return err
	}
	args = append(args, cfg.deployArgs(version, x.C.Repo.Subject)...)
	if err := x.Local(shell.Cmd{Name: name, Args: args, Dir: src, Env: cfg.wranglerEnv(x), Display: display(cfg, src, name, args)}); err != nil {
		return err
	}
	x.Activated = version
	return nil
}

// HealthScript 不用：健康检查在本机做，见 Health。
func (d Driver) HealthScript(x *driver.Exec) string { return "" }

// Health 先确认 Cloudflare 上 100% 流量的是刚切换的版本，再按检查方式请求地址或执行命令（都在本机）。
func (d Driver) Health(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	want := x.Activated
	if want == "" {
		want = x.C.Version
	}
	timeout := time.Duration(x.C.Int("health_timeout", 60)) * time.Second
	deadline := time.Now().Add(timeout)

	// 部署记录是接口同步写的，一般第一次就对；最多等 30 秒
	var last string
	for i := 0; ; i++ {
		o, err := fetch(x.Ctx, cfg, x.C.App)
		switch {
		case err != nil:
			last = err.Error()
		case o.state.Current == want && o.percent >= 100:
			x.Info("Cloudflare 上 100%% 流量的是 %s", want)
			last = ""
		case o.state.Current == want:
			last = fmt.Sprintf("%s 只有 %.0f%% 流量", want, o.percent)
		default:
			last = fmt.Sprintf("100%% 流量的是 %s，不是 %s", orPlaceholder(o.state.Current, "（无）"), want)
		}
		if last == "" {
			break
		}
		if i >= 15 {
			return errors.New("线上版本不对：" + last)
		}
		if err := sleep(x.Ctx, 2*time.Second); err != nil {
			return err
		}
	}

	switch x.C.Str("health_mode") {
	case "http":
		return httpCheck(x, x.C.Str("health_url"), timeout, deadline)
	case "command":
		cmd := x.C.Str("health_command")
		for {
			err := x.Local(shell.Cmd{Name: "sh", Args: []string{"-c", cmd}, Dir: srcDir(x), Env: cfg.buildEnv(x)})
			if err == nil {
				return nil
			}
			var se *shell.Error
			if !errors.As(err, &se) || se.ExitCode <= 0 {
				return err
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%d 秒内没有通过（检查方式：自定义命令），详见上面的输出", int(timeout.Seconds()))
			}
			if err := sleep(x.Ctx, 2*time.Second); err != nil {
				return err
			}
		}
	}
	return nil
}

// httpCheck 在本机每 2 秒请求一次地址，返回 2xx 或 3xx 即通过（和 curl -f 一样，不跟随跳转）。
func httpCheck(x *driver.Exec, url string, timeout time.Duration, deadline time.Time) error {
	hc := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var last string
	for {
		req, err := http.NewRequestWithContext(x.Ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Cache-Control", "no-cache")
		resp, err := hc.Do(req)
		msg := ""
		if err != nil {
			msg = err.Error()
		} else {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				x.Info("%s 返回 %d", url, resp.StatusCode)
				return nil
			}
			msg = fmt.Sprintf("返回 %d", resp.StatusCode)
		}
		if msg != last {
			x.Info("%s：%s", url, msg)
			last = msg
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d 秒内没有通过（检查方式：HTTP 地址 %s），最后一次：%s", int(timeout.Seconds()), url, msg)
		}
		if err := sleep(x.Ctx, 2*time.Second); err != nil {
			return err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// State 读取线上版本：100% 流量的版本和最近 10 个可以回滚的版本。
func (d Driver) State(x *driver.Exec) (*driver.OnlineState, error) {
	cfg, _ := d.Resolve(x.C)
	o, err := fetch(x.Ctx, cfg, x.C.App)
	if err != nil {
		return nil, err
	}
	return o.state, nil
}

// Cleanup 没有这一步：旧版本由 Cloudflare 保留和清理。
func (d Driver) Cleanup(x *driver.Exec) error { return nil }

// LogFile 没有服务器，部署记录只在本地任务里。
func (d Driver) LogFile(x *driver.Exec) string { return "" }
