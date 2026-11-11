package core

import (
	"context"

	"deployhub/internal/detect"
	"deployhub/internal/driver"
	"deployhub/internal/model"
	"deployhub/internal/remote"
	"deployhub/internal/store"
)

// DeployTarget 是部署一个应用需要的全部信息，给部署引擎用。
type DeployTarget struct {
	App      *model.App // 敏感参数已解密，${名称} 还没替换
	Spec     driver.Spec
	Fields   []driver.Field
	Executor driver.Executor
	Server   *model.Server // 不需要服务器的部署方式（如 Workers）为 nil
	// Creds 是参数里选择的凭据：参数键 → 凭据字段（敏感字段是明文）。
	Creds map[string]map[string]string
}

// Target 返回服务器的连接信息；没有服务器时为空。
func (t *DeployTarget) Target() remote.Target {
	if t.Server == nil {
		return remote.Target{}
	}
	return ServerTarget(t.Server)
}

// DeployTarget 读取部署一个应用需要的信息。
func (s *Service) DeployTarget(ctx context.Context, project, app string) (*DeployTarget, error) {
	a, err := s.GetApp(ctx, project, app)
	if err != nil {
		return nil, err
	}
	d, ok := s.drivers.Get(a.Driver)
	if !ok {
		return nil, invalid("不认识的部署方式：" + a.Driver)
	}
	ex, ok := d.(driver.Executor)
	if !ok {
		return nil, invalid("部署方式「" + d.Spec().Title + "」的部署执行还没实现")
	}
	fields, _ := s.fieldsOf(a.Driver)
	t := &DeployTarget{App: a, Spec: d.Spec(), Fields: fields, Executor: ex}
	if t.Spec.UsesServer() {
		if t.Server, err = s.ConnectableServer(ctx, a.Server); err != nil {
			return nil, err
		}
	}
	if t.Creds, err = s.credsFor(ctx, fields, a.Params); err != nil {
		return nil, err
	}
	return t, nil
}

// SecretValues 返回指定密钥的明文值，不存在的不返回。
func (s *Service) SecretValues(ctx context.Context, names []string) (map[string]string, error) {
	out := map[string]string{}
	for _, n := range names {
		enc, err := s.store.SecretValue(ctx, n)
		if err == store.ErrNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		v, err := s.vault.Decrypt(enc)
		if err != nil {
			return nil, err
		}
		out[n] = v
	}
	return out, nil
}

// PlanContext 组装执行用的上下文（参数应已替换 ${名称}）。
func (s *Service) PlanContext(project, app string, params map[string]any, srv *model.Server, repo *detect.Repo, version string) *driver.PlanContext {
	c := s.planContext(project, app, params, srv, repo)
	c.Version = version
	return c
}

// DetectServer 测试连接并保存检测结果。srv 应来自 ConnectableServer（私钥已准备好）。
func (s *Service) DetectServer(ctx context.Context, srv *model.Server) (*model.ServerFacts, error) {
	facts, err := detect.Server(ctx, s.ssh, ServerTarget(srv))
	if err != nil {
		return nil, err
	}
	srv.Facts = facts
	_ = s.store.SetServerFacts(ctx, srv.Name, facts, s.now())
	return facts, nil
}

// Store 返回存储层（部署引擎直接读写任务表）。
func (s *Service) Store() *store.Store { return s.store }
