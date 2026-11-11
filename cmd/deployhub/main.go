// deployhub 启动部署平台：打开数据库、启动 Web 服务并打开浏览器。页面用账号密码登录；
// 还没有账号时在终端打印一次性链接，用它创建第一个管理员。
// 已经在运行时，再次执行只会打开浏览器。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"deployhub/internal/api"
	"deployhub/internal/auth"
	"deployhub/internal/buildinfo"
	"deployhub/internal/core"
	"deployhub/internal/driver/builtin"
	"deployhub/internal/remote"
	"deployhub/internal/store"
	"deployhub/internal/tenant"
	"deployhub/internal/vault"
	"deployhub/web"
)

func main() {
	os.Exit(run())
}

type options struct {
	addr          string
	publicURL     string
	dataDir       string
	noBrowser     bool
	noKeychain    bool
	devUI         string
	resetPassword string
}

func run() int {
	var o options
	fs := flag.NewFlagSet("deployhub", flag.ContinueOnError)
	fs.StringVar(&o.addr, "addr", envOr("DEPLOYHUB_ADDR", "127.0.0.1:7788"), "监听地址（环境变量 DEPLOYHUB_ADDR）；不是本机地址时必须同时指定 -public-url")
	fs.StringVar(&o.publicURL, "public-url", os.Getenv("DEPLOYHUB_PUBLIC_URL"), "浏览器里访问平台用的地址，如 https://deploy.example.com（环境变量 DEPLOYHUB_PUBLIC_URL）；放在反向代理后面或给其他机器访问时填写")
	fs.StringVar(&o.dataDir, "data", envOr("DEPLOYHUB_HOME", "~/.deployhub"), "数据目录（环境变量 DEPLOYHUB_HOME）")
	fs.BoolVar(&o.noBrowser, "no-browser", false, "启动后不自动打开浏览器，只在终端打印地址")
	fs.BoolVar(&o.noKeychain, "no-keychain", false, "主密钥不放钥匙串，存在数据目录的 master.key（权限 600）")
	fs.StringVar(&o.devUI, "dev-ui", "", "前端开发服务器地址，如 http://127.0.0.1:5173；打印的地址指向它，并放行它的 Host 和 Origin")
	fs.StringVar(&o.resetPassword, "reset-password", "", "给这个用户名换一个随机密码并打印出来，然后退出（忘记密码时用）")
	showVersion := fs.Bool("version", false, "显示版本")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "用法：deployhub [选项]\n\n启动部署平台并打开浏览器；已经在运行时只打开浏览器。\n\n选项：\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Println("deployhub", buildinfo.Version)
		return 0
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "deployhub 没有子命令：%s\n日常操作都在页面上完成，运行 deployhub 打开页面；deployhub -h 查看选项\n", strings.Join(fs.Args(), " "))
		return 2
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := serve(o, logger); err != nil {
		fmt.Fprintln(os.Stderr, "deployhub:", err)
		return 1
	}
	return 0
}

func serve(o options, logger *slog.Logger) error {
	publicURL, err := checkListen(o.addr, o.publicURL)
	if err != nil {
		return err
	}
	dataDir, err := expandHome(o.dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录 %s：%w", dataDir, err)
	}
	instanceID := fingerprint(dataDir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, filepath.Join(dataDir, "deployhub.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	accounts := core.NewAccounts(st)
	if o.resetPassword != "" {
		return resetPassword(ctx, accounts, o.resetPassword)
	}
	// 对外地址是 HTTPS 时，会话 Cookie 只通过 HTTPS 发送
	a := auth.New(st, strings.HasPrefix(publicURL, "https://"))

	var keychain vault.Keychain
	if !o.noKeychain {
		keychain = vault.SystemKeychain()
	}
	vt, keyWhere, err := vault.Open(dataDir, "data-"+instanceID, keychain)
	if err != nil {
		return err
	}

	baseURL := "http://" + o.addr
	var extraOrigins []string
	if publicURL != "" {
		baseURL = publicURL
		extraOrigins = append(extraOrigins, publicURL)
	}
	if o.devUI != "" {
		baseURL = strings.TrimRight(o.devUI, "/")
		extraOrigins = append(extraOrigins, o.devUI)
	}

	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return openRunning(ctx, o, a, accounts, instanceID, baseURL)
		}
		return fmt.Errorf("监听 %s：%w", o.addr, err)
	}

	guard, err := auth.NewGuard(o.addr, extraOrigins...)
	if err != nil {
		return err
	}
	drivers, err := builtin.Registry()
	if err != nil {
		return err
	}
	ssh := remote.NewSSH()
	tenants := tenant.NewManager(tenant.Options{Store: st, Vault: vt, Drivers: drivers, Runner: ssh, SSH: ssh, DataDir: dataDir, Logger: logger})
	moveLegacyBuildDir(dataDir, tenants.Dir(1), logger)
	handler := api.NewHandler(api.Options{
		Version:    buildinfo.Version,
		InstanceID: instanceID,
		Auth:       a,
		Guard:      guard,
		Accounts:   accounts,
		Tenants:    tenants,
		Drivers:    drivers,
		UI:         web.Dist(),
		Logger:     logger,
	})
	// 不设 WriteTimeout：SSE 事件流是长连接
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	fmt.Fprintf(os.Stderr, "deployhub %s 已启动：%s（监听 %s）\n数据目录：%s\n主密钥：%s\n", buildinfo.Version, baseURL, o.addr, dataDir, keyWhere)
	if strings.HasPrefix(publicURL, "http://") && !isLoopbackURL(publicURL) {
		fmt.Fprintln(os.Stderr, "注意：对外地址不是 HTTPS，密码和会话会在网络上明文传输。建议在前面放一个带证书的反向代理（Caddy、Nginx），-public-url 填 https:// 地址。")
	}
	if err := printAndOpen(ctx, o, a, accounts, baseURL); err != nil {
		return err
	}

	select {
	case err := <-errc:
		return fmt.Errorf("Web 服务退出：%w", err)
	case <-ctx.Done():
	}
	if n := tenants.Running(); n > 0 {
		fmt.Fprintf(os.Stderr, "有 %d 个任务正在执行，等它们结束后退出（再按一次 Ctrl+C 强制退出）……\n", n)
		done := make(chan struct{})
		go func() { tenants.Wait(); close(done) }()
		force := make(chan os.Signal, 1)
		signal.Notify(force, os.Interrupt, syscall.SIGTERM)
		select {
		case <-done:
		case <-force:
			fmt.Fprintln(os.Stderr, "强制退出，未完成的任务下次启动时标记为中断")
		}
	}
	fmt.Fprintln(os.Stderr, "正在停止……")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// SSE 长连接不会自己结束，超时后直接关闭
		srv.Close()
	}
	return nil
}

// openRunning 处理端口被占用的情况：如果占用者是使用同一个数据目录的 deployhub，
// 直接打开浏览器（还没有账号时，在共享的数据库里生成创建管理员的链接）。
func openRunning(ctx context.Context, o options, a *auth.Auth, accounts *core.Accounts, instanceID, baseURL string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+o.addr+"/api/health", nil)
	// 连的是监听地址，Host 用页面地址：对外监听时平台只认对外地址的 Host
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		req.Host = u.Host
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("端口 %s 已被其他程序占用，可以用 -addr 换一个端口", o.addr)
	}
	defer resp.Body.Close()
	var h struct{ App, Version, Instance string }
	if json.NewDecoder(resp.Body).Decode(&h) != nil || h.App != "deployhub" {
		return fmt.Errorf("端口 %s 已被其他程序占用，可以用 -addr 换一个端口", o.addr)
	}
	if h.Instance != instanceID {
		return fmt.Errorf("端口 %s 上运行的 deployhub 使用的是另一个数据目录，可以用 -addr 换一个端口", o.addr)
	}
	fmt.Fprintf(os.Stderr, "deployhub %s 已经在运行：%s\n", h.Version, baseURL)
	return printAndOpen(ctx, o, a, accounts, baseURL)
}

// printAndOpen 打开浏览器。还没有账号时，打开的是创建管理员的一次性链接；有账号后就是登录页。
func printAndOpen(ctx context.Context, o options, a *auth.Auth, accounts *core.Accounts, baseURL string) error {
	link := baseURL
	setup, err := accounts.NeedsSetup(ctx)
	if err != nil {
		return err
	}
	if setup {
		token, err := a.NewSetupToken(ctx)
		if err != nil {
			return err
		}
		link = baseURL + "/setup?token=" + url.QueryEscape(token)
		fmt.Fprintf(os.Stderr, "还没有账号。打开下面的链接创建管理员（%d 分钟内有效，只能用一次）：\n  %s\n", int(auth.SetupTTL.Minutes()), link)
	} else {
		fmt.Fprintf(os.Stderr, "用账号密码登录：%s\n忘记密码时运行 deployhub -reset-password <用户名>\n", link)
	}
	if o.noBrowser {
		return nil
	}
	if err := openBrowser(link); err != nil {
		fmt.Fprintf(os.Stderr, "无法自动打开浏览器（%v），请手动打开上面的地址\n", err)
	}
	return nil
}

// resetPassword 给账号换一个随机密码并打印。直接改数据库，服务在不在运行都可以用。
func resetPassword(ctx context.Context, accounts *core.Accounts, username string) error {
	password, err := accounts.ResetPassword(ctx, username)
	if err != nil {
		return fmt.Errorf("重置 %s 的密码：%w", username, err)
	}
	fmt.Fprintf(os.Stderr, "账号 %s 的新密码（已登录的页面全部失效，登录后请在「设置」里改掉）：\n", core.NormalizeUsername(username))
	fmt.Println(password)
	return nil
}

// moveLegacyBuildDir 把升级前的构建目录 <数据目录>/build 挪给第一个账号（升级前的数据都归它），保留构建缓存。
func moveLegacyBuildDir(dataDir, accountDir string, logger *slog.Logger) {
	old, dst := filepath.Join(dataDir, "build"), filepath.Join(accountDir, "build")
	if _, err := os.Stat(old); err != nil {
		return
	}
	if _, err := os.Stat(dst); err == nil {
		return
	}
	if err := os.MkdirAll(accountDir, 0o700); err == nil {
		err = os.Rename(old, dst)
		if err == nil {
			return
		}
		logger.Warn("挪动旧的构建目录失败，下次构建会重新生成缓存", "from", old, "to", dst, "err", err)
	}
}

func openBrowser(link string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", link).Start()
	case "linux":
		return exec.Command("xdg-open", link).Start()
	default:
		return fmt.Errorf("不支持的系统 %s", runtime.GOOS)
	}
}

// checkListen 校验监听地址和对外地址，返回规范化后的对外地址（没有时为空）。
// 监听本机地址时可以不填对外地址；监听其他地址时必须填，平台只接受 Host 和 Origin 是这个地址的请求。
func checkListen(addr, publicURL string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "", fmt.Errorf("监听地址 %s 无效，应形如 127.0.0.1:7788", addr)
	}
	publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if publicURL != "" {
		u, err := url.Parse(publicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
			return "", fmt.Errorf("对外地址 %s 无效，应形如 https://deploy.example.com，不带路径", publicURL)
		}
	}
	loopback := host == "localhost"
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if !loopback && publicURL == "" {
		return "", fmt.Errorf("监听地址 %s 不是本机地址，要同时用 -public-url 指定浏览器里访问用的地址，如 -public-url https://deploy.example.com", addr)
	}
	return publicURL, nil
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("找不到用户主目录：%w", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("数据目录 %s 无效：%w", p, err)
	}
	return abs, nil
}

func fingerprint(dataDir string) string {
	sum := sha256.Sum256([]byte(dataDir))
	return hex.EncodeToString(sum[:8])
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
