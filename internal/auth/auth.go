// Package auth 负责页面登录和请求来源校验。
//
// 页面登录：用户名和密码登录，登录后写入会话 Cookie；会话保存在库里（只存令牌的哈希），
// 改密码、停用或删除账号时对应的会话立即失效。
// 第一个管理员：还没有账号时，启动时在终端打印一次性链接（10 分钟内有效，只能用一次），打开后创建管理员。
// 来源校验：只接受平台自己地址的 Host，修改类请求还要求 Origin 是平台自己，
// 这样浏览器里打开的其他网站无法偷偷调用接口（DNS 重绑定、跨站请求）。
// 第三方调用方用的访问令牌（API、MCP）在 M4 加入。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"deployhub/internal/model"
)

const (
	// CookieName 是页面会话 Cookie 的名称。
	CookieName = "deployhub_session"
	// SetupTTL 是创建管理员的链接的有效期。
	SetupTTL = 10 * time.Minute
	// SessionTTL 是页面会话的有效期。
	SessionTTL = 30 * 24 * time.Hour
)

// Store 保存一次性令牌和会话的哈希。
type Store interface {
	CreateLoginToken(ctx context.Context, hash string, now, expires time.Time) error
	ConsumeLoginToken(ctx context.Context, hash string, now time.Time) (bool, error)
	CreateSession(ctx context.Context, hash string, userID int64, now, expires time.Time) error
	SessionUser(ctx context.Context, hash string, now time.Time) (*model.User, error)
	DeleteSession(ctx context.Context, hash string) error
}

// Auth 签发和校验页面会话、生成创建管理员的链接、限制密码尝试次数。
type Auth struct {
	store Store
	now   func() time.Time
	// secure 为 true 时会话 Cookie 只通过 HTTPS 发送。
	secure bool
	// Limiter 限制同一个用户名连续输错密码的次数。
	Limiter *Limiter
}

// New 创建 Auth。secureCookie 在对外地址是 HTTPS 时为 true。
func New(store Store, secureCookie bool) *Auth {
	return &Auth{store: store, now: time.Now, secure: secureCookie, Limiter: NewLimiter(5, 5*time.Minute)}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机令牌：%w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewSetupToken 生成一个创建管理员用的一次性令牌，返回明文；库里只存它的哈希。
func (a *Auth) NewSetupToken(ctx context.Context) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := a.now()
	if err := a.store.CreateLoginToken(ctx, hashToken(token), now, now.Add(SetupTTL)); err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeSetupToken 使用一次性令牌，成功后令牌立即失效。
func (a *Auth) ConsumeSetupToken(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	return a.store.ConsumeLoginToken(ctx, hashToken(token), a.now())
}

// StartSession 给账号新建一个会话，返回要写入的 Cookie。
func (a *Auth) StartSession(ctx context.Context, userID int64) (*http.Cookie, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	now := a.now()
	exp := now.Add(SessionTTL)
	if err := a.store.CreateSession(ctx, hashToken(token), userID, now, exp); err != nil {
		return nil, err
	}
	return a.cookie(token, exp), nil
}

func (a *Auth) cookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   a.secure,
		// Strict：从其他网站跳转或发起的请求都不带这个 Cookie
		SameSite: http.SameSiteStrictMode,
	}
}

// SessionHash 返回请求里会话令牌的哈希，没有会话 Cookie 时为空。
func (a *Auth) SessionHash(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return hashToken(c.Value)
}

// User 返回请求对应的登录账号；没登录、会话过期或账号已停用时 ok 为 false。
func (a *Auth) User(r *http.Request) (u *model.User, ok bool) {
	hash := a.SessionHash(r)
	if hash == "" {
		return nil, false
	}
	u, err := a.store.SessionUser(r.Context(), hash, a.now())
	if err != nil {
		return nil, false
	}
	return u, true
}

// EndSession 删除请求的会话，返回用来清掉浏览器里 Cookie 的 Cookie。
func (a *Auth) EndSession(r *http.Request) *http.Cookie {
	if hash := a.SessionHash(r); hash != "" {
		_ = a.store.DeleteSession(r.Context(), hash)
	}
	c := a.cookie("", time.Unix(0, 0))
	c.MaxAge = -1
	return c
}

// HashToken 返回令牌的 SHA-256（十六进制）。会话令牌、访问令牌在库里都只存它。
func HashToken(token string) string { return hashToken(token) }

// NewToken 生成一个随机令牌（32 字节，base64url）。
func NewToken() (string, error) { return randomToken() }

// BearerToken 取出请求头 Authorization: Bearer <令牌> 里的令牌，没有时返回空。
func BearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
