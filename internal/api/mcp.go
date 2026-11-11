package api

import (
	"context"
	"net/http"

	"deployhub/internal/auth"
	"deployhub/internal/mcp"
)

// mcpHandler 把 MCP 服务挂在 /mcp：只接受访问令牌，认证后交给 MCP SDK 处理。
func (s *server) mcpHandler() http.Handler {
	inner := mcp.NewHandler(func(r *http.Request) *mcp.Session {
		info, _ := r.Context().Value(sessionKey).(*sessionInfo)
		if info == nil || info.token == nil {
			return nil
		}
		return &mcp.Session{Core: info.tenant.Core, Engine: info.tenant.Engine, Token: info.token, Version: s.Version}
	}, nil)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.BearerToken(r) == "" {
			w.Header().Set("WWW-Authenticate", `Bearer`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "MCP 要用访问令牌：请求头 Authorization: Bearer <令牌>，令牌在页面「设置 → 访问令牌」里创建")
			return
		}
		info := s.authenticate(w, r)
		if info == nil {
			return
		}
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, info)))
	})
}
