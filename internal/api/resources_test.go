package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func (e *env) send(t *testing.T, method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = testHost
	req.Header.Set("Origin", "http://"+testHost)
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestResourcesFlow(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)

	if rec := e.send(t, "POST", "/api/servers", `{"name":"shop-main","host":"1.2.3.4"}`, nil); rec.Code != 401 {
		t.Fatalf("未登录应 401，实际 %d", rec.Code)
	}
	if rec := e.send(t, "POST", "/api/servers", `{"name":"shop-main","host":"1.2.3.4"}`, c); rec.Code != 201 {
		t.Fatalf("新增服务器 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/servers", `{"name":"shop-main","host":"1.2.3.4"}`, c); rec.Code != 409 {
		t.Fatalf("重名应 409，实际 %d", rec.Code)
	}
	if rec := e.send(t, "POST", "/api/servers", `{"name":"x1","host":"h","unknown":1}`, c); rec.Code != 400 {
		t.Fatalf("不认识的字段应 400，实际 %d", rec.Code)
	}
	rec := e.send(t, "POST", "/api/servers/shop-main/test", ``, c)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"arch":"arm64"`) {
		t.Fatalf("测试连接 = %d %s", rec.Code, rec.Body)
	}

	if rec := e.send(t, "POST", "/api/projects", `{"name":"shop","title":"示例商城"}`, c); rec.Code != 201 {
		t.Fatalf("新增项目 = %d %s", rec.Code, rec.Body)
	}

	// 参数有误：422，按字段返回
	rec = e.send(t, "POST", "/api/projects/shop/apps", `{"driver":"gobinary","params":{"name":"Bad"}}`, c)
	if rec.Code != 422 {
		t.Fatalf("参数有误应 422，实际 %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Error errorBody `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	fields := map[string]bool{}
	for _, f := range body.Error.Fields {
		fields[f.Field] = true
	}
	for _, f := range []string{"name", "title", "repo_path", "branch", "main"} {
		if !fields[f] {
			t.Errorf("应返回 %s 的错误：%+v", f, body.Error.Fields)
		}
	}

	dir := t.TempDir()
	params := `{"name":"open-api","title":"开放 API","repo_path":"` + dir + `","branch":"main","server":"shop-main","main":"./cmd/open-api"}`
	if rec := e.send(t, "POST", "/api/projects/shop/apps/validate", `{"driver":"gobinary","params":`+params+`}`, c); rec.Code != 200 {
		t.Fatalf("校验 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/projects/shop/apps", `{"driver":"gobinary","params":`+params+`}`, c); rec.Code != 201 {
		t.Fatalf("新增应用 = %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "GET", "/api/projects/shop", c)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"open-api"`) {
		t.Fatalf("项目详情 = %d %s", rec.Code, rec.Body)
	}
	rec = e.send(t, "POST", "/api/projects/shop/apps/open-api/preview", `{}`, c)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "go build") {
		t.Fatalf("预览 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", "/api/projects/shop/apps/nope", c); rec.Code != 404 {
		t.Fatalf("不存在的应用应 404，实际 %d", rec.Code)
	}

	// 密钥：明文只能用 POST reveal 读取
	if rec := e.send(t, "POST", "/api/secrets", `{"name":"API_URL","value":"https://x"}`, c); rec.Code != 201 {
		t.Fatalf("新增密钥 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", "/api/secrets", c); strings.Contains(rec.Body.String(), "https://x") {
		t.Fatal("密钥列表不应返回值")
	}
	rec = e.send(t, "POST", "/api/secrets/API_URL/reveal", ``, c)
	if !strings.Contains(rec.Body.String(), `"value":"https://x"`) {
		t.Fatalf("reveal = %s", rec.Body)
	}
	// 跨站请求读取明文被拒绝
	req := httptest.NewRequest("POST", "/api/secrets/API_URL/reveal", nil)
	req.Host = testHost
	req.Header.Set("Origin", "https://evil.example.com")
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("跨站读取密钥应 403，实际 %d", rr.Code)
	}

	if rec := e.send(t, "DELETE", "/api/servers/shop-main", ``, c); rec.Code != 409 {
		t.Fatalf("有应用的服务器删除应 409，实际 %d", rec.Code)
	}
}

func TestCredentialRoutes(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)
	const token = "secret-token-should-not-leak"
	body := `{"name":"cf-main","kind":"cloudflare","title":"主账号","values":{"account_id":"0123456789abcdef0123456789abcdef","api_token":"` + token + `"}}`
	if rec := e.send(t, "POST", "/api/credentials", body, c); rec.Code != 201 || strings.Contains(rec.Body.String(), token) {
		t.Fatalf("新增凭据 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "POST", "/api/credentials", `{"name":"x1","kind":"cloudflare","values":{}}`, c); rec.Code != 422 || !strings.Contains(rec.Body.String(), `"account_id"`) {
		t.Fatalf("缺字段应 422 并按字段返回：%d %s", rec.Code, rec.Body)
	}
	rec := e.send(t, "GET", "/api/credentials", ``, c)
	var list []struct {
		Name    string   `json:"name"`
		Secrets []string `json:"secrets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || len(list[0].Secrets) != 1 || strings.Contains(rec.Body.String(), token) {
		t.Fatalf("凭据列表 = %s %v", rec.Body, err)
	}
	if rec := e.send(t, "PUT", "/api/credentials/cf-main", `{"title":"改名","values":{"account_id":"0123456789abcdef0123456789abcdef"}}`, c); rec.Code != 200 {
		t.Fatalf("修改凭据（Token 留空不改）= %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "DELETE", "/api/credentials/cf-main", ``, c); rec.Code != 200 {
		t.Fatalf("删除凭据 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(t, "GET", "/api/credentials/cf-main", ``, c); rec.Code != 404 {
		t.Fatalf("删除后应 404，实际 %d", rec.Code)
	}
}
