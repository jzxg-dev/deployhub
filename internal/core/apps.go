package core

import (
	"context"
	"errors"
	"fmt"

	"deployhub/internal/detect"
	"deployhub/internal/driver"
	"deployhub/internal/model"
	"deployhub/internal/store"
	"deployhub/internal/vault"
)

// AppInput 是新增或修改应用时提交的内容。
type AppInput struct {
	// Driver 是部署方式，新增时必填；修改时可以不填，填了必须和原来一致。
	Driver string `json:"driver"`
	// Params 是参数，键见部署方式的声明。
	Params map[string]any `json:"params"`
	// Revision 是修改时基于的版本，不是最新版本时拒绝保存（防止覆盖别处的修改）。
	Revision int `json:"revision"`
}

// fieldsOf 返回部署方式的全部参数（通用参数 + 部署方式参数）。
func (s *Service) fieldsOf(driverName string) ([]driver.Field, bool) {
	d, ok := s.drivers.Get(driverName)
	if !ok {
		return nil, false
	}
	return driver.FieldsOf(d.Spec()), true
}

// serverID 返回应用保存时的服务器编号；不需要服务器的部署方式为 nil。
func (p *prepared) serverID() *int64 {
	if p.server == nil {
		return nil
	}
	return &p.server.ID
}

// prepared 是校验通过、可以保存的应用参数。
type prepared struct {
	driver string
	fields []driver.Field
	params map[string]any
	server *model.Server
}

// prepare 校验应用参数。existing 为 nil 表示新增。
func (s *Service) prepare(ctx context.Context, project *model.Project, existing *model.App, in AppInput) (*prepared, error) {
	drv := in.Driver
	if existing != nil {
		if drv != "" && drv != existing.Driver {
			return nil, fieldErr("driver", "部署方式创建后不能修改，换部署方式请新建应用")
		}
		drv = existing.Driver
	}
	if drv == "" {
		return nil, fieldErr("driver", "请选择部署方式")
	}
	fields, ok := s.fieldsOf(drv)
	if !ok {
		return nil, fieldErr("driver", "不认识的部署方式："+drv)
	}
	if in.Params == nil {
		in.Params = map[string]any{}
	}
	params, errs := driver.Clean(fields, in.Params)

	name, _ := params["name"].(string)
	if existing != nil && name != "" && name != existing.Name {
		errs = append(errs, driver.FieldError{Field: "name", Message: "应用标识创建后不能修改"})
	}
	if existing == nil && name != "" {
		if _, err := s.store.GetApp(ctx, project.Name, name); err == nil {
			errs = append(errs, driver.FieldError{Field: "name", Message: "项目里已有同名应用"})
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}

	var srv *model.Server
	if sn, _ := params["server"].(string); sn != "" {
		got, err := s.store.GetServer(ctx, sn)
		switch {
		case errors.Is(err, store.ErrNotFound):
			errs = append(errs, driver.FieldError{Field: "server", Message: "服务器 " + sn + " 不存在"})
		case err != nil:
			return nil, err
		default:
			srv = got
		}
	}

	credErrs, err := s.checkCredentialRefs(ctx, fields, params)
	if err != nil {
		return nil, err
	}
	errs = append(errs, credErrs...)

	secrets, err := s.store.SecretNames(ctx)
	if err != nil {
		return nil, err
	}
	reported := map[string]bool{}
	for _, r := range driver.Refs(fields, params) {
		key := r.Field + "\x00" + r.Name
		if r.Required && !secrets[r.Name] && !reported[key] {
			reported[key] = true
			errs = append(errs, driver.FieldError{Field: r.Field, Message: fmt.Sprintf("引用的 ${%s} 不存在，请先在「密钥与变量」里添加，或写成 ${%s:-默认值}", r.Name, r.Name)})
		}
	}

	if len(errs) > 0 {
		return nil, invalid(fmt.Sprintf("有 %d 处参数需要修改", len(errs)), errs...)
	}
	return &prepared{driver: drv, fields: fields, params: params, server: srv}, nil
}

// ValidateApp 只校验不保存。name 为空表示新增。
func (s *Service) ValidateApp(ctx context.Context, projectName, name string, in AppInput) error {
	project, err := s.GetProject(ctx, projectName)
	if err != nil {
		return err
	}
	var existing *model.App
	if name != "" {
		if existing, err = s.getApp(ctx, projectName, name); err != nil {
			return err
		}
	}
	_, err = s.prepare(ctx, project, existing, in)
	return err
}

// CreateApp 新增应用。
func (s *Service) CreateApp(ctx context.Context, projectName string, in AppInput) (*model.App, error) {
	project, err := s.GetProject(ctx, projectName)
	if err != nil {
		return nil, err
	}
	p, err := s.prepare(ctx, project, nil, in)
	if err != nil {
		return nil, err
	}
	if err := driver.Sensitive(p.fields, p.params, s.vault.Encrypt); err != nil {
		return nil, err
	}
	a := &model.App{
		ProjectID: project.ID, Name: p.params["name"].(string), Title: p.params["title"].(string),
		Driver: p.driver, ServerID: p.serverID(), Params: p.params,
	}
	if err := s.store.CreateApp(ctx, a); err != nil {
		return nil, wrapStore(err, "应用")
	}
	return s.GetApp(ctx, projectName, a.Name)
}

// UpdateApp 保存应用的新参数，生成新版本。
func (s *Service) UpdateApp(ctx context.Context, projectName, name string, in AppInput) (*model.App, error) {
	project, err := s.GetProject(ctx, projectName)
	if err != nil {
		return nil, err
	}
	existing, err := s.getApp(ctx, projectName, name)
	if err != nil {
		return nil, err
	}
	if in.Revision == 0 {
		return nil, fieldErr("revision", "请提供修改时基于的版本号 revision")
	}
	p, err := s.prepare(ctx, project, existing, in)
	if err != nil {
		return nil, err
	}
	if err := driver.Sensitive(p.fields, p.params, s.vault.Encrypt); err != nil {
		return nil, err
	}
	existing.Title, existing.ServerID, existing.Params = p.params["title"].(string), p.serverID(), p.params
	if err := s.store.UpdateApp(ctx, existing, in.Revision); err != nil {
		return nil, wrapStore(err, "应用")
	}
	return s.GetApp(ctx, projectName, name)
}

// getApp 读取应用，参数保持加密。
func (s *Service) getApp(ctx context.Context, projectName, name string) (*model.App, error) {
	a, err := s.store.GetApp(ctx, projectName, name)
	return a, wrapStore(err, "应用")
}

// GetApp 读取应用，敏感参数已解密。只给页面用；访问令牌读取时用 GetAppRedacted。
func (s *Service) GetApp(ctx context.Context, projectName, name string) (*model.App, error) {
	a, err := s.getApp(ctx, projectName, name)
	if err != nil {
		return nil, err
	}
	if err := s.decrypt(a.Driver, a.Params); err != nil {
		return nil, err
	}
	return a, nil
}

// redacted 是敏感参数在不返回内容时的占位值。
const redacted = "******"

// GetAppRedacted 读取应用，敏感参数（如配置文件内容）不返回内容。访问令牌读取时用。
func (s *Service) GetAppRedacted(ctx context.Context, projectName, name string) (*model.App, error) {
	a, err := s.getApp(ctx, projectName, name)
	if err != nil {
		return nil, err
	}
	s.redact(a.Driver, a.Params)
	return a, nil
}

// UpdateAppKeepSensitive 修改应用，但敏感参数没传或传的是占位值时保留原来的内容。
// 访问令牌读不到敏感内容，把读到的参数改一改再提交回来时，不能把配置文件内容清掉。
func (s *Service) UpdateAppKeepSensitive(ctx context.Context, projectName, name string, in AppInput) (*model.App, error) {
	existing, err := s.GetApp(ctx, projectName, name)
	if err != nil {
		return nil, err
	}
	if fields, ok := s.fieldsOf(existing.Driver); ok && in.Params != nil {
		keepSensitive(fields, in.Params, existing.Params)
	}
	if _, err := s.UpdateApp(ctx, projectName, name, in); err != nil {
		return nil, err
	}
	return s.GetAppRedacted(ctx, projectName, name)
}

// CreateAppRedacted 新增应用，返回时敏感参数不带内容。访问令牌新建时用。
func (s *Service) CreateAppRedacted(ctx context.Context, projectName string, in AppInput) (*model.App, error) {
	if fields, ok := s.fieldsOf(in.Driver); ok && in.Params != nil {
		keepSensitive(fields, in.Params, nil) // 占位值不是内容，去掉
	}
	a, err := s.CreateApp(ctx, projectName, in)
	if err != nil {
		return nil, err
	}
	s.redact(a.Driver, a.Params)
	return a, nil
}

// keepSensitive 把 in 里没传或是占位值的敏感参数换成 old 里原来的内容（old 没有就去掉）。
// 记录类参数（如配置文件）按 name 对应到原来的记录，没有 name 的按位置。
func keepSensitive(fields []driver.Field, in, old map[string]any) {
	for _, f := range fields {
		if f.Sensitive {
			if v, ok := in[f.Key].(string); !ok || v == redacted {
				if ov, has := old[f.Key]; has {
					in[f.Key] = ov
				} else {
					delete(in, f.Key)
				}
			}
			continue
		}
		if f.Type != driver.TypeRecords {
			continue
		}
		oldRecs := driver.Records(old, f.Key)
		for i, r := range driver.Records(in, f.Key) {
			var match map[string]any
			if n, _ := r["name"].(string); n != "" {
				for _, o := range oldRecs {
					if o["name"] == n {
						match = o
						break
					}
				}
			} else if i < len(oldRecs) {
				match = oldRecs[i]
			}
			keepSensitive(f.Fields, r, match)
		}
	}
}

// ListApps 返回项目下的应用；敏感参数不返回内容。
func (s *Service) ListApps(ctx context.Context, projectName string) ([]*model.App, error) {
	if projectName != "" {
		if _, err := s.GetProject(ctx, projectName); err != nil {
			return nil, err
		}
	}
	list, err := s.store.ListApps(ctx, projectName)
	if err != nil {
		return nil, err
	}
	for _, a := range list {
		s.redact(a.Driver, a.Params)
	}
	return list, nil
}

// DeleteApp 删除应用和它的修改记录。
func (s *Service) DeleteApp(ctx context.Context, projectName, name string) error {
	a, err := s.getApp(ctx, projectName, name)
	if err != nil {
		return err
	}
	if err := s.store.DeleteApp(ctx, a.ID); err != nil {
		return wrapStore(err, "应用")
	}
	s.removeManagedRepo(projectName, name)
	return nil
}

// ListRevisions 返回应用的修改记录（新的在前），敏感参数已解密。
func (s *Service) ListRevisions(ctx context.Context, projectName, name string) ([]*model.AppRevision, error) {
	a, err := s.getApp(ctx, projectName, name)
	if err != nil {
		return nil, err
	}
	revs, err := s.store.ListRevisions(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range revs {
		if err := s.decrypt(a.Driver, r.Params); err != nil {
			return nil, err
		}
	}
	return revs, nil
}

// RestoreRevision 用某个旧版本的参数保存一个新版本。
func (s *Service) RestoreRevision(ctx context.Context, projectName, name string, revision int) (*model.App, error) {
	revs, err := s.ListRevisions(ctx, projectName, name)
	if err != nil {
		return nil, err
	}
	for _, r := range revs {
		if r.Revision == revision {
			return s.UpdateApp(ctx, projectName, name, AppInput{Params: r.Params, Revision: revs[0].Revision})
		}
	}
	return nil, notFound("版本 %d 不存在", revision)
}

// DetectRepo 识别本机仓库。
func (s *Service) DetectRepo(ctx context.Context, path string) *detect.Repo {
	return s.repos.Detect(ctx, path)
}

func (s *Service) decrypt(driverName string, params map[string]any) error {
	fields, ok := s.fieldsOf(driverName)
	if !ok {
		return nil
	}
	return driver.Sensitive(fields, params, func(v string) (string, error) {
		if !vault.IsEncrypted(v) {
			return v, nil
		}
		return s.vault.Decrypt(v)
	})
}

// redact 把敏感参数的内容清空，只保留「有内容」这个事实。
func (s *Service) redact(driverName string, params map[string]any) {
	fields, ok := s.fieldsOf(driverName)
	if !ok {
		return
	}
	_ = driver.Sensitive(fields, params, func(string) (string, error) { return redacted, nil })
}
