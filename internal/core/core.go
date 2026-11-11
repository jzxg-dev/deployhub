// Package core 是业务层：服务器、密钥与变量、项目、应用的增删改查和校验。
// 页面、HTTP API、以后的 MCP 都调用这里，规则只写一份。
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"deployhub/internal/detect"
	"deployhub/internal/driver"
	"deployhub/internal/remote"
	"deployhub/internal/store"
	"deployhub/internal/vault"
)

// Kind 是错误的类别，接口据此决定 HTTP 状态码。
type Kind int

const (
	KindInvalid  Kind = iota + 1 // 参数不对（422）
	KindNotFound                 // 不存在（404）
	KindConflict                 // 和现有数据冲突：重名、被引用、版本过期（409）
	KindFailed                   // 执行失败：连不上服务器等（502）
)

// Error 是业务错误，带中文说明，校验错误按字段列出。
type Error struct {
	Kind    Kind
	Message string
	Fields  []driver.FieldError
}

func (e *Error) Error() string { return e.Message }

func invalid(msg string, fields ...driver.FieldError) *Error {
	return &Error{Kind: KindInvalid, Message: msg, Fields: fields}
}

func fieldErr(field, msg string) *Error {
	return invalid(msg, driver.FieldError{Field: field, Message: msg})
}

func notFound(format string, a ...any) *Error {
	return &Error{Kind: KindNotFound, Message: fmt.Sprintf(format, a...)}
}

func conflict(format string, a ...any) *Error {
	return &Error{Kind: KindConflict, Message: fmt.Sprintf(format, a...)}
}

// Service 是业务层入口。
type Service struct {
	store   *store.Store
	vault   *vault.Vault
	drivers *driver.Registry
	ssh     remote.Runner
	repos   detect.RepoDetector
	now     func() time.Time
	// dataDir 放这个账号的私钥临时文件（keys/）和平台克隆的仓库（repos/）
	dataDir string
	// repoBusy 判断应用是不是正在部署（由部署引擎设置）；正在部署时不去动平台克隆的仓库
	repoBusy func(project, app string) bool
	repoMu   sync.Mutex
	repoLock map[string]*sync.Mutex // 仓库目录 → 锁
	// gitProtocols 是拉取 git 地址时允许的协议（GIT_ALLOW_PROTOCOL）
	gitProtocols string
}

// New 创建业务层。
func New(st *store.Store, v *vault.Vault, drivers *driver.Registry, ssh remote.Runner) *Service {
	return &Service{store: st, vault: v, drivers: drivers, ssh: ssh, now: time.Now,
		dataDir: filepath.Join(os.TempDir(), "deployhub-data"), repoLock: map[string]*sync.Mutex{}, gitProtocols: "https:http:ssh"}
}

// SetGitProtocols 替换拉取 git 地址时允许的协议（测试用：让测试能从本地目录克隆）。
func (s *Service) SetGitProtocols(p string) { s.gitProtocols = p }

// SetDataDir 指定这个账号的数据目录（私钥临时文件、平台克隆的仓库）。
func (s *Service) SetDataDir(dir string) { s.dataDir = dir }

// SetRepoBusy 设置「应用是不是正在部署」的判断，由部署引擎提供。
func (s *Service) SetRepoBusy(fn func(project, app string) bool) { s.repoBusy = fn }

// SetRepoDetector 替换仓库识别（测试用）。
func (s *Service) SetRepoDetector(d detect.RepoDetector) { s.repos = d }

// Drivers 返回部署方式注册表。
func (s *Service) Drivers() *driver.Registry { return s.drivers }

var (
	nameRe       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,38}[a-z0-9]$`)
	secretNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
)

const nameHint = "小写字母开头，只能包含小写字母、数字和 -，2～40 个字符"

func checkName(field, v string) *Error {
	if v == "" {
		return fieldErr(field, "请填写标识")
	}
	if !nameRe.MatchString(v) {
		return fieldErr(field, nameHint)
	}
	return nil
}

// wrapStore 把存储层的错误换成业务错误。
func wrapStore(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return notFound("%s不存在", what)
	case errors.Is(err, store.ErrDuplicate):
		return conflict("已有同名的%s", what)
	case errors.Is(err, store.ErrConflict):
		return conflict("%s已被修改（可能在另一个页面），请刷新后再改", what)
	}
	return err
}

func trim(s string) string { return strings.TrimSpace(s) }

func ctxTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
