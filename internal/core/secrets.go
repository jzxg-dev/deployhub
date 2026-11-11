package core

import (
	"context"
	"sort"
	"strings"

	"deployhub/internal/driver"
	"deployhub/internal/model"
)

// SecretInput 是新增或修改密钥时提交的内容。修改时 Value 为 nil 表示不改值。
type SecretInput struct {
	Name        string  `json:"name"`
	Value       *string `json:"value"`
	Description string  `json:"description"`
}

const maxSecretSize = 64 << 10

// ListSecrets 返回所有密钥（不含值），带引用它的应用。
func (s *Service) ListSecrets(ctx context.Context) ([]*model.Secret, error) {
	list, err := s.store.ListSecrets(ctx)
	if err != nil {
		return nil, err
	}
	usage, err := s.secretUsage(ctx)
	if err != nil {
		return nil, err
	}
	for _, sec := range list {
		if u := usage[sec.Name]; u != nil {
			sec.UsedBy = u
		}
	}
	return list, nil
}

// secretUsage 返回每个密钥被哪些应用引用（项目/应用）。
func (s *Service) secretUsage(ctx context.Context) (map[string][]string, error) {
	apps, err := s.store.ListApps(ctx, "")
	if err != nil {
		return nil, err
	}
	usage := map[string][]string{}
	for _, a := range apps {
		fields, ok := s.fieldsOf(a.Driver)
		if !ok {
			continue
		}
		for _, name := range driver.RefNames(driver.Refs(fields, a.Params)) {
			usage[name] = append(usage[name], a.Project+"/"+a.Name)
		}
	}
	for _, u := range usage {
		sort.Strings(u)
	}
	return usage, nil
}

func validateSecret(in *SecretInput, create bool) *Error {
	var fields []driver.FieldError
	if create {
		switch {
		case in.Name == "":
			fields = append(fields, driver.FieldError{Field: "name", Message: "请填写名称"})
		case !secretNameRe.MatchString(in.Name):
			fields = append(fields, driver.FieldError{Field: "name", Message: "大写字母开头，只能包含大写字母、数字和下划线，如 PAYMENT_API_URL"})
		}
		if in.Value == nil {
			fields = append(fields, driver.FieldError{Field: "value", Message: "请填写值"})
		}
	}
	if in.Value != nil && len(*in.Value) > maxSecretSize {
		fields = append(fields, driver.FieldError{Field: "value", Message: "值太长，最多 64 KB"})
	}
	if len(in.Description) > 500 {
		fields = append(fields, driver.FieldError{Field: "description", Message: "说明最多 500 个字符"})
	}
	if len(fields) > 0 {
		return invalid("密钥信息有误", fields...)
	}
	return nil
}

// CreateSecret 新增密钥。
func (s *Service) CreateSecret(ctx context.Context, in SecretInput) error {
	in.Name, in.Description = trim(in.Name), trim(in.Description)
	if e := validateSecret(&in, true); e != nil {
		return e
	}
	enc, err := s.vault.Encrypt(*in.Value)
	if err != nil {
		return err
	}
	return wrapStore(s.store.CreateSecret(ctx, in.Name, enc, in.Description), "密钥")
}

// UpdateSecret 修改密钥的值或说明（名称不能改）。
func (s *Service) UpdateSecret(ctx context.Context, name string, in SecretInput) error {
	in.Description = trim(in.Description)
	if n := trim(in.Name); n != "" && n != name {
		return fieldErr("name", "密钥名称创建后不能修改，请新建一个再删除旧的")
	}
	if e := validateSecret(&in, false); e != nil {
		return e
	}
	var enc *string
	if in.Value != nil {
		v, err := s.vault.Encrypt(*in.Value)
		if err != nil {
			return err
		}
		enc = &v
	}
	return wrapStore(s.store.UpdateSecret(ctx, name, enc, in.Description), "密钥")
}

// RevealSecret 返回密钥的明文值。只给页面用，访问令牌不能调用。
func (s *Service) RevealSecret(ctx context.Context, name string) (string, error) {
	enc, err := s.store.SecretValue(ctx, name)
	if err != nil {
		return "", wrapStore(err, "密钥")
	}
	return s.vault.Decrypt(enc)
}

// DeleteSecret 删除密钥；还有应用引用时拒绝。
func (s *Service) DeleteSecret(ctx context.Context, name string) error {
	usage, err := s.secretUsage(ctx)
	if err != nil {
		return err
	}
	if u := usage[name]; len(u) > 0 {
		return conflict("还有应用引用 ${%s}：%s，先修改这些应用", name, strings.Join(u, "、"))
	}
	return wrapStore(s.store.DeleteSecret(ctx, name), "密钥")
}
