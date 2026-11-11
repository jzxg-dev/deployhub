package core

import (
	"context"
	"crypto/rand"
	"errors"
	"regexp"
	"strings"
	"time"

	"deployhub/internal/auth"
	"deployhub/internal/driver"
	"deployhub/internal/model"
	"deployhub/internal/store"
)

// Accounts 管理登录账号：创建第一个管理员、校验密码、新建、改密码、停用、删除。
// 账号不属于任何一个账号的数据，所以用的是不带归属的 Store。
type Accounts struct {
	store *store.Store
	now   func() time.Time
}

// NewAccounts 创建账号管理；st 应是 store.Open 返回的不带归属的 Store。
func NewAccounts(st *store.Store) *Accounts {
	return &Accounts{store: st, now: time.Now}
}

// ErrBadCredentials 表示用户名或密码不对（包括账号已停用），不区分具体原因。
var ErrBadCredentials = errors.New("用户名或密码不对")

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

const (
	minPasswordLen = 8
	maxPasswordLen = 128
)

// NormalizeUsername 去掉首尾空白并转成小写：用户名不区分大小写。
func NormalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func checkUsername(v string) []driver.FieldError {
	switch {
	case v == "":
		return []driver.FieldError{{Field: "username", Message: "请填写用户名"}}
	case !usernameRe.MatchString(v):
		return []driver.FieldError{{Field: "username", Message: "小写字母开头，只能包含小写字母、数字、- 和 _，2～32 个字符"}}
	}
	return nil
}

func checkPassword(field, v string) []driver.FieldError {
	switch {
	case v == "":
		return []driver.FieldError{{Field: field, Message: "请填写密码"}}
	case len(v) < minPasswordLen:
		return []driver.FieldError{{Field: field, Message: "密码至少 8 个字符"}}
	case len(v) > maxPasswordLen:
		return []driver.FieldError{{Field: field, Message: "密码最多 128 个字符"}}
	}
	return nil
}

// NeedsSetup 返回是否还没有任何账号（要先创建管理员）。
func (a *Accounts) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := a.store.CountUsers(ctx)
	return n == 0, err
}

// ValidateNewAccount 只校验用户名和密码的格式，不保存。
func ValidateNewAccount(username, password string) error {
	if fields := append(checkUsername(NormalizeUsername(username)), checkPassword("password", password)...); len(fields) > 0 {
		return invalid("账号信息有误", fields...)
	}
	return nil
}

// CreateFirstAdmin 创建第一个管理员；已经有账号时拒绝。升级前已有的数据归这个账号。
func (a *Accounts) CreateFirstAdmin(ctx context.Context, username, password string) (*model.User, error) {
	username = NormalizeUsername(username)
	if err := ValidateNewAccount(username, password); err != nil {
		return nil, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	if err := a.store.CreateFirstUser(ctx, username, hash); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, conflict("已经有账号了，请直接登录")
		}
		return nil, err
	}
	u, err := a.get(ctx, username)
	if err != nil {
		return nil, err
	}
	// 创建后直接登录，记一次登录时间
	_ = a.store.TouchUserLogin(ctx, u.ID, a.now())
	return u, nil
}

func (a *Accounts) get(ctx context.Context, username string) (*model.User, error) {
	u, err := a.store.GetUser(ctx, username)
	if err != nil {
		return nil, wrapStore(err, "账号")
	}
	return &u.User, nil
}

// Authenticate 校验用户名和密码，成功时记录登录时间。失败一律返回 ErrBadCredentials。
func (a *Accounts) Authenticate(ctx context.Context, username, password string) (*model.User, error) {
	u, err := a.store.GetUser(ctx, NormalizeUsername(username))
	if errors.Is(err, store.ErrNotFound) {
		auth.BurnPassword(password)
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	if !auth.VerifyPassword(u.PasswordHash, password) || u.Disabled {
		return nil, ErrBadCredentials
	}
	_ = a.store.TouchUserLogin(ctx, u.ID, a.now())
	return &u.User, nil
}

// AuthenticateToken 校验访问令牌，返回令牌和它所属的账号。令牌不存在、已吊销或账号已停用时返回 ErrBadCredentials。
func (a *Accounts) AuthenticateToken(ctx context.Context, token string) (*model.AccessToken, *model.User, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return nil, nil, ErrBadCredentials
	}
	t, u, err := a.store.TokenByHash(ctx, auth.HashToken(token), a.now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrBadCredentials
	}
	return t, u, err
}

// List 返回所有账号。
func (a *Accounts) List(ctx context.Context) ([]*model.User, error) {
	return a.store.ListUsers(ctx)
}

// UserInput 是新建账号时提交的内容。
type UserInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Admin    bool   `json:"admin"`
}

// Create 新建账号。
func (a *Accounts) Create(ctx context.Context, in UserInput) (*model.User, error) {
	in.Username = NormalizeUsername(in.Username)
	if fields := append(checkUsername(in.Username), checkPassword("password", in.Password)...); len(fields) > 0 {
		return nil, invalid("账号信息有误", fields...)
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	if err := a.store.CreateUser(ctx, in.Username, hash, in.Admin); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return nil, fieldErr("username", "已有这个用户名")
		}
		return nil, err
	}
	return a.get(ctx, in.Username)
}

// UserUpdate 是管理员修改账号时提交的内容，没填的不改。
type UserUpdate struct {
	Password *string `json:"password"`
	Admin    *bool   `json:"admin"`
	Disabled *bool   `json:"disabled"`
}

// Update 由管理员 actor 修改账号 username：重置密码、设为或取消管理员、停用或启用。
// 不能停用自己或取消自己的管理员，这样始终至少有一个能用的管理员。
// 重置密码或停用后，那个账号已登录的页面立即失效。
func (a *Accounts) Update(ctx context.Context, actor *model.User, username string, in UserUpdate) (*model.User, error) {
	u, err := a.store.GetUser(ctx, username)
	if err != nil {
		return nil, wrapStore(err, "账号")
	}
	self := u.ID == actor.ID
	if in.Password != nil {
		if self {
			return nil, fieldErr("password", "改自己的密码请到「设置」里，需要先输入当前密码")
		}
		if fields := checkPassword("password", *in.Password); len(fields) > 0 {
			return nil, invalid("密码不符合要求", fields...)
		}
	}
	admin, disabled := u.Admin, u.Disabled
	if in.Admin != nil {
		admin = *in.Admin
	}
	if in.Disabled != nil {
		disabled = *in.Disabled
	}
	if self && (!admin || disabled) {
		return nil, conflict("不能停用自己或取消自己的管理员身份，请让另一个管理员操作")
	}
	if in.Password != nil {
		hash, err := auth.HashPassword(*in.Password)
		if err != nil {
			return nil, err
		}
		if err := a.store.SetUserPassword(ctx, u.ID, hash); err != nil {
			return nil, wrapStore(err, "账号")
		}
	}
	if admin != u.Admin || disabled != u.Disabled {
		if err := a.store.SetUserFlags(ctx, u.ID, admin, disabled); err != nil {
			return nil, wrapStore(err, "账号")
		}
	}
	if in.Password != nil || (disabled && !u.Disabled) {
		if err := a.store.DeleteUserSessions(ctx, u.ID, ""); err != nil {
			return nil, err
		}
	}
	return a.get(ctx, username)
}

// Lookup 读取账号（删除前确认用）。
func (a *Accounts) Lookup(ctx context.Context, username string) (*model.User, error) {
	return a.get(ctx, username)
}

// Delete 由管理员 actor 删除账号和它的全部数据。不能删除自己。
func (a *Accounts) Delete(ctx context.Context, actor, u *model.User) error {
	if u.ID == actor.ID {
		return conflict("不能删除自己的账号，请让另一个管理员操作")
	}
	return wrapStore(a.store.DeleteUser(ctx, u.ID), "账号")
}

// ChangePassword 修改自己的密码，要先输入当前密码。成功后其他设备上已登录的页面失效，
// keepSession 是当前页面的会话（保留）。
func (a *Accounts) ChangePassword(ctx context.Context, user *model.User, current, password, keepSession string) error {
	u, err := a.store.GetUser(ctx, user.Username)
	if err != nil {
		return wrapStore(err, "账号")
	}
	if !auth.VerifyPassword(u.PasswordHash, current) {
		return fieldErr("current", "当前密码不对")
	}
	if fields := checkPassword("password", password); len(fields) > 0 {
		return invalid("密码不符合要求", fields...)
	}
	if password == current {
		return fieldErr("password", "新密码不能和当前密码一样")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := a.store.SetUserPassword(ctx, u.ID, hash); err != nil {
		return wrapStore(err, "账号")
	}
	return a.store.DeleteUserSessions(ctx, u.ID, keepSession)
}

// ResetPassword 给账号换一个随机密码并返回它，用于忘记密码时在运行 deployhub 的机器上找回（命令行 -reset-password）。
// 账号已登录的页面全部失效。
func (a *Accounts) ResetPassword(ctx context.Context, username string) (string, error) {
	u, err := a.store.GetUser(ctx, NormalizeUsername(username))
	if err != nil {
		return "", wrapStore(err, "账号")
	}
	password, err := randomPassword(16)
	if err != nil {
		return "", err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	if err := a.store.SetUserPassword(ctx, u.ID, hash); err != nil {
		return "", wrapStore(err, "账号")
	}
	return password, a.store.DeleteUserSessions(ctx, u.ID, "")
}

// randomPassword 生成 n 个字符的随机密码，去掉了容易看错的字符（0 O 1 l I）。
func randomPassword(n int) (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		// 256 不是字符数的整数倍，取模有很小的偏差；16 个字符的随机密码不在意这一点
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}
