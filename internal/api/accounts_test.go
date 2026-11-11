package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupCreatesFirstAdmin(t *testing.T) {
	e := newEnv(t, nil)
	tok, err := e.auth.NewSetupToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 填错了不消耗令牌
	if rec := e.send(t, "POST", "/api/setup", `{"token":"`+tok+`","username":"admin","password":"short"}`, nil); rec.Code != 422 {
		t.Fatalf("密码太短应 422，实际 %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/setup", `{"token":"wrong","username":"admin","password":"`+testPassword+`"}`, nil); errorCode(rec) != "setup_expired" {
		t.Fatalf("令牌不对应拒绝：%d %s", rec.Code, rec.Body)
	}
	rec := e.send(t, "POST", "/api/setup", `{"token":"`+tok+`","username":"Admin","password":"`+testPassword+`"}`, nil)
	if rec.Code != 201 {
		t.Fatalf("创建管理员 = %d %s", rec.Code, rec.Body)
	}
	c := sessionCookie(t, rec)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("会话 Cookie 必须 HttpOnly 且 SameSite=Strict")
	}
	if rec := e.do(t, "GET", "/api/session", c); !strings.Contains(rec.Body.String(), `"username":"admin"`) || !strings.Contains(rec.Body.String(), `"admin":true`) {
		t.Fatalf("创建后应以管理员登录：%s", rec.Body)
	}
	// 有账号之后不能再创建
	tok2, _ := e.auth.NewSetupToken(context.Background())
	if rec := e.send(t, "POST", "/api/setup", `{"token":"`+tok2+`","username":"evil","password":"`+testPassword+`"}`, nil); rec.Code != 409 {
		t.Fatalf("已有账号时应 409，实际 %d %s", rec.Code, rec.Body)
	}
}

func TestLoginLogout(t *testing.T) {
	e := newEnv(t, nil)
	e.addUser(t, "alice", false)

	if rec := e.send(t, "POST", "/api/login", `{"username":"alice","password":"wrong-password"}`, nil); rec.Code != 401 || errorCode(rec) != "invalid_credentials" {
		t.Fatalf("密码不对 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/login", `{"username":"nobody","password":"wrong-password"}`, nil); rec.Code != 401 || errorCode(rec) != "invalid_credentials" {
		t.Fatalf("用户名不存在应和密码不对一样：%d %s", rec.Code, rec.Body)
	}
	// 用户名不区分大小写
	rec := e.send(t, "POST", "/api/login", `{"username":" Alice ","password":"`+testPassword+`"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("登录 = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "pbkdf2") {
		t.Fatal("响应里不应出现密码哈希")
	}
	c := sessionCookie(t, rec)
	if rec := e.do(t, "GET", "/api/session", c); rec.Code != 200 {
		t.Fatalf("登录后读取会话 = %d", rec.Code)
	}
	if rec := e.send(t, "POST", "/api/logout", ``, c); rec.Code != 200 {
		t.Fatalf("退出 = %d", rec.Code)
	}
	if rec := e.do(t, "GET", "/api/session", c); rec.Code != 401 {
		t.Fatalf("退出后旧会话应失效，实际 %d", rec.Code)
	}
}

func TestLoginLockout(t *testing.T) {
	e := newEnv(t, nil)
	e.addUser(t, "alice", false)
	for i := 0; i < 5; i++ {
		if rec := e.send(t, "POST", "/api/login", `{"username":"alice","password":"wrong-password"}`, nil); rec.Code != 401 {
			t.Fatalf("第 %d 次 = %d", i+1, rec.Code)
		}
	}
	// 输错 5 次后，密码对了也要等
	rec := e.send(t, "POST", "/api/login", `{"username":"alice","password":"`+testPassword+`"}`, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("应暂时拒绝，实际 %d %s", rec.Code, rec.Body)
	}
}

func TestLoginNeedsOrigin(t *testing.T) {
	e := newEnv(t, nil)
	e.addUser(t, "alice", false)
	req := e.do(t, "POST", "/api/login", nil) // 没有 Origin
	if req.Code != http.StatusForbidden {
		t.Fatalf("没有 Origin 的登录请求应 403，实际 %d", req.Code)
	}
}

// 两个账号各自的项目、服务器、密钥、任务互相看不到，名称可以相同。
func TestAccountsAreIsolated(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.loginAs(t, "alice", true) // 管理员也看不到别人的数据
	bob := e.loginAs(t, "bob", false)

	for _, c := range []*http.Cookie{alice, bob} {
		if rec := e.send(t, "POST", "/api/servers", `{"name":"main","host":"1.2.3.4"}`, c); rec.Code != 201 {
			t.Fatalf("新增服务器 = %d %s", rec.Code, rec.Body)
		}
		if rec := e.send(t, "POST", "/api/projects", `{"name":"shop","title":"商城"}`, c); rec.Code != 201 {
			t.Fatalf("新增项目 = %d %s", rec.Code, rec.Body)
		}
	}
	if rec := e.send(t, "POST", "/api/secrets", `{"name":"API_URL","value":"https://alice.example"}`, alice); rec.Code != 201 {
		t.Fatalf("新增密钥 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/projects", `{"name":"alice-only","title":"只有 alice 有"}`, alice); rec.Code != 201 {
		t.Fatalf("新增项目 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "PUT", "/api/projects/shop", `{"title":"alice 的商城"}`, alice); rec.Code != 200 {
		t.Fatalf("修改项目 = %d %s", rec.Code, rec.Body)
	}

	// bob 的列表里没有 alice 的数据，自己的同名项目没被改
	rec := e.do(t, "GET", "/api/projects", bob)
	if body := rec.Body.String(); strings.Contains(body, "alice") || !strings.Contains(body, `"title":"商城"`) {
		t.Fatalf("bob 的项目列表不对：%s", body)
	}
	if rec := e.do(t, "GET", "/api/secrets", bob); strings.Contains(rec.Body.String(), "API_URL") {
		t.Fatalf("bob 看到了 alice 的密钥：%s", rec.Body)
	}
	// 直接按名称访问也是 404
	for _, target := range []string{"/api/projects/alice-only", "/api/projects/alice-only/apps/x", "/api/projects/alice-only/apps/x/repo"} {
		if rec := e.do(t, "GET", target, bob); rec.Code != 404 {
			t.Fatalf("%s 应 404，实际 %d %s", target, rec.Code, rec.Body)
		}
	}
	if rec := e.send(t, "POST", "/api/secrets/API_URL/reveal", ``, bob); rec.Code != 404 {
		t.Fatalf("读别人的密钥应 404，实际 %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "DELETE", "/api/projects/alice-only", ``, bob); rec.Code != 404 {
		t.Fatalf("删别人的项目应 404，实际 %d", rec.Code)
	}
	if rec := e.send(t, "PUT", "/api/secrets/API_URL", `{"value":"https://evil.example"}`, bob); rec.Code != 404 {
		t.Fatalf("改别人的密钥应 404，实际 %d", rec.Code)
	}
	if rec := e.send(t, "POST", "/api/secrets/API_URL/reveal", ``, alice); !strings.Contains(rec.Body.String(), "https://alice.example") {
		t.Fatalf("alice 的密钥被改了：%s", rec.Body)
	}

	// 访问令牌：bob 查看不了 alice 的
	aliceToken := e.newToken(t, alice, "ci", "read")
	if rec := e.send(t, "POST", "/api/tokens/ci/reveal", ``, bob); rec.Code != 404 {
		t.Fatalf("查看别人的令牌应 404，实际 %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/tokens/ci/reveal", ``, alice); !strings.Contains(rec.Body.String(), aliceToken) {
		t.Fatalf("alice 查看自己的令牌：%s", rec.Body)
	}

	// 导出只有自己的
	if rec := e.send(t, "POST", "/api/export", `{"sensitive":true}`, bob); rec.Code != 200 || strings.Contains(rec.Body.String(), "alice") {
		t.Fatalf("bob 的导出里有 alice 的数据：%d %s", rec.Code, rec.Body)
	}

	// 任务：alice 的任务 bob 读不到
	ctx := context.Background()
	u, _ := e.st.GetUser(ctx, "alice")
	tn, _ := e.tenants.Get(ctx, u.ID)
	if _, err := tn.Core.ImportHistory(ctx, "shop", writeHistory(t)); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "GET", "/api/jobs", alice); !strings.Contains(rec.Body.String(), `"id":1`) {
		t.Fatalf("alice 应该能看到导入的任务：%s", rec.Body)
	}
	if rec := e.do(t, "GET", "/api/jobs", bob); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("bob 看到了 alice 的任务：%s", rec.Body)
	}
	if rec := e.do(t, "GET", "/api/jobs/1", bob); rec.Code != 404 {
		t.Fatalf("读别人的任务应 404，实际 %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", "/api/jobs/1/events", bob); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("bob 读到了 alice 的任务日志：%s", rec.Body)
	}
	if rec := e.send(t, "POST", "/api/jobs/1/cancel", ``, bob); rec.Code != 409 {
		t.Fatalf("取消别人的任务应失败，实际 %d", rec.Code)
	}
}

func TestUserManagement(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.loginAs(t, "admin", true)
	member := e.loginAs(t, "member", false)

	// 普通账号不能管理账号
	if rec := e.do(t, "GET", "/api/users", member); rec.Code != 403 {
		t.Fatalf("普通账号读账号列表应 403，实际 %d", rec.Code)
	}
	if rec := e.send(t, "POST", "/api/users", `{"username":"x1","password":"`+testPassword+`","admin":true}`, member); rec.Code != 403 {
		t.Fatalf("普通账号新建账号应 403，实际 %d", rec.Code)
	}

	if rec := e.send(t, "POST", "/api/users", `{"username":"Bad Name","password":"short"}`, admin); rec.Code != 422 || !strings.Contains(rec.Body.String(), `"username"`) || !strings.Contains(rec.Body.String(), `"password"`) {
		t.Fatalf("格式不对应按字段报错：%d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/users", `{"username":"member","password":"`+testPassword+`"}`, admin); rec.Code != 422 {
		t.Fatalf("重名应 422，实际 %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/users", `{"username":"carol","password":"`+testPassword+`"}`, admin); rec.Code != 201 || strings.Contains(rec.Body.String(), "pbkdf2") {
		t.Fatalf("新建账号 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", "/api/users", admin); !strings.Contains(rec.Body.String(), `"username":"carol"`) || strings.Contains(rec.Body.String(), "pbkdf2") {
		t.Fatalf("账号列表不对：%s", rec.Body)
	}

	// 不能停用、降级、删除自己
	for _, body := range []string{`{"disabled":true}`, `{"admin":false}`} {
		if rec := e.send(t, "PUT", "/api/users/admin", body, admin); rec.Code != 409 {
			t.Fatalf("对自己 %s 应 409，实际 %d %s", body, rec.Code, rec.Body)
		}
	}
	if rec := e.send(t, "DELETE", "/api/users/admin", ``, admin); rec.Code != 409 {
		t.Fatalf("删除自己应 409，实际 %d", rec.Code)
	}

	// 重置密码后，那个账号已登录的页面失效，新密码可以登录
	if rec := e.send(t, "PUT", "/api/users/member", `{"password":"brand-new-pass"}`, admin); rec.Code != 200 {
		t.Fatalf("重置密码 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", "/api/session", member); rec.Code != 401 {
		t.Fatalf("重置密码后旧会话应失效，实际 %d", rec.Code)
	}
	rec := e.send(t, "POST", "/api/login", `{"username":"member","password":"brand-new-pass"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("用新密码登录 = %d %s", rec.Code, rec.Body)
	}
	member = sessionCookie(t, rec)

	// 停用后不能登录，已登录的页面失效；启用后恢复
	if rec := e.send(t, "PUT", "/api/users/member", `{"disabled":true}`, admin); rec.Code != 200 {
		t.Fatalf("停用 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", "/api/session", member); rec.Code != 401 {
		t.Fatalf("停用后会话应失效，实际 %d", rec.Code)
	}
	if rec := e.send(t, "POST", "/api/login", `{"username":"member","password":"brand-new-pass"}`, nil); rec.Code != 401 {
		t.Fatalf("停用后不能登录，实际 %d", rec.Code)
	}
	if rec := e.send(t, "PUT", "/api/users/member", `{"disabled":false}`, admin); rec.Code != 200 {
		t.Fatalf("启用 = %d %s", rec.Code, rec.Body)
	}
	rec = e.send(t, "POST", "/api/login", `{"username":"member","password":"brand-new-pass"}`, nil)
	member = sessionCookie(t, rec)

	// 删除账号连同它的数据
	if rec := e.send(t, "POST", "/api/projects", `{"name":"gone","title":"会被删掉"}`, member); rec.Code != 201 {
		t.Fatalf("新增项目 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "DELETE", "/api/users/member", ``, admin); rec.Code != 200 {
		t.Fatalf("删除账号 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", "/api/projects", member); rec.Code != 401 {
		t.Fatalf("账号删除后会话应失效，实际 %d", rec.Code)
	}
	// 同名账号重新创建后是全新的，不会接手原来的数据
	if rec := e.send(t, "POST", "/api/users", `{"username":"member","password":"`+testPassword+`"}`, admin); rec.Code != 201 {
		t.Fatalf("重新创建 = %d %s", rec.Code, rec.Body)
	}
	rec = e.send(t, "POST", "/api/login", `{"username":"member","password":"`+testPassword+`"}`, nil)
	if rec := e.do(t, "GET", "/api/projects", sessionCookie(t, rec)); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("新账号不应有数据：%s", rec.Body)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	e := newEnv(t, nil)
	e.addUser(t, "alice", false)
	first := sessionCookie(t, e.send(t, "POST", "/api/login", `{"username":"alice","password":"`+testPassword+`"}`, nil))
	second := sessionCookie(t, e.send(t, "POST", "/api/login", `{"username":"alice","password":"`+testPassword+`"}`, nil))

	if rec := e.send(t, "POST", "/api/account/password", `{"current":"wrong-password","password":"another-pass-1"}`, first); rec.Code != 422 || !strings.Contains(rec.Body.String(), `"current"`) {
		t.Fatalf("当前密码不对 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/account/password", `{"current":"`+testPassword+`","password":"another-pass-1"}`, first); rec.Code != 200 {
		t.Fatalf("改密码 = %d %s", rec.Code, rec.Body)
	}
	// 当前页面保持登录，其他设备上的会话失效
	if rec := e.do(t, "GET", "/api/session", first); rec.Code != 200 {
		t.Fatalf("改密码的页面应保持登录，实际 %d", rec.Code)
	}
	if rec := e.do(t, "GET", "/api/session", second); rec.Code != 401 {
		t.Fatalf("其他会话应失效，实际 %d", rec.Code)
	}
	if rec := e.send(t, "POST", "/api/login", `{"username":"alice","password":"`+testPassword+`"}`, nil); rec.Code != 401 {
		t.Fatalf("旧密码不能再登录，实际 %d", rec.Code)
	}
}

// writeHistory 写一份旧工具的部署历史（一条记录），返回文件路径。
func writeHistory(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	line := `{"unit": "web", "action": "deploy", "version": "v1", "previous": "", "result": "success", "server": "main", "operator": "alice", "time": "2026-11-07 02:12:00", "error": ""}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
