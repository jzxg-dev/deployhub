package core

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"deployhub/internal/credential"
	"deployhub/internal/detect"
	"deployhub/internal/driver"
	"deployhub/internal/model"
	"deployhub/internal/remote"
	"deployhub/internal/store"
)

// ServerInput 是新增或修改服务器时提交的内容。
type ServerInput struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	KeyPath string `json:"keyPath"`
	// KeyCredential 是平台保存的 SSH 私钥（凭据）的名称，和 KeyPath 只能填一个。
	KeyCredential string   `json:"keyCredential"`
	SSHOptions    []string `json:"sshOptions"`
}

var (
	hostRe      = regexp.MustCompile(`^[A-Za-z0-9.:\-\[\]]+$`)
	userRe      = regexp.MustCompile(`^[a-z_][a-z0-9_.-]*$`)
	sshOptionRe = regexp.MustCompile(`^[A-Za-z]+=\S.*$`)
)

// 会在本机执行命令的 ssh 选项，不允许通过平台设置（以后 AI 也能改服务器配置）。
var forbiddenSSHOptions = map[string]bool{
	"proxycommand": true, "localcommand": true, "permitlocalcommand": true, "knownhostscommand": true,
	"remotecommand": true, "match": true,
}

func (in *ServerInput) normalize() {
	in.Name, in.Title, in.Host, in.User, in.KeyPath = trim(in.Name), trim(in.Title), trim(in.Host), trim(in.User), trim(in.KeyPath)
	in.KeyCredential = trim(in.KeyCredential)
	if in.Port == 0 {
		in.Port = 22
	}
	opts := []string{}
	for _, o := range in.SSHOptions {
		if o = trim(o); o != "" {
			opts = append(opts, o)
		}
	}
	in.SSHOptions = opts
}

// validate 校验连接信息；checkName 为 false 时不校验名称（修改时名称不变，测试连接时可以没有名称）。
func (in *ServerInput) validate(checkNameField bool) *Error {
	var fields []driver.FieldError
	add := func(f, m string) { fields = append(fields, driver.FieldError{Field: f, Message: m}) }
	if checkNameField {
		if e := checkName("name", in.Name); e != nil {
			fields = append(fields, e.Fields...)
		}
	}
	switch {
	case in.Host == "":
		add("host", "请填写服务器地址")
	case !hostRe.MatchString(in.Host):
		add("host", "地址只能是域名或 IP")
	}
	if in.Port < 1 || in.Port > 65535 {
		add("port", "端口应在 1～65535 之间")
	}
	if in.User != "" && !userRe.MatchString(in.User) {
		add("user", "用户名格式不对")
	}
	if in.KeyPath != "" && in.KeyCredential != "" {
		add("keyPath", "已经选了平台保存的密钥，密钥文件路径要留空")
	} else if in.KeyPath != "" {
		if st, err := os.Stat(remote.ExpandHome(in.KeyPath)); err != nil || st.IsDir() {
			add("keyPath", "运行 deployhub 的机器上找不到这个密钥文件")
		}
	}
	for _, o := range in.SSHOptions {
		if !sshOptionRe.MatchString(o) {
			add("sshOptions", "SSH 选项应写成 名称=值，如 StrictHostKeyChecking=accept-new："+o)
			break
		}
		name, _, _ := strings.Cut(o, "=")
		if forbiddenSSHOptions[strings.ToLower(name)] {
			add("sshOptions", "不允许设置 "+name+"：它会在本机执行命令")
			break
		}
	}
	if len(fields) > 0 {
		return invalid("服务器信息有误", fields...)
	}
	return nil
}

func (in *ServerInput) server() *model.Server {
	return &model.Server{Name: in.Name, Title: in.Title, Host: in.Host, Port: in.Port, User: in.User,
		KeyPath: in.KeyPath, KeyCredential: in.KeyCredential, SSHOptions: in.SSHOptions}
}

// ServerTarget 返回服务器的连接信息。用平台保存的密钥时，要先由业务层把私钥准备成文件（KeyFile）。
func ServerTarget(s *model.Server) remote.Target {
	key := s.KeyPath
	if s.KeyCredential != "" {
		// 没准备好私钥文件时给一个不存在的路径，让连接明确失败，而不是悄悄换成本机默认的密钥
		key = s.KeyFile
		if key == "" {
			key = "/nonexistent/deployhub-key-not-prepared"
		}
	}
	return remote.Target{Host: s.Host, Port: s.Port, User: s.User, KeyPath: key, Options: s.SSHOptions}
}

// checkKeyCredential 校验服务器选的密钥凭据存在而且是 SSH 私钥。
func (s *Service) checkKeyCredential(ctx context.Context, name string) *Error {
	if name == "" {
		return nil
	}
	row, err := s.store.GetCredential(ctx, name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fieldErr("keyCredential", "凭据 "+name+" 不存在，请先在「凭据」里添加 SSH 私钥")
	case err != nil:
		return &Error{Kind: KindFailed, Message: err.Error()}
	case row.Kind != credential.SSHKey:
		return fieldErr("keyCredential", "凭据 "+name+" 不是 SSH 私钥")
	}
	return nil
}

// prepareServerKey 把服务器选的平台密钥准备成文件，填进 srv.KeyFile。没选平台密钥时什么都不做。
func (s *Service) prepareServerKey(ctx context.Context, srv *model.Server) error {
	if srv.KeyCredential == "" {
		return nil
	}
	file, err := s.credentialKeyFile(ctx, srv.KeyCredential)
	if err != nil {
		return err
	}
	srv.KeyFile = file
	return nil
}

// ListServers 返回所有服务器。
func (s *Service) ListServers(ctx context.Context) ([]*model.Server, error) {
	return s.store.ListServers(ctx)
}

// GetServer 按名称读取服务器。
func (s *Service) GetServer(ctx context.Context, name string) (*model.Server, error) {
	srv, err := s.store.GetServer(ctx, name)
	return srv, wrapStore(err, "服务器")
}

// ConnectableServer 读取服务器并准备好连接要用的私钥。
func (s *Service) ConnectableServer(ctx context.Context, name string) (*model.Server, error) {
	srv, err := s.GetServer(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := s.prepareServerKey(ctx, srv); err != nil {
		return nil, err
	}
	return srv, nil
}

// CreateServer 新增服务器。
func (s *Service) CreateServer(ctx context.Context, in ServerInput) (*model.Server, error) {
	in.normalize()
	if e := in.validate(true); e != nil {
		return nil, e
	}
	if e := s.checkKeyCredential(ctx, in.KeyCredential); e != nil {
		return nil, e
	}
	srv := in.server()
	if err := s.store.CreateServer(ctx, srv); err != nil {
		return nil, wrapStore(err, "服务器")
	}
	return s.GetServer(ctx, srv.Name)
}

// UpdateServer 修改服务器（名称不能改）。连接信息变了，之前的检测结果作废。
func (s *Service) UpdateServer(ctx context.Context, name string, in ServerInput) (*model.Server, error) {
	in.normalize()
	if in.Name != "" && in.Name != name {
		return nil, fieldErr("name", "服务器名称创建后不能修改")
	}
	if e := in.validate(false); e != nil {
		return nil, e
	}
	if e := s.checkKeyCredential(ctx, in.KeyCredential); e != nil {
		return nil, e
	}
	srv := in.server()
	srv.Name = name
	if err := s.store.UpdateServer(ctx, srv); err != nil {
		return nil, wrapStore(err, "服务器")
	}
	return s.GetServer(ctx, name)
}

// DeleteServer 删除服务器；还有应用部署在上面时拒绝。
func (s *Service) DeleteServer(ctx context.Context, name string) error {
	apps, err := s.store.ListApps(ctx, "")
	if err != nil {
		return err
	}
	var used []string
	for _, a := range apps {
		if a.Server == name {
			used = append(used, a.Project+"/"+a.Name)
		}
	}
	if len(used) > 0 {
		return conflict("还有应用部署在这台服务器上：%s，先把它们改到其他服务器或删除", strings.Join(used, "、"))
	}
	return wrapStore(s.store.DeleteServer(ctx, name), "服务器")
}

// ServerTestResult 是测试连接的结果。
type ServerTestResult struct {
	OK       bool               `json:"ok"`
	Facts    *model.ServerFacts `json:"facts,omitempty"`
	Error    string             `json:"error,omitempty"`
	Detail   string             `json:"detail,omitempty"` // ssh 原始输出
	Duration int64              `json:"durationMs"`
}

// TestServerInput 用还没保存的连接信息测试连接。
func (s *Service) TestServerInput(ctx context.Context, in ServerInput) (*ServerTestResult, error) {
	in.normalize()
	if e := in.validate(false); e != nil {
		return nil, e
	}
	if e := s.checkKeyCredential(ctx, in.KeyCredential); e != nil {
		return nil, e
	}
	srv := in.server()
	if err := s.prepareServerKey(ctx, srv); err != nil {
		return nil, err
	}
	return s.testTarget(ctx, ServerTarget(srv)), nil
}

// TestServer 测试已保存的服务器，成功时保存检测结果。
func (s *Service) TestServer(ctx context.Context, name string) (*ServerTestResult, error) {
	srv, err := s.ConnectableServer(ctx, name)
	if err != nil {
		return nil, err
	}
	res := s.testTarget(ctx, ServerTarget(srv))
	if res.OK {
		if err := s.store.SetServerFacts(ctx, name, res.Facts, s.now()); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (s *Service) testTarget(ctx context.Context, t remote.Target) *ServerTestResult {
	ctx, cancel := ctxTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	facts, err := detect.Server(ctx, s.ssh, t)
	res := &ServerTestResult{Duration: time.Since(start).Milliseconds()}
	if err != nil {
		res.Error = err.Error()
		var ce *remote.ConnectError
		if errors.As(err, &ce) {
			res.Detail = ce.Detail
		}
		return res
	}
	res.OK, res.Facts = true, facts
	return res
}
