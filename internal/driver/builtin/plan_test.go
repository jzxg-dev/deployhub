package builtin

import (
	"strings"
	"testing"

	"deployhub/internal/driver"
	"deployhub/internal/driver/gobinary"
	"deployhub/internal/driver/image"
	"deployhub/internal/driver/static"
)

func ctx(app string, params map[string]any) *driver.PlanContext {
	return &driver.PlanContext{
		Project: "shop", App: app, Params: params, Version: "main-a1b2c3d-20261107-1200",
		Server: driver.ServerInfo{Name: "shop-main", Dest: "deploy@1.2.3.4", User: "deploy", Arch: "arm64"},
		Repo:   driver.RepoInfo{Path: "/repo", Hints: map[string]string{"build_command": "pnpm install --frozen-lockfile && pnpm build", "dist": "dist", "dockerfile": "Dockerfile"}},
	}
}

// clean 用和保存时一样的规则整理参数
func clean(t *testing.T, d driver.Driver, params map[string]any) map[string]any {
	t.Helper()
	out, errs := driver.Clean(append(driver.CommonFields(), d.Spec().Fields...), params)
	if len(errs) > 0 {
		t.Fatalf("参数校验失败：%v", errs)
	}
	return out
}

func base(name string) map[string]any {
	return map[string]any{"name": name, "title": name, "repo_path": "/repo", "branch": "main", "server": "shop-main"}
}

func allCommands(p *driver.Plan) string {
	var b strings.Builder
	for _, s := range p.Steps {
		for _, c := range s.Commands {
			b.WriteString(c.Command + "\n")
		}
	}
	return b.String()
}

func TestGoBinaryPlanMatchesOldTool(t *testing.T) {
	// 对应旧工具 deploy/open-api.toml
	p := base("open-api")
	p["main"] = "./cmd/open-api"
	p["remote_dir"] = "/opt/shop/api/open-api"
	p["working_dir"] = "/opt/shop/api/shared"
	p["env"] = map[string]any{"SERVER_PORT": "8889"}
	p["health_mode"] = "http"
	p["health_url"] = "http://127.0.0.1:8889/ping"
	p["ldflags_x"] = map[string]any{"go-service-api/global.Version": "{version}"}
	d := gobinary.Driver{}
	plan := d.Plan(ctx("open-api", clean(t, d, p)))
	cmds := allCommands(plan)
	for _, want := range []string{
		"GOARCH=arm64 go build -trimpath",            // 按服务器检测的架构
		"-X go-service-api/global.Version={version}", // 版本注入
		"-o '<构建目录>/open-api' ./cmd/open-api",
		"/opt/shop/api/open-api/releases/main-a1b2c3d-20261107-1200",
		"mkdir -p /opt/shop/api/open-api/releases /opt/shop/api/shared",
		"ln -sfn releases/main-a1b2c3d-20261107-1200 current.tmp && mv -T current.tmp current",
		"sudo systemctl restart open-api",
		"curl -fsS -o /dev/null --max-time 5 http://127.0.0.1:8889/ping",
		"Environment=SERVER_PORT=8889",
		"ExecStart=/opt/shop/api/open-api/current/open-api",
		"WorkingDirectory=/opt/shop/api/shared",
		"User=deploy",
	} {
		if !strings.Contains(cmds, want) {
			t.Errorf("命令里缺少 %q\n%s", want, cmds)
		}
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("不应有提醒：%v", plan.Warnings)
	}
}

func TestGoBinaryDefaults(t *testing.T) {
	p := base("shop-api")
	p["main"] = "./cmd/shop-api"
	d := gobinary.Driver{}
	c := ctx("shop-api", clean(t, d, p))
	c.Server.Arch = ""
	plan := d.Plan(c)
	resolved := map[string]string{}
	for _, r := range plan.Resolved {
		resolved[r.Key] = r.Value + "（" + r.Source + "）"
	}
	want := map[string]string{
		"goarch":       "amd64（默认）",
		"remote_dir":   "/opt/shop/shop-api（默认）",
		"working_dir":  "/opt/shop/shop-api/shared（默认）",
		"service":      "shop-api（默认）",
		"service_user": "deploy（SSH 登录用户）",
	}
	for k, v := range want {
		if resolved[k] != v {
			t.Errorf("%s = %q，期望 %q", k, resolved[k], v)
		}
	}
	if len(plan.Warnings) == 0 {
		t.Error("没检测过架构时应提醒")
	}
}

func TestStaticPlan(t *testing.T) {
	// 对应旧工具 deploy/payment.toml
	p := base("payment")
	p["remote_dir"] = "/srv/www/shop.example.com"
	p["index_check_domain"] = "shop.example.com"
	p["build_env"] = map[string]any{"VITE_APP_API_URL": "${PAYMENT_API_URL:-}"}
	p["exclude"] = []any{"*.map", "dist.zip"}
	d := static.Driver{}
	plan := d.Plan(ctx("payment", clean(t, d, p)))
	cmds := allCommands(plan)
	for _, want := range []string{
		"CI=true VITE_APP_API_URL='${PAYMENT_API_URL:-}' pnpm install --frozen-lockfile && pnpm build",
		"跳过 *.map、dist.zip",
		"/srv/www/shop.example.com/releases/main-a1b2c3d-20261107-1200",
		"--resolve shop.example.com:443:127.0.0.1 https://shop.example.com/index.html | cmp -s - /srv/www/shop.example.com/current/index.html",
	} {
		if !strings.Contains(cmds, want) {
			t.Errorf("命令里缺少 %q\n%s", want, cmds)
		}
	}
}

func TestImagePlan(t *testing.T) {
	// 对应旧工具 deploy/shop-mcp.toml
	p := base("shop-mcp")
	p["compose_dir"] = "/opt/shop/shop-mcp"
	p["config_files"] = []any{map[string]any{"name": "config.toml", "content": "x"}}
	p["required_config"] = []any{map[string]any{"file": "config.toml", "key": "MCP.enabled", "value": "true"}}
	d := image.Driver{}
	plan := d.Plan(ctx("shop-mcp", clean(t, d, p)))
	cmds := allCommands(plan)
	for _, want := range []string{
		"docker buildx build --platform linux/arm64 -f Dockerfile -t shop-mcp:main-a1b2c3d-20261107-1200",
		"--load",
		"docker save shop-mcp:main-a1b2c3d-20261107-1200 | gzip | ssh deploy@1.2.3.4",
		"sed -i '/^SHOP_MCP_TAG=/d' .env && echo SHOP_MCP_TAG=main-a1b2c3d-20261107-1200 >> .env",
		"docker compose up -d shop-mcp",
		"image: shop-mcp:${SHOP_MCP_TAG:-latest}",
		"chmod 600 /opt/shop/shop-mcp/config.toml.uploading",
		"-v key=enabled -v want=true",
	} {
		if !strings.Contains(cmds, want) {
			t.Errorf("命令里缺少 %q\n%s", want, cmds)
		}
	}
	// 配置文件由平台上传，前置条件留到部署时检查
	for _, ch := range d.Checks(ctx("shop-mcp", clean(t, d, p))) {
		if strings.HasPrefix(ch.Name, "配置项") {
			t.Errorf("平台上传的配置文件不应在检查时读服务器上的旧文件：%s", ch.Name)
		}
	}
}

func TestEveryBuiltinDriverPlans(t *testing.T) {
	r, _ := Registry()
	for _, s := range r.Catalog().Drivers {
		d, _ := r.Get(s.Name)
		if _, ok := d.(driver.Planner); !ok {
			t.Errorf("部署方式 %s 没有实现 Planner", s.Name)
		}
	}
}
