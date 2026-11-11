// Package api 提供 HTTP 接口、SSE 事件流和前端页面。
//
// 约定：接口前缀 /api，JSON 格式；错误统一返回 {"error": {"code", "message", "fields"}}。
// 页面用账号密码登录，之后靠会话 Cookie 认证；AI 和脚本用访问令牌（Authorization: Bearer），能调用哪些接口看令牌的权限级别。
// 所有接口和它们的访问要求集中在 routes.go，OpenAPI 文档也从那里生成。
// 除了登录和创建管理员，所有接口都只读写当前账号的数据（见 tenant）。
package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"deployhub/internal/auth"
	"deployhub/internal/core"
	"deployhub/internal/credential"
	"deployhub/internal/driver"
	"deployhub/internal/engine"
	"deployhub/internal/model"
	"deployhub/internal/tenant"
)

// Options 是创建接口服务需要的依赖。
type Options struct {
	Version    string
	InstanceID string // 数据目录的指纹，用于识别「已经在运行的是不是同一个 deployhub」
	Auth       *auth.Auth
	Guard      *auth.Guard
	Accounts   *core.Accounts
	Tenants    *tenant.Manager
	Drivers    *driver.Registry
	UI         fs.FS // 前端构建产物
	Logger     *slog.Logger
	// PingInterval 是 SSE 心跳间隔，默认 15 秒。
	PingInterval time.Duration
}

type server struct {
	Options
}

// NewHandler 创建处理所有请求的 Handler。
func NewHandler(o Options) http.Handler {
	if o.PingInterval == 0 {
		o.PingInterval = 15 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	s := &server{Options: o}

	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		mux.Handle(rt.method+" "+rt.path, s.guard(rt))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "接口不存在")
	})
	// 其余路径都是前端页面
	mux.Handle("/", newSPA(o.UI))

	// MCP 只认访问令牌、不认 Cookie，所以不需要 Origin 校验（只校验 Host）
	top := http.NewServeMux()
	top.Handle("/mcp", o.Guard.HostOnly(s.mcpHandler()))
	top.Handle("/", o.Guard.Middleware(mux))
	return securityHeaders(top)
}

type ctxKey int

const sessionKey ctxKey = 0

// sessionInfo 是请求对应的账号和它的业务层、引擎、事件通道。token 不为空表示是用访问令牌调用的。
type sessionInfo struct {
	user   *model.User
	tenant *tenant.Tenant
	token  *model.AccessToken
}

// authenticate 认证请求：带 Authorization: Bearer 的只按访问令牌认证（不看 Cookie），否则按页面会话。
// 失败时已经写好响应，返回 nil。
func (s *server) authenticate(w http.ResponseWriter, r *http.Request) *sessionInfo {
	info := &sessionInfo{}
	if bearer := auth.BearerToken(r); bearer != "" {
		t, u, err := s.Accounts.AuthenticateToken(r.Context(), bearer)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			writeError(w, http.StatusUnauthorized, "invalid_token", "访问令牌无效或已被吊销")
			return nil
		}
		info.user, info.token = u, t
	} else {
		u, ok := s.Auth.User(r)
		if !ok {
			if setup, err := s.Accounts.NeedsSetup(r.Context()); err == nil && setup {
				writeError(w, http.StatusUnauthorized, "setup_required", "还没有账号：请用终端里打印的链接创建管理员，或重新运行 deployhub")
				return nil
			}
			writeError(w, http.StatusUnauthorized, "unauthorized", "未登录或登录已过期，请重新登录")
			return nil
		}
		info.user = u
	}
	t, err := s.Tenants.Get(r.Context(), info.user.ID)
	if err != nil {
		s.fail(w, r, err)
		return nil
	}
	info.tenant = t
	return info
}

// guard 按接口的访问要求包一层认证和权限检查，并把账号和它的 Tenant 放进请求的上下文。
func (s *server) guard(rt route) http.Handler {
	if rt.access == accessPublic {
		return rt.fn
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := s.authenticate(w, r)
		if info == nil {
			return
		}
		switch {
		case info.token != nil && rt.access.level() == "":
			writeError(w, http.StatusForbidden, "page_only", "这个操作只能登录页面后做，访问令牌不能调用")
			return
		case info.token != nil && model.LevelRank(info.token.Level) < model.LevelRank(rt.access.level()):
			writeError(w, http.StatusForbidden, "forbidden", "访问令牌的权限不够：这个操作需要「"+levelTitle(rt.access.level())+"」权限")
			return
		case rt.access == accessPageAdmin && !info.user.Admin:
			writeError(w, http.StatusForbidden, "forbidden", "只有管理员可以管理账号")
			return
		}
		rt.fn(w, r.WithContext(context.WithValue(r.Context(), sessionKey, info)))
	})
}

// 下面几个只能在 guard 包着的处理函数里用。
func (s *server) token(r *http.Request) *model.AccessToken {
	return r.Context().Value(sessionKey).(*sessionInfo).token
}

// initiator 返回任务的发起方：页面是 page，访问令牌是 token:<名称>。
func (s *server) initiator(r *http.Request) string {
	if t := s.token(r); t != nil {
		return "token:" + t.Name
	}
	return "page"
}

func (s *server) user(r *http.Request) *model.User {
	return r.Context().Value(sessionKey).(*sessionInfo).user
}
func (s *server) tenant(r *http.Request) *tenant.Tenant {
	return r.Context().Value(sessionKey).(*sessionInfo).tenant
}
func (s *server) core(r *http.Request) *core.Service    { return s.tenant(r).Core }
func (s *server) engine(r *http.Request) *engine.Engine { return s.tenant(r).Engine }

// health 用于检查服务是否在运行；第二次运行 deployhub 时据此判断端口是不是被自己占用。
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"app":      "deployhub",
		"version":  s.Version,
		"instance": s.InstanceID,
	})
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"authenticated": true, "version": s.Version, "user": s.user(r)}
	if t := s.token(r); t != nil {
		out["token"] = t // 用访问令牌调用时，返回令牌的名称和权限
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) drivers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		driver.Catalog
		CredentialKinds []credential.Kind `json:"credentialKinds"`
	}{s.Drivers.Catalog(), credential.Kinds()})
}

// securityHeaders 加上通用的安全响应头。Referrer-Policy 防止创建管理员的链接里的令牌通过 Referer 泄露。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type errorBody struct {
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Fields  []driver.FieldError `json:"fields,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string, fields ...driver.FieldError) {
	writeJSON(w, status, map[string]errorBody{"error": {Code: code, Message: message, Fields: fields}})
}
