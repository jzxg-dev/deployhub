package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"deployhub/internal/auth"
	"deployhub/internal/core"
	"deployhub/internal/driver/builtin"
	"deployhub/internal/events"
	"deployhub/internal/remote"
	"deployhub/internal/store"
	"deployhub/internal/tenant"
	"deployhub/internal/vault"
)

type fakeSSH struct{ out string }

func (f *fakeSSH) Run(context.Context, remote.Target, string) (*remote.Result, error) {
	return &remote.Result{Stdout: f.out}, nil
}

func (f *fakeSSH) Stream(context.Context, remote.Target, string, func(string)) error { return nil }
func (f *fakeSSH) CopyFiles(context.Context, remote.Target, []string, string, func(string)) error {
	return nil
}
func (f *fakeSSH) Rsync(context.Context, remote.Target, string, string, []string, func(string)) error {
	return nil
}

const testHost = "127.0.0.1:7788"

// 测试账号的密码；迭代次数多，哈希只算一次，各测试共用。
const testPassword = "correct-horse"

var testPasswordHash = func() string {
	h, err := auth.HashPassword(testPassword)
	if err != nil {
		panic(err)
	}
	return h
}()

type env struct {
	h        http.Handler
	auth     *auth.Auth
	st       *store.Store
	accounts *core.Accounts
	tenants  *tenant.Manager
}

func newEnv(t *testing.T, ui fstest.MapFS) *env {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "deployhub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := auth.New(st, false)
	g, err := auth.NewGuard(testHost)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	vt, _ := vault.New(bytes.Repeat([]byte{3}, 32))
	accounts := core.NewAccounts(st)
	tenants := tenant.NewManager(tenant.Options{
		Store: st, Vault: vt, Drivers: reg, Runner: &fakeSSH{out: "machine=aarch64\nuser=deploy\n"}, SSH: &fakeSSH{}, DataDir: t.TempDir(),
	})
	t.Cleanup(tenants.Wait)
	if ui == nil {
		ui = fstest.MapFS{
			"index.html":      {Data: []byte("<html>app</html>")},
			"assets/app-1.js": {Data: []byte("console.log(1)")},
			"favicon.svg":     {Data: []byte("<svg/>")},
		}
	}
	h := NewHandler(Options{
		Version: "test", InstanceID: "inst", Auth: a, Guard: g, Accounts: accounts, Tenants: tenants, Drivers: reg, UI: ui,
		PingInterval: 20 * time.Millisecond,
	})
	return &env{h: h, auth: a, st: st, accounts: accounts, tenants: tenants}
}

// addUser 直接在库里建一个密码为 testPassword 的账号。
func (e *env) addUser(t *testing.T, username string, admin bool) *store.UserRow {
	t.Helper()
	ctx := context.Background()
	if err := e.st.CreateUser(ctx, username, testPasswordHash, admin); err != nil {
		t.Fatal(err)
	}
	u, err := e.st.GetUser(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// loginAs 建一个账号并给它一个会话（不走密码校验）。
func (e *env) loginAs(t *testing.T, username string, admin bool) *http.Cookie {
	t.Helper()
	u := e.addUser(t, username, admin)
	c, err := e.auth.StartSession(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// hub 返回账号的事件通道。
func (e *env) hub(t *testing.T, username string) *events.Hub {
	t.Helper()
	u, err := e.st.GetUser(context.Background(), username)
	if err != nil {
		t.Fatal(err)
	}
	tn, err := e.tenants.Get(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tn.Hub
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName && c.Value != "" {
			return c
		}
	}
	t.Fatalf("没有写入会话 Cookie：%d %s", rec.Code, rec.Body.String())
	return nil
}

func errorCode(rec *httptest.ResponseRecorder) string {
	var body struct {
		Error errorBody `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

func (e *env) do(t *testing.T, method, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = testHost
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// login 用默认的管理员账号登录。
func (e *env) login(t *testing.T) *http.Cookie {
	t.Helper()
	return e.loginAs(t, "admin", true)
}

func TestHealthNeedsNoLogin(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do(t, http.MethodGet, "/api/health", nil)
	if rec.Code != 200 {
		t.Fatalf("状态码 %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["app"] != "deployhub" || body["instance"] != "inst" {
		t.Fatalf("返回 %v", body)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("缺少 Referrer-Policy")
	}
}

func TestDriversRequiresLogin(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do(t, http.MethodGet, "/api/drivers", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录时状态码 %d，期望 401", rec.Code)
	}
	var body struct {
		Error errorBody `json:"error"`
	}
	// 还没有账号时提示先创建管理员
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "setup_required" {
		t.Fatalf("错误格式不对：%s", rec.Body.String())
	}

	c := e.login(t)
	if rec := e.do(t, http.MethodGet, "/api/drivers", nil); errorCode(rec) != "unauthorized" {
		t.Fatalf("有账号后未登录应返回 unauthorized：%s", rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/api/drivers", c)
	if rec.Code != 200 {
		t.Fatalf("登录后状态码 %d", rec.Code)
	}
	var cat struct {
		Sections []any `json:"sections"`
		Common   []any `json:"common"`
		Drivers  []struct {
			Name  string `json:"name"`
			Steps []any  `json:"steps"`
		} `json:"drivers"`
		CredentialKinds []struct {
			Name string `json:"name"`
		} `json:"credentialKinds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	if len(cat.Drivers) != 4 || len(cat.Common) == 0 || len(cat.Sections) == 0 || len(cat.CredentialKinds) == 0 {
		t.Fatalf("返回内容不完整：%d 种部署方式，%d 种凭据", len(cat.Drivers), len(cat.CredentialKinds))
	}
	for _, d := range cat.Drivers {
		if len(d.Steps) == 0 {
			t.Fatalf("部署方式 %s 没有返回部署步骤", d.Name)
		}
	}
}

func TestSessionEndpoint(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)
	rec := e.do(t, http.MethodGet, "/api/session", c)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"authenticated":true`) || !strings.Contains(rec.Body.String(), `"username":"admin"`) {
		t.Fatalf("返回 %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownAPIReturnsJSON404(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do(t, http.MethodGet, "/api/nope", nil)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "not_found") {
		t.Fatalf("返回 %d %s", rec.Code, rec.Body.String())
	}
}

func TestGuardApplied(t *testing.T) {
	e := newEnv(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "evil.example.com:7788"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Host 不对时状态码 %d，期望 403", rec.Code)
	}
}

func TestSPA(t *testing.T) {
	e := newEnv(t, nil)
	cases := []struct {
		path, want string
		code       int
	}{
		{"/", "app", 200},
		{"/projects/shop", "app", 200}, // 前端路由
		{"/assets/app-1.js", "console.log", 200},
		{"/favicon.svg", "<svg/>", 200},
		{"/assets/missing.js", "", 404},
	}
	for _, c := range cases {
		rec := e.do(t, http.MethodGet, c.path, nil)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Fatalf("%s = %d %q", c.path, rec.Code, rec.Body.String())
		}
	}
	if cc := e.do(t, http.MethodGet, "/assets/app-1.js", nil).Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("assets 应长期缓存，实际 %q", cc)
	}
}

func TestSPANotBuilt(t *testing.T) {
	e := newEnv(t, fstest.MapFS{".gitkeep": {}})
	rec := e.do(t, http.MethodGet, "/", nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "前端还没有构建") {
		t.Fatalf("返回 %d %s", rec.Code, rec.Body.String())
	}
}

func TestEventsStream(t *testing.T) {
	e := newEnv(t, nil)
	c := e.login(t)
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	req.Host = testHost
	req.AddCookie(c)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %s", ct)
	}

	r := bufio.NewReader(resp.Body)
	readEvent := func() string {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					t.Fatal("事件流提前结束")
				}
				t.Fatal(err)
			}
			if name, ok := strings.CutPrefix(strings.TrimSpace(line), "event: "); ok {
				return name
			}
		}
	}
	if got := readEvent(); got != "hello" {
		t.Fatalf("第一条事件 %s，期望 hello", got)
	}
	// 等订阅建立后发布一条自定义事件，应该能收到（中间可能夹着 ping）
	hub := e.hub(t, "admin")
	for hub.Subscribers() == 0 {
		time.Sleep(time.Millisecond)
	}
	// 别的账号的事件收不到
	e.addUser(t, "other", false)
	e.hub(t, "other").Publish(events.Event{Type: "leak"})
	hub.Publish(events.Event{Type: "custom"})
	for i := 0; i < 20; i++ {
		switch readEvent() {
		case "custom":
			return
		case "leak":
			t.Fatal("收到了别的账号的事件")
		}
	}
	t.Fatal("没有收到发布的事件")
}

func (f *fakeSSH) RunStdin(context.Context, remote.Target, string, io.Reader, func(string)) error {
	return nil
}
