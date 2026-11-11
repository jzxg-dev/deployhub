package core

import (
	"context"

	"deployhub/internal/driver"
	"deployhub/internal/model"
)

// ProjectInput 是新增或修改项目时提交的内容。
type ProjectInput struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (in *ProjectInput) validate(create bool) *Error {
	var fields []driver.FieldError
	if create {
		if e := checkName("name", in.Name); e != nil {
			fields = append(fields, e.Fields...)
		}
	}
	if in.Title == "" {
		fields = append(fields, driver.FieldError{Field: "title", Message: "请填写名称"})
	}
	if len(in.Description) > 1000 {
		fields = append(fields, driver.FieldError{Field: "description", Message: "说明最多 1000 个字符"})
	}
	if len(fields) > 0 {
		return invalid("项目信息有误", fields...)
	}
	return nil
}

// ListProjects 返回所有项目。
func (s *Service) ListProjects(ctx context.Context) ([]*model.Project, error) {
	return s.store.ListProjects(ctx)
}

// GetProject 按标识读取项目。
func (s *Service) GetProject(ctx context.Context, name string) (*model.Project, error) {
	p, err := s.store.GetProject(ctx, name)
	return p, wrapStore(err, "项目")
}

// CreateProject 新增项目。
func (s *Service) CreateProject(ctx context.Context, in ProjectInput) (*model.Project, error) {
	in.Name, in.Title, in.Description = trim(in.Name), trim(in.Title), trim(in.Description)
	if e := in.validate(true); e != nil {
		return nil, e
	}
	p := &model.Project{Name: in.Name, Title: in.Title, Description: in.Description}
	if err := s.store.CreateProject(ctx, p); err != nil {
		return nil, wrapStore(err, "项目")
	}
	return s.GetProject(ctx, p.Name)
}

// UpdateProject 修改项目的名称和说明（标识不能改）。
func (s *Service) UpdateProject(ctx context.Context, name string, in ProjectInput) (*model.Project, error) {
	in.Title, in.Description = trim(in.Title), trim(in.Description)
	if n := trim(in.Name); n != "" && n != name {
		return nil, fieldErr("name", "项目标识创建后不能修改")
	}
	if e := in.validate(false); e != nil {
		return nil, e
	}
	if err := s.store.UpdateProject(ctx, &model.Project{Name: name, Title: in.Title, Description: in.Description}); err != nil {
		return nil, wrapStore(err, "项目")
	}
	return s.GetProject(ctx, name)
}

// DeleteProject 删除项目；还有应用时拒绝。
func (s *Service) DeleteProject(ctx context.Context, name string) error {
	p, err := s.GetProject(ctx, name)
	if err != nil {
		return err
	}
	if p.AppCount > 0 {
		return conflict("项目里还有 %d 个应用，先删除它们", p.AppCount)
	}
	return wrapStore(s.store.DeleteProject(ctx, name), "项目")
}
