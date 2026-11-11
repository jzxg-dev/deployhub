package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"deployhub/internal/model"
)

type memSession struct {
	userID  int64
	expires time.Time
}

type memStore struct {
	tokens   map[string]time.Time // hash -> 过期时间
	used     map[string]bool
	sessions map[string]memSession
	disabled map[int64]bool
}

func newMemStore() *memStore {
	return &memStore{tokens: map[string]time.Time{}, used: map[string]bool{}, sessions: map[string]memSession{}, disabled: map[int64]bool{}}
}

func (m *memStore) CreateLoginToken(_ context.Context, hash string, _, expires time.Time) error {
	m.tokens[hash] = expires
	return nil
}

func (m *memStore) ConsumeLoginToken(_ context.Context, hash string, now time.Time) (bool, error) {
	exp, ok := m.tokens[hash]
	if !ok || m.used[hash] || !now.Before(exp) {
		return false, nil
	}
	m.used[hash] = true
	return true, nil
}

func (m *memStore) CreateSession(_ context.Context, hash string, userID int64, _, expires time.Time) error {
	m.sessions[hash] = memSession{userID, expires}
	return nil
}

func (m *memStore) SessionUser(_ context.Context, hash string, now time.Time) (*model.User, error) {
	s, ok := m.sessions[hash]
	if !ok || !now.Before(s.expires) || m.disabled[s.userID] {
		return nil, errors.New("没有这个会话")
	}
	return &model.User{ID: s.userID, Username: "u"}, nil
}

func (m *memStore) DeleteSession(_ context.Context, hash string) error {
	delete(m.sessions, hash)
	return nil
}

func TestSetupTokenFlow(t *testing.T) {
	ctx := context.Background()
	st := newMemStore()
	a := New(st, false)
	tok, err := a.NewSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, stored := st.tokens[tok]; stored {
		t.Fatal("不应保存令牌明文")
	}
	if ok, _ := a.ConsumeSetupToken(ctx, tok); !ok {
		t.Fatal("新令牌应该能用")
	}
	if ok, _ := a.ConsumeSetupToken(ctx, tok); ok {
		t.Fatal("令牌只能用一次")
	}
	if ok, _ := a.ConsumeSetupToken(ctx, ""); ok {
		t.Fatal("空令牌不能通过")
	}
}

func TestSession(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st := newMemStore()
	a := New(st, false)
	a.now = func() time.Time { return now }

	c, err := a.StartSession(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure {
		t.Fatal("会话 Cookie 必须 HttpOnly 且 SameSite=Strict；HTTP 下不带 Secure")
	}
	if _, stored := st.sessions[c.Value]; stored {
		t.Fatal("不应保存会话令牌明文")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	if u, ok := a.User(req); !ok || u.ID != 7 {
		t.Fatalf("刚签发的会话应该有效：%v %v", u, ok)
	}

	// 乱填的 Cookie
	bad := httptest.NewRequest(http.MethodGet, "/", nil)
	bad.AddCookie(&http.Cookie{Name: CookieName, Value: c.Value + "x"})
	if _, ok := a.User(bad); ok {
		t.Fatal("不存在的会话不能通过")
	}
	if _, ok := a.User(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Fatal("没有 Cookie 不能通过")
	}

	// 账号停用
	st.disabled[7] = true
	if _, ok := a.User(req); ok {
		t.Fatal("账号停用后会话不能通过")
	}
	st.disabled[7] = false

	// 退出
	cleared := a.EndSession(req)
	if cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatal("退出时应清掉 Cookie")
	}
	if _, ok := a.User(req); ok {
		t.Fatal("退出后会话不能通过")
	}

	// 过期
	c2, _ := a.StartSession(context.Background(), 7)
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(c2)
	a.now = func() time.Time { return now.Add(SessionTTL + time.Second) }
	if _, ok := a.User(req2); ok {
		t.Fatal("过期的会话不能通过")
	}

	// 对外地址是 HTTPS 时带 Secure
	if c, _ := New(newMemStore(), true).StartSession(context.Background(), 1); !c.Secure {
		t.Fatal("HTTPS 下会话 Cookie 应带 Secure")
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct-horse") {
		t.Fatal("正确的密码应该通过")
	}
	if VerifyPassword(h, "correct-horsf") || VerifyPassword(h, "") {
		t.Fatal("错误的密码不能通过")
	}
	h2, _ := HashPassword("correct-horse")
	if h == h2 {
		t.Fatal("同一个密码两次的哈希应该不同（盐不同）")
	}
	for _, bad := range []string{"", "plain", "pbkdf2-sha256$x$a$b", "pbkdf2-sha256$0$YQ$YQ", "md5$1$YQ$YQ"} {
		if VerifyPassword(bad, "correct-horse") {
			t.Fatalf("格式不对的哈希 %q 不能通过", bad)
		}
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("第 %d 次应该允许", i+1)
		}
		l.Fail("a")
	}
	if ok, wait := l.Allow("a"); ok || wait != time.Minute {
		t.Fatalf("失败 3 次后应拒绝并等 1 分钟：%v %v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("别的 key 不受影响")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("过了时间窗口应恢复")
	}
	l.Fail("a")
	l.Reset("a")
	for i := 0; i < 2; i++ {
		l.Fail("a")
	}
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("Reset 后重新计数")
	}
}

func TestGuard(t *testing.T) {
	g, err := NewGuard("127.0.0.1:7788", "http://127.0.0.1:5173", "https://deploy.example.com")
	if err != nil {
		t.Fatal(err)
	}
	h := g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	cases := []struct {
		name   string
		method string
		host   string
		origin string
		want   int
	}{
		{"本机 GET", http.MethodGet, "127.0.0.1:7788", "", 200},
		{"localhost GET", http.MethodGet, "localhost:7788", "", 200},
		{"开发服务器 GET", http.MethodGet, "127.0.0.1:5173", "", 200},
		{"对外地址 GET", http.MethodGet, "deploy.example.com", "", 200},
		{"重绑定域名", http.MethodGet, "evil.example.com:7788", "", 403},
		{"端口不对", http.MethodGet, "127.0.0.1:8080", "", 403},
		{"同源 POST", http.MethodPost, "127.0.0.1:7788", "http://127.0.0.1:7788", 200},
		{"开发服务器 POST", http.MethodPost, "127.0.0.1:5173", "http://127.0.0.1:5173", 200},
		{"对外地址 POST", http.MethodPost, "deploy.example.com", "https://deploy.example.com", 200},
		{"对外地址但协议不对", http.MethodPost, "deploy.example.com", "http://deploy.example.com", 403},
		{"跨站 POST", http.MethodPost, "127.0.0.1:7788", "https://evil.example.com", 403},
		{"没有 Origin 的 POST", http.MethodPost, "127.0.0.1:7788", "", 403},
		{"跨站 DELETE", http.MethodDelete, "127.0.0.1:7788", "http://127.0.0.1:9999", 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/api/x", nil)
			req.Host = c.host
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("状态码 %d，期望 %d", rec.Code, c.want)
			}
		})
	}
}

func TestGuardListenAll(t *testing.T) {
	// 监听所有网卡时只认对外地址，不放行 0.0.0.0 这种 Host
	g, err := NewGuard("0.0.0.0:7788", "http://192.168.1.5:7788")
	if err != nil {
		t.Fatal(err)
	}
	h := g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for host, want := range map[string]int{"192.168.1.5:7788": 200, "0.0.0.0:7788": 403, "localhost:7788": 403} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("Host %s 状态码 %d，期望 %d", host, rec.Code, want)
		}
	}
}

func TestGuardRejectsBadOrigin(t *testing.T) {
	if _, err := NewGuard("127.0.0.1:7788", "ftp://x"); err == nil {
		t.Fatal("非 http 的页面地址应该报错")
	}
	if _, err := NewGuard("127.0.0.1:7788", "https://deploy.example.com/hub"); err == nil {
		t.Fatal("带路径的页面地址应该报错")
	}
	if _, err := NewGuard("bad-addr"); err == nil {
		t.Fatal("无效的监听地址应该报错")
	}
}
