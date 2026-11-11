package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
)

// Guard 校验请求的 Host 和 Origin，只放行平台自己的地址。
type Guard struct {
	hosts   map[string]bool
	origins map[string]bool
}

// NewGuard 根据监听地址（如 127.0.0.1:7788）和额外允许的页面地址生成校验规则。
// 额外的页面地址有两种：前端开发服务器（http://127.0.0.1:5173）和对外访问地址（https://deploy.example.com）。
// 监听 127.0.0.1 和 ::1 时同时放行 localhost；监听所有网卡（0.0.0.0、::）时只放行额外的页面地址。
func NewGuard(listenAddr string, extraOrigins ...string) (*Guard, error) {
	g := &Guard{hosts: map[string]bool{}, origins: map[string]bool{}}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("监听地址 %s 无效：%w", listenAddr, err)
	}
	names := []string{host}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		names = nil
	}
	if host == "127.0.0.1" || host == "::1" {
		names = append(names, "localhost")
	}
	if host == "localhost" {
		names = append(names, "127.0.0.1", "::1")
	}
	for _, n := range names {
		g.allow("http", net.JoinHostPort(n, port))
	}
	for _, o := range extraOrigins {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("页面地址 %s 无效，应形如 https://deploy.example.com 或 http://127.0.0.1:5173，不带路径", o)
		}
		g.allow(u.Scheme, u.Host)
	}
	return g, nil
}

func (g *Guard) allow(scheme, hostport string) {
	g.hosts[hostport] = true
	g.origins[scheme+"://"+hostport] = true
}

// HostOnly 只校验 Host，不看 Origin。给只认访问令牌、不认 Cookie 的入口（MCP）用：
// 没带令牌的请求由它自己返回 401，而不是在这里被当成跨站请求拦下。
func (g *Guard) HostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.hosts[r.Host] {
			http.Error(w, "Host 不被允许", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Middleware 拒绝 Host 不是平台地址的请求；修改类请求（非 GET/HEAD/OPTIONS）还要求 Origin 是平台地址。
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.hosts[r.Host] {
			// 防 DNS 重绑定：恶意域名解析到平台的地址时 Host 是那个域名
			http.Error(w, "Host 不被允许", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			// 浏览器发起的修改类请求一定带 Origin。带访问令牌（Authorization: Bearer）的请求不看 Origin：
			// 这类请求只按令牌认证、不认 Cookie，而别的网站既拿不到令牌，也没法跨站加这个请求头。
			if BearerToken(r) == "" && !g.origins[r.Header.Get("Origin")] {
				http.Error(w, "请求来源不被允许", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
