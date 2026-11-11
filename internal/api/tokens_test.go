package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// call 用访问令牌发请求：不带 Cookie，也不带 Origin（脚本和 AI 不是浏览器）。
func (e *env) call(t *testing.T, method, target, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = testHost
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// newToken 在页面上（会话 c）创建一个访问令牌，返回明文。
func (e *env) newToken(t *testing.T, c *http.Cookie, name, level string) string {
	t.Helper()
	rec := e.send(t, "POST", "/api/tokens", `{"name":"`+name+`","level":"`+level+`"}`, c)
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != 201 || err != nil || !strings.HasPrefix(out.Token, "dh_") {
		t.Fatalf("创建令牌 = %d %s", rec.Code, rec.Body)
	}
	return out.Token
}

func TestTokenLifecycle(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)
	if rec := e.send(t, "POST", "/api/tokens", `{"name":"","level":"root"}`, c); rec.Code != 422 {
		t.Fatalf("名称为空应 422，实际 %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/tokens", `{"name":"x","level":"root"}`, c); rec.Code != 422 || !strings.Contains(rec.Body.String(), `"level"`) {
		t.Fatalf("权限不对应 422，实际 %d %s", rec.Code, rec.Body)
	}
	token := e.newToken(t, c, "Claude Code", "read")
	if rec := e.send(t, "POST", "/api/tokens", `{"name":"Claude Code","level":"read"}`, c); rec.Code != 422 {
		t.Fatalf("重名应 422，实际 %d", rec.Code)
	}
	// 列表里没有令牌本身，库里也没有明文
	rec := e.do(t, "GET", "/api/tokens", c)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"Claude Code"`) || strings.Contains(rec.Body.String(), token) {
		t.Fatalf("令牌列表 = %d %s", rec.Code, rec.Body)
	}

	// 用令牌调用：不需要 Cookie 和 Origin
	rec = e.call(t, "GET", "/api/session", "", token)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"username":"admin"`) || !strings.Contains(rec.Body.String(), `"level":"read"`) {
		t.Fatalf("令牌读取会话 = %d %s", rec.Code, rec.Body)
	}
	// 令牌不对：401，即使同时带着有效的 Cookie 也不认
	if rec := e.call(t, "GET", "/api/projects", "", "dh_wrong"); rec.Code != 401 || errorCode(rec) != "invalid_token" {
		t.Fatalf("错误的令牌 = %d %s", rec.Code, rec.Body)
	}
	req := httptest.NewRequest("GET", "/api/projects", nil)
	req.Host = testHost
	req.Header.Set("Authorization", "Bearer dh_wrong")
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("带了错误的令牌时不应退回去用 Cookie，实际 %d", rr.Code)
	}
	// 不带令牌也不带 Cookie 的修改请求：没有 Origin，被来源校验拦下
	if rec := e.call(t, "POST", "/api/projects", `{}`, ""); rec.Code != 403 {
		t.Fatalf("没有令牌的非浏览器请求 = %d", rec.Code)
	}

	// 令牌不能管理令牌
	if rec := e.call(t, "GET", "/api/tokens", "", token); rec.Code != 403 || errorCode(rec) != "page_only" {
		t.Fatalf("令牌读令牌列表 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.call(t, "POST", "/api/tokens", `{"name":"more","level":"admin"}`, token); rec.Code != 403 {
		t.Fatalf("令牌创建令牌 = %d", rec.Code)
	}

	// 吊销后立刻不能用
	if rec := e.send(t, "DELETE", "/api/tokens/Claude%20Code", ``, c); rec.Code != 200 {
		t.Fatalf("吊销 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.call(t, "GET", "/api/session", "", token); rec.Code != 401 {
		t.Fatalf("吊销后 = %d", rec.Code)
	}
}

func TestTokenLevels(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)
	read, deploy, admin := e.newToken(t, c, "r", "read"), e.newToken(t, c, "d", "deploy"), e.newToken(t, c, "a", "admin")
	if rec := e.send(t, "POST", "/api/secrets", `{"name":"API_URL","value":"https://secret.example"}`, c); rec.Code != 201 {
		t.Fatal(rec.Body)
	}

	type tc struct {
		method, path, body string
		want               map[string]int // 令牌 → 状态码
	}
	cases := []tc{
		{"GET", "/api/projects", "", map[string]int{read: 200, deploy: 200, admin: 200}},
		{"GET", "/api/secrets", "", map[string]int{read: 200}}, // 只有名称
		{"POST", "/api/projects", `{"name":"p1","title":"项目"}`, map[string]int{read: 403, deploy: 403, admin: 201}},
		{"POST", "/api/deploys", `{"apps":["p1/nope"]}`, map[string]int{read: 403, deploy: 404, admin: 404}}, // 404：过了权限检查，应用不存在
		{"POST", "/api/rollbacks", `{"app":"p1/nope"}`, map[string]int{read: 403, deploy: 404}},
		{"POST", "/api/jobs/1/cancel", "", map[string]int{read: 403, deploy: 409}},
		{"POST", "/api/servers", `{"name":"s1","host":"1.2.3.4"}`, map[string]int{read: 403, deploy: 403, admin: 201}},
		// 下面这些不管什么级别都只能在页面上做
		{"POST", "/api/secrets/API_URL/reveal", "", map[string]int{read: 403, deploy: 403, admin: 403}},
		{"POST", "/api/secrets", `{"name":"X1","value":"v"}`, map[string]int{admin: 403}},
		{"POST", "/api/export", `{"sensitive":true}`, map[string]int{admin: 403}},
		{"DELETE", "/api/projects/p1", "", map[string]int{admin: 403}},
		{"DELETE", "/api/servers/s1", "", map[string]int{admin: 403}},
		{"GET", "/api/users", "", map[string]int{admin: 403}},
		{"POST", "/api/account/password", `{}`, map[string]int{admin: 403}},
		{"POST", "/api/jobs/1/approve", "", map[string]int{admin: 403}},
		{"POST", "/api/jobs/1/reject", "", map[string]int{admin: 403}},
		{"POST", "/api/credentials", `{}`, map[string]int{admin: 403}},
	}
	names := map[string]string{read: "只读", deploy: "可部署", admin: "管理"}
	for _, c := range cases {
		for token, want := range c.want {
			if rec := e.call(t, c.method, c.path, c.body, token); rec.Code != want {
				t.Errorf("%s 令牌 %s %s = %d，期望 %d：%s", names[token], c.method, c.path, rec.Code, want, rec.Body)
			}
		}
	}
	if rec := e.call(t, "GET", "/api/secrets", "", admin); strings.Contains(rec.Body.String(), "secret.example") {
		t.Fatalf("令牌读到了密钥的值：%s", rec.Body)
	}
	if rec := e.call(t, "POST", "/api/projects", `{"name":"p2","title":"项目"}`, read); errorCode(rec) != "forbidden" || !strings.Contains(rec.Body.String(), "管理") {
		t.Fatalf("权限不够时应说明需要什么权限：%s", rec.Body)
	}
}

var pathParam = regexp.MustCompile(`\{[a-z]+\}`)

// 路由表里的每个接口：除了明确公开的，不登录一律 401；只读令牌只能调用标了只读的。
// 以后新增接口忘了想清楚访问要求，这个测试会提醒。
func TestEveryRouteIsGuarded(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)
	read := e.newToken(t, c, "r", "read")
	public := map[string]bool{"GET /api/health": true, "POST /api/login": true, "POST /api/logout": true, "POST /api/setup": true}
	s := &server{}
	seen := map[string]bool{}
	for _, rt := range s.routes() {
		key := rt.method + " " + rt.path
		if seen[key] {
			t.Errorf("接口 %s 重复", key)
		}
		seen[key] = true
		if rt.summary == "" || rt.tag == "" {
			t.Errorf("接口 %s 没有说明", key)
		}
		if (rt.access == accessPublic) != public[key] {
			t.Errorf("接口 %s 的公开设置和预期不一致", key)
		}
		if rt.access == accessPublic || rt.path == "/api/events" {
			continue
		}
		path := pathParam.ReplaceAllString(rt.path, "1")
		// 不登录（带 Origin，排除来源校验的影响）
		if rec := e.send(t, rt.method, path, `{}`, nil); rec.Code != 401 {
			t.Errorf("不登录调用 %s = %d，期望 401", key, rec.Code)
		}
		rec := e.call(t, rt.method, path, `{}`, read)
		if rt.access == accessRead {
			if rec.Code == 401 || rec.Code == 403 {
				t.Errorf("只读令牌调用 %s = %d，应该允许", key, rec.Code)
			}
		} else if rec.Code != 403 {
			t.Errorf("只读令牌调用 %s = %d，期望 403", key, rec.Code)
		}
	}
}

// 令牌读不到敏感参数的内容；把读到的参数改一改再提交，原来的内容不会丢。
func TestTokenCannotReadSensitiveParams(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)
	admin := e.newToken(t, c, "a", "admin")
	for _, body := range []string{`{"name":"srv","host":"1.2.3.4"}`} {
		if rec := e.send(t, "POST", "/api/servers", body, c); rec.Code != 201 {
			t.Fatal(rec.Body)
		}
	}
	if rec := e.send(t, "POST", "/api/projects", `{"name":"proj","title":"项目"}`, c); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	const secret = "db_password = hunter2-very-secret"
	params := `{"name":"app","title":"应用","repo_path":"` + t.TempDir() + `","branch":"main","server":"srv","main":"./cmd/app",` +
		`"config_files":[{"name":"config.toml","source":"platform","content":"` + secret + `"}]}`
	if rec := e.send(t, "POST", "/api/projects/proj/apps", `{"driver":"gobinary","params":`+params+`}`, c); rec.Code != 201 {
		t.Fatalf("新增应用 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", "/api/projects/proj/apps/app", c); !strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("页面应能看到配置文件内容：%s", rec.Body)
	}
	for _, path := range []string{"/api/projects/proj/apps/app", "/api/projects/proj", "/api/apps"} {
		if rec := e.call(t, "GET", path, "", admin); rec.Code != 200 || strings.Contains(rec.Body.String(), "hunter2") {
			t.Fatalf("令牌读 %s 不应看到配置文件内容：%d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := e.call(t, "GET", "/api/projects/proj/apps/app/revisions", "", admin); rec.Code != 403 {
		t.Fatalf("修改记录里有敏感内容，令牌不能读：%d", rec.Code)
	}

	// 令牌：读出来改个名称再提交
	rec := e.call(t, "GET", "/api/projects/proj/apps/app", "", admin)
	var app struct {
		Params   map[string]any `json:"params"`
		Revision int            `json:"revision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	app.Params["title"] = "改过的名称"
	body, _ := json.Marshal(map[string]any{"params": app.Params, "revision": app.Revision})
	rec = e.call(t, "PUT", "/api/projects/proj/apps/app", string(body), admin)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "hunter2") || !strings.Contains(rec.Body.String(), "改过的名称") {
		t.Fatalf("令牌修改应用 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", "/api/projects/proj/apps/app", c); !strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), `"content":"******"`) {
		t.Fatalf("令牌修改后配置文件内容应保留：%s", rec.Body)
	}
}

func TestTokensAreIsolatedByAccount(t *testing.T) {
	e := newEnv(t, nil)
	alice, bob := e.loginAs(t, "alice", true), e.loginAs(t, "bob", false)
	aliceToken := e.newToken(t, alice, "ci", "admin")
	e.newToken(t, bob, "ci", "read") // 不同账号可以用同样的名称
	if rec := e.send(t, "POST", "/api/projects", `{"name":"bob-only","title":"bob 的"}`, bob); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	if rec := e.call(t, "GET", "/api/projects", "", aliceToken); strings.Contains(rec.Body.String(), "bob-only") {
		t.Fatalf("alice 的令牌看到了 bob 的项目：%s", rec.Body)
	}
	if rec := e.call(t, "GET", "/api/projects/bob-only", "", aliceToken); rec.Code != 404 {
		t.Fatalf("应 404，实际 %d", rec.Code)
	}
	if rec := e.do(t, "GET", "/api/tokens", alice); strings.Count(rec.Body.String(), `"name"`) != 1 {
		t.Fatalf("alice 只应看到自己的令牌：%s", rec.Body)
	}
	// 账号停用后令牌不能用，启用后恢复；账号删除后令牌一起没了
	bobToken := e.newToken(t, bob, "second", "read")
	if rec := e.send(t, "PUT", "/api/users/bob", `{"disabled":true}`, alice); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if rec := e.call(t, "GET", "/api/projects", "", bobToken); rec.Code != 401 {
		t.Fatalf("账号停用后令牌应失效，实际 %d", rec.Code)
	}
	if rec := e.send(t, "PUT", "/api/users/bob", `{"disabled":false}`, alice); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if rec := e.call(t, "GET", "/api/projects", "", bobToken); rec.Code != 200 {
		t.Fatalf("账号启用后令牌应恢复，实际 %d", rec.Code)
	}
	if rec := e.send(t, "DELETE", "/api/users/bob", ``, alice); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if rec := e.call(t, "GET", "/api/projects", "", bobToken); rec.Code != 401 {
		t.Fatalf("账号删除后令牌应失效，实际 %d", rec.Code)
	}
}

func TestOpenAPI(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)
	if rec := e.do(t, "GET", "/api/openapi.json", nil); rec.Code != 401 {
		t.Fatalf("不登录 = %d", rec.Code)
	}
	rec := e.do(t, "GET", "/api/openapi.json", c)
	var doc struct {
		OpenAPI string                               `json:"openapi"`
		Paths   map[string]map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); rec.Code != 200 || err != nil || doc.OpenAPI != "3.1.0" {
		t.Fatalf("接口文档 = %d %v", rec.Code, err)
	}
	// 路由表里的每个接口都在文档里
	n := 0
	for _, rt := range (&server{}).routes() {
		op := doc.Paths[rt.path][strings.ToLower(rt.method)]
		if op == nil || op["summary"] != rt.summary {
			t.Errorf("文档里没有 %s %s", rt.method, rt.path)
		}
		n++
	}
	if got := doc.Paths["/api/deploys"]["post"]["x-access"]; got != "deploy" {
		t.Errorf("部署接口的 x-access = %v", got)
	}
	if got := doc.Paths["/api/secrets/{name}/reveal"]["post"]["x-access"]; got != "page" {
		t.Errorf("读取密钥明文的 x-access = %v", got)
	}
	if n < 60 {
		t.Errorf("接口数量 %d，比预期少", n)
	}
}

// hostTransport 让测试服务器收到的 Host 是平台的地址，并带上访问令牌。
type hostTransport struct{ token string }

func (h hostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = testHost
	if h.token != "" {
		r.Header.Set("Authorization", "Bearer "+h.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (e *env) mcp(t *testing.T, url, token string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: hostTransport{token}}, DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("连接 MCP：%v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func toolText(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestMCP(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	ctx := context.Background()

	// 没有令牌、令牌不对：连不上
	for _, token := range []string{"", "dh_wrong"} {
		client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
		if cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp",
			HTTPClient: &http.Client{Transport: hostTransport{token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil); err == nil {
			cs.Close()
			t.Fatalf("令牌 %q 不应能连上 MCP", token)
		}
	}
	// 会话 Cookie 不能用来调 MCP
	req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(`{}`))
	req.Host = testHost
	req.Header.Set("Origin", "http://"+testHost)
	req.AddCookie(c)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Fatalf("用 Cookie 调 MCP 应 401：%v %v", resp, err)
	}

	// 工具列表随权限级别变化
	want := map[string]int{"read": 10, "deploy": 13, "admin": 15}
	tokens := map[string]string{}
	for level, n := range want {
		tokens[level] = e.newToken(t, c, "mcp-"+level, level)
		res, err := e.mcp(t, srv.URL, tokens[level]).ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Tools) != n {
			t.Errorf("%s 令牌有 %d 个工具，期望 %d", level, len(res.Tools), n)
		}
		for _, tool := range res.Tools {
			if tool.Description == "" || tool.Annotations == nil {
				t.Errorf("工具 %s 缺少说明或标注", tool.Name)
			}
			if readOnlyTool := tool.Annotations.ReadOnlyHint; readOnlyTool == (tool.Name == "deploy" || tool.Name == "rollback" || tool.Name == "cancel_job" || tool.Name == "create_app" || tool.Name == "update_app") {
				t.Errorf("工具 %s 的只读标注不对", tool.Name)
			}
		}
	}

	// 准备一个应用
	for _, r := range [][2]string{
		{"/api/servers", `{"name":"srv","host":"1.2.3.4"}`},
		{"/api/projects", `{"name":"proj","title":"项目"}`},
		{"/api/projects/proj/apps", `{"driver":"gobinary","params":{"name":"app","title":"应用","repo_path":"` + t.TempDir() + `","branch":"main","server":"srv","main":"./cmd/app",` +
			`"config_files":[{"name":"c.toml","source":"platform","content":"password=hunter2"}]}}`},
	} {
		if rec := e.send(t, "POST", r[0], r[1], c); rec.Code != 201 {
			t.Fatalf("%s = %d %s", r[0], rec.Code, rec.Body)
		}
	}

	read := e.mcp(t, srv.URL, tokens["read"])
	res, err := read.CallTool(ctx, &sdk.CallToolParams{Name: "list_apps", Arguments: map[string]any{"with_status": false}})
	if err != nil || res.IsError || !strings.Contains(toolText(t, res), `"app":"proj/app"`) {
		t.Fatalf("list_apps = %v %v", toolText(t, res), err)
	}
	res, err = read.CallTool(ctx, &sdk.CallToolParams{Name: "get_app", Arguments: map[string]any{"app": "proj/app"}})
	if err != nil || res.IsError || strings.Contains(toolText(t, res), "hunter2") || !strings.Contains(toolText(t, res), `"revision":1`) {
		t.Fatalf("get_app 不应返回配置文件内容：%v %v", toolText(t, res), err)
	}
	res, err = read.CallTool(ctx, &sdk.CallToolParams{Name: "list_drivers", Arguments: map[string]any{"driver": "gobinary"}})
	if err != nil || res.IsError || !strings.Contains(toolText(t, res), `"key":"repo_source"`) || !strings.Contains(toolText(t, res), `"key":"main"`) {
		t.Fatalf("list_drivers 应返回通用参数和部署方式参数：%v", err)
	}
	// 校验错误按字段说明
	res, err = read.CallTool(ctx, &sdk.CallToolParams{Name: "validate_app", Arguments: map[string]any{"project": "proj", "driver": "gobinary", "params": map[string]any{"name": "Bad"}}})
	if err != nil || !res.IsError || !strings.Contains(toolText(t, res), "name：") {
		t.Fatalf("validate_app 应按字段报错：%v %v", toolText(t, res), err)
	}
	// 只读令牌调不了部署（工具不在列表里）
	if res, err := read.CallTool(ctx, &sdk.CallToolParams{Name: "deploy", Arguments: map[string]any{"apps": []string{"proj/app"}}}); err == nil && !res.IsError {
		t.Fatal("只读令牌不应能调用 deploy")
	}

	// 可部署令牌发起部署：应用默认要审批，任务待审批、没有执行
	dep := e.mcp(t, srv.URL, tokens["deploy"])
	res, err = dep.CallTool(ctx, &sdk.CallToolParams{Name: "deploy", Arguments: map[string]any{"apps": []string{"proj/app"}, "idempotency_key": "k1"}})
	text := toolText(t, res)
	if err != nil || res.IsError || !strings.Contains(text, `"status":"pending_approval"`) || !strings.Contains(text, `"initiator":"token:mcp-deploy"`) {
		t.Fatalf("deploy = %v %v", text, err)
	}
	// 同一个幂等键再调用：还是那个任务
	res, _ = dep.CallTool(ctx, &sdk.CallToolParams{Name: "deploy", Arguments: map[string]any{"apps": []string{"proj/app"}, "idempotency_key": "k1"}})
	if !strings.Contains(toolText(t, res), `"id":1,`) {
		t.Fatalf("幂等键相同应返回原来的任务：%s", toolText(t, res))
	}
	// 不带幂等键再发起：已经有待审批的了
	res, _ = dep.CallTool(ctx, &sdk.CallToolParams{Name: "deploy", Arguments: map[string]any{"apps": []string{"proj/app"}}})
	if !res.IsError || !strings.Contains(toolText(t, res), "待审批") {
		t.Fatalf("重复发起应提示已有待审批的任务：%s", toolText(t, res))
	}
	res, _ = dep.CallTool(ctx, &sdk.CallToolParams{Name: "wait_job", Arguments: map[string]any{"job_id": 1, "timeout_seconds": 5}})
	if !strings.Contains(toolText(t, res), "审批") || !strings.Contains(toolText(t, res), `"finished":false`) {
		t.Fatalf("wait_job 应立刻返回并说明在等审批：%s", toolText(t, res))
	}
	// 页面上能看到待审批的任务，拒绝后任务结束
	if rec := e.do(t, "GET", "/api/jobs?status=pending_approval", c); !strings.Contains(rec.Body.String(), `"id":1`) {
		t.Fatalf("待审批列表 = %s", rec.Body)
	}
	if rec := e.send(t, "POST", "/api/jobs/1/reject", ``, c); rec.Code != 200 {
		t.Fatalf("拒绝 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/jobs/1/approve", ``, c); rec.Code != 409 {
		t.Fatalf("已拒绝的任务不能再批准：%d", rec.Code)
	}
	res, _ = dep.CallTool(ctx, &sdk.CallToolParams{Name: "get_job", Arguments: map[string]any{"job_id": 1}})
	if !strings.Contains(toolText(t, res), `"status":"rejected"`) {
		t.Fatalf("get_job = %s", toolText(t, res))
	}
	res, _ = dep.CallTool(ctx, &sdk.CallToolParams{Name: "get_job_logs", Arguments: map[string]any{"job_id": 1}})
	if !strings.Contains(toolText(t, res), "拒绝") {
		t.Fatalf("日志里应有拒绝的说明：%s", toolText(t, res))
	}

	// 管理令牌修改应用：敏感内容保留
	adm := e.mcp(t, srv.URL, tokens["admin"])
	res, err = adm.CallTool(ctx, &sdk.CallToolParams{Name: "update_app", Arguments: map[string]any{"app": "proj/app", "revision": 1, "params": map[string]any{
		"name": "app", "title": "MCP 改的", "repo_path": t.TempDir(), "branch": "main", "server": "srv", "main": "./cmd/app",
		"config_files": []map[string]any{{"name": "c.toml", "source": "platform", "content": "******"}}}}})
	if err != nil || res.IsError || strings.Contains(toolText(t, res), "hunter2") {
		t.Fatalf("update_app = %v %v", toolText(t, res), err)
	}
	if rec := e.do(t, "GET", "/api/projects/proj/apps/app", c); !strings.Contains(rec.Body.String(), "password=hunter2") || !strings.Contains(rec.Body.String(), "MCP 改的") {
		t.Fatalf("MCP 修改后配置文件内容应保留：%s", rec.Body)
	}

	// 令牌吊销后，已经连上的客户端也立刻不能用
	if rec := e.send(t, "DELETE", "/api/tokens/mcp-read", ``, c); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if res, err := read.CallTool(ctx, &sdk.CallToolParams{Name: "list_jobs", Arguments: map[string]any{}}); err == nil && !res.IsError {
		t.Fatal("令牌吊销后 MCP 调用应失败")
	}
}
