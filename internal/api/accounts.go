package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"

	"deployhub/internal/core"
)

// login 用用户名和密码登录，成功后写入会话 Cookie。同一个用户名连续输错几次后要等一会儿才能再试。
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	name := core.NormalizeUsername(in.Username)
	if name == "" || in.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "请填写用户名和密码")
		return
	}
	key := "user:" + name
	if ok, wait := s.Auth.Limiter.Allow(key); !ok {
		minutes := int(math.Ceil(wait.Minutes()))
		w.Header().Set("Retry-After", fmt.Sprint(int(math.Ceil(wait.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", fmt.Sprintf("密码输错次数太多，请 %d 分钟后再试", minutes))
		return
	}
	u, err := s.Accounts.Authenticate(r.Context(), name, in.Password)
	if errors.Is(err, core.ErrBadCredentials) {
		s.Auth.Limiter.Fail(key)
		s.Logger.Warn("登录失败", "username", name, "remote", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码不对")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Auth.Limiter.Reset(key)
	c, err := s.Auth.StartSession(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, c)
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

// logout 退出登录：删除会话并清掉 Cookie。没登录时也返回成功。
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, s.Auth.EndSession(r))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// setup 用终端里打印的一次性链接创建第一个管理员，成功后直接登录。
func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if need, err := s.Accounts.NeedsSetup(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	} else if !need {
		writeError(w, http.StatusConflict, "conflict", "已经有账号了，请直接登录")
		return
	}
	// 先校验填写的内容再用掉令牌：填错了改一下还能用同一个链接
	if err := core.ValidateNewAccount(in.Username, in.Password); err != nil {
		s.fail(w, r, err)
		return
	}
	ok, err := s.Auth.ConsumeSetupToken(r.Context(), in.Token)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "setup_expired", "这个链接已经过期或用过了，重新运行 deployhub 会打印新的链接")
		return
	}
	u, err := s.Accounts.CreateFirstAdmin(r.Context(), in.Username, in.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.Auth.StartSession(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, c)
	writeJSON(w, http.StatusCreated, map[string]any{"user": u})
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.ok(w, r, s.Accounts.ChangePassword(r.Context(), s.user(r), in.Current, in.Password, s.Auth.SessionHash(r)))
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Accounts.List(r.Context())
	s.respond(w, r, http.StatusOK, list, err)
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var in core.UserInput
	if !decode(w, r, &in) {
		return
	}
	u, err := s.Accounts.Create(r.Context(), in)
	s.respond(w, r, http.StatusCreated, u, err)
}

func (s *server) updateUser(w http.ResponseWriter, r *http.Request) {
	var in core.UserUpdate
	if !decode(w, r, &in) {
		return
	}
	u, err := s.Accounts.Update(r.Context(), s.user(r), r.PathValue("username"), in)
	s.respond(w, r, http.StatusOK, u, err)
}

// deleteUser 删除账号和它的全部数据；账号有任务正在执行时拒绝。
func (s *server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u, err := s.Accounts.Lookup(r.Context(), r.PathValue("username"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n := s.Tenants.Busy(u.ID); n > 0 {
		writeError(w, http.StatusConflict, "busy", fmt.Sprintf("这个账号有 %d 个任务正在执行，等它们结束后再删除", n))
		return
	}
	if err := s.Accounts.Delete(r.Context(), s.user(r), u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Tenants.Remove(u.ID)
	s.ok(w, r, nil)
}
