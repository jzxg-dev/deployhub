package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"deployhub/internal/credential"
	"deployhub/internal/driver"
	"deployhub/internal/model"
	"deployhub/internal/store"
)

// CredentialInput 是新增或修改凭据时提交的内容。修改时敏感字段留空表示不改。
type CredentialInput struct {
	Name   string            `json:"name"`
	Kind   string            `json:"kind"`
	Title  string            `json:"title"`
	Values map[string]string `json:"values"`
}

// credentialKind 是参数的凭据种类：选项来源是 credentials:<种类> 时返回种类。
func credentialKind(f driver.Field) (string, bool) {
	return strings.CutPrefix(f.OptionsFrom, "credentials:")
}

// ListCredentials 返回所有凭据（不含敏感字段的值），带选择了它的应用。
func (s *Service) ListCredentials(ctx context.Context) ([]*model.Credential, error) {
	rows, err := s.store.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}
	usage, err := s.credentialUsage(ctx)
	if err != nil {
		return nil, err
	}
	list := make([]*model.Credential, len(rows))
	for i, r := range rows {
		list[i] = s.publicCredential(r)
		if u := usage[r.Name]; u != nil {
			list[i].UsedBy = u
		}
	}
	return list, nil
}

// GetCredential 读取凭据（不含敏感字段的值）。
func (s *Service) GetCredential(ctx context.Context, name string) (*model.Credential, error) {
	r, err := s.store.GetCredential(ctx, name)
	if err != nil {
		return nil, wrapStore(err, "凭据")
	}
	c := s.publicCredential(r)
	usage, err := s.credentialUsage(ctx)
	if err != nil {
		return nil, err
	}
	if u := usage[name]; u != nil {
		c.UsedBy = u
	}
	return c, nil
}

// publicCredential 去掉加密内容，只列出哪些敏感字段已经有值。
func (s *Service) publicCredential(r *store.CredentialRow) *model.Credential {
	c := r.Credential
	if secrets, err := s.decryptSecrets(r.SecretsEnc); err == nil {
		for k, v := range secrets {
			if v != "" {
				c.Secrets = append(c.Secrets, k)
			}
		}
		sort.Strings(c.Secrets)
	}
	return &c
}

func (s *Service) decryptSecrets(enc string) (map[string]string, error) {
	out := map[string]string{}
	if enc == "" {
		return out, nil
	}
	plain, err := s.vault.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(plain), &out); err != nil {
		return nil, fmt.Errorf("凭据的加密内容格式不对：%w", err)
	}
	return out, nil
}

// credentialUsage 返回每个凭据被谁选择：应用（项目/应用）和服务器（服务器 <名称>）。
func (s *Service) credentialUsage(ctx context.Context) (map[string][]string, error) {
	apps, err := s.store.ListApps(ctx, "")
	if err != nil {
		return nil, err
	}
	usage := map[string][]string{}
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	for _, srv := range servers {
		if srv.KeyCredential != "" {
			usage[srv.KeyCredential] = append(usage[srv.KeyCredential], "服务器 "+srv.Name)
		}
	}
	for _, a := range apps {
		fields, ok := s.fieldsOf(a.Driver)
		if !ok {
			continue
		}
		for _, f := range fields {
			if _, ok := credentialKind(f); !ok {
				continue
			}
			if n, _ := a.Params[f.Key].(string); n != "" {
				usage[n] = append(usage[n], a.Project+"/"+a.Name)
			}
		}
	}
	for _, u := range usage {
		sort.Strings(u)
	}
	return usage, nil
}

// cleanCredential 校验凭据：种类存在、字段符合声明。existing 不为空时，没填的敏感字段沿用原来的值。
// lenient 为 true 时敏感字段可以不填（导入没有敏感内容的配置时，先建出来，之后再补）。
// 返回不敏感字段和敏感字段。
func (s *Service) cleanCredential(in *CredentialInput, existing map[string]string, lenient bool) (credential.Kind, map[string]string, map[string]string, *Error) {
	kind, ok := credential.Get(in.Kind)
	if !ok {
		return kind, nil, nil, fieldErr("kind", "请选择凭据种类")
	}
	raw := map[string]any{}
	for k, v := range in.Values {
		raw[k] = v
	}
	for _, f := range kind.Fields {
		if v, _ := raw[f.Key].(string); f.Sensitive && strings.TrimSpace(v) == "" && existing[f.Key] != "" {
			raw[f.Key] = existing[f.Key]
		}
	}
	fields := kind.Fields
	if lenient {
		fields = make([]driver.Field, len(kind.Fields))
		for i, f := range kind.Fields {
			if f.Sensitive {
				f.Required = false
			}
			fields[i] = f
		}
	}
	cleaned, errs := driver.Clean(fields, raw)
	if len(in.Title) > 100 {
		errs = append(errs, driver.FieldError{Field: "title", Message: "名称最多 100 个字符"})
	}
	if len(errs) > 0 {
		return kind, nil, nil, invalid("凭据信息有误", errs...)
	}
	plain, secret := map[string]string{}, map[string]string{}
	for _, f := range kind.Fields {
		v, _ := cleaned[f.Key].(string)
		if f.Sensitive {
			secret[f.Key] = v
		} else if v != "" {
			plain[f.Key] = v
		}
	}
	return kind, plain, secret, nil
}

func (s *Service) encryptSecrets(secret map[string]string) (string, error) {
	b, err := json.Marshal(secret)
	if err != nil {
		return "", err
	}
	return s.vault.Encrypt(string(b))
}

// CreateCredential 新增凭据。
func (s *Service) CreateCredential(ctx context.Context, in CredentialInput) (*model.Credential, error) {
	return s.createCredential(ctx, in, false)
}

func (s *Service) createCredential(ctx context.Context, in CredentialInput, lenient bool) (*model.Credential, error) {
	in.Name, in.Title = trim(in.Name), trim(in.Title)
	var fields []driver.FieldError
	if e := checkName("name", in.Name); e != nil {
		fields = append(fields, e.Fields...)
	}
	kind, plain, secret, e := s.cleanCredential(&in, nil, lenient)
	if e != nil {
		fields = append(fields, e.Fields...)
	}
	if len(fields) > 0 {
		return nil, invalid("凭据信息有误", fields...)
	}
	if e := s.checkLocalCredential(ctx, kind, plain, secret); e != nil {
		return nil, e
	}
	enc, err := s.encryptSecrets(secret)
	if err != nil {
		return nil, err
	}
	row := &store.CredentialRow{SecretsEnc: enc}
	row.Name, row.Kind, row.Title, row.Values = in.Name, in.Kind, in.Title, plain
	if err := s.store.CreateCredential(ctx, row); err != nil {
		return nil, wrapStore(err, "凭据")
	}
	return s.GetCredential(ctx, in.Name)
}

// UpdateCredential 修改凭据；名称和种类不能改，敏感字段留空表示不改。
func (s *Service) UpdateCredential(ctx context.Context, name string, in CredentialInput) (*model.Credential, error) {
	row, err := s.store.GetCredential(ctx, name)
	if err != nil {
		return nil, wrapStore(err, "凭据")
	}
	if n := trim(in.Name); n != "" && n != name {
		return nil, fieldErr("name", "凭据标识创建后不能修改，请新建一个再删除旧的")
	}
	if in.Kind != "" && in.Kind != row.Kind {
		return nil, fieldErr("kind", "凭据种类创建后不能修改")
	}
	in.Kind, in.Title = row.Kind, trim(in.Title)
	existing, err := s.decryptSecrets(row.SecretsEnc)
	if err != nil {
		return nil, err
	}
	kind, plain, secret, e := s.cleanCredential(&in, existing, false)
	if e != nil {
		return nil, e
	}
	if e := s.checkLocalCredential(ctx, kind, plain, secret); e != nil {
		return nil, e
	}
	enc, err := s.encryptSecrets(secret)
	if err != nil {
		return nil, err
	}
	// 私钥换了，旧私钥的临时文件不再需要
	s.removeKeyFile(existing[credential.SSHKeyField])
	row.Title, row.Values, row.SecretsEnc = in.Title, plain, enc
	if err := s.store.UpdateCredential(ctx, row); err != nil {
		return nil, wrapStore(err, "凭据")
	}
	return s.GetCredential(ctx, name)
}

// checkLocalCredential 对只在本机验证的凭据种类（如 SSH 私钥）在保存前验证，格式不对不让保存。
// 敏感字段都没填时跳过（导入没有敏感内容的配置）。
func (s *Service) checkLocalCredential(ctx context.Context, kind credential.Kind, plain, secret map[string]string) *Error {
	if !kind.Local {
		return nil
	}
	values, filled := map[string]string{}, false
	for k, v := range plain {
		values[k] = v
	}
	for k, v := range secret {
		values[k] = v
		filled = filled || v != ""
	}
	if !filled {
		return nil
	}
	ctx, cancel := ctxTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := kind.Test(ctx, values); err != nil {
		for _, f := range kind.Fields {
			if f.Sensitive {
				return fieldErr(f.Key, err.Error())
			}
		}
		return invalid(err.Error())
	}
	return nil
}

// DeleteCredential 删除凭据；还有应用或服务器选择它时拒绝。
func (s *Service) DeleteCredential(ctx context.Context, name string) error {
	usage, err := s.credentialUsage(ctx)
	if err != nil {
		return err
	}
	if u := usage[name]; len(u) > 0 {
		return conflict("凭据 %s 还在用：%s，先把它们改成别的凭据", name, strings.Join(u, "、"))
	}
	if row, err := s.store.GetCredential(ctx, name); err == nil {
		if secrets, err := s.decryptSecrets(row.SecretsEnc); err == nil {
			s.removeKeyFile(secrets[credential.SSHKeyField])
		}
	}
	return wrapStore(s.store.DeleteCredential(ctx, name), "凭据")
}

// TestCredentialInput 验证还没保存的凭据（只读）。name 不为空时是修改已有凭据，没填的敏感字段用已保存的值。
func (s *Service) TestCredentialInput(ctx context.Context, name string, in CredentialInput) ([]model.InfoItem, error) {
	existing := map[string]string{}
	if name != "" {
		row, err := s.store.GetCredential(ctx, name)
		if err != nil {
			return nil, wrapStore(err, "凭据")
		}
		in.Kind = row.Kind
		if existing, err = s.decryptSecrets(row.SecretsEnc); err != nil {
			return nil, err
		}
	}
	kind, plain, secret, e := s.cleanCredential(&in, existing, false)
	if e != nil {
		return nil, e
	}
	for k, v := range secret {
		plain[k] = v
	}
	return s.testCredential(ctx, kind, plain)
}

func (s *Service) testCredential(ctx context.Context, kind credential.Kind, values map[string]string) ([]model.InfoItem, error) {
	ctx, cancel := ctxTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := kind.Test(ctx, values)
	if err != nil {
		return nil, &Error{Kind: KindFailed, Message: err.Error()}
	}
	return info, nil
}

// TestCredential 验证已保存的凭据并保存结果。
func (s *Service) TestCredential(ctx context.Context, name string) (*model.Credential, error) {
	kindName, values, err := s.CredentialValues(ctx, name)
	if err != nil {
		return nil, err
	}
	kind, ok := credential.Get(kindName)
	if !ok {
		return nil, invalid("不认识的凭据种类：" + kindName)
	}
	info, err := s.testCredential(ctx, kind, values)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetCredentialInfo(ctx, name, info, s.now()); err != nil {
		return nil, err
	}
	return s.GetCredential(ctx, name)
}

// CredentialValues 返回凭据的种类和全部字段（敏感字段是明文），给部署用。
func (s *Service) CredentialValues(ctx context.Context, name string) (string, map[string]string, error) {
	row, err := s.store.GetCredential(ctx, name)
	if err != nil {
		return "", nil, wrapStore(err, "凭据 "+name+" ")
	}
	secrets, err := s.decryptSecrets(row.SecretsEnc)
	if err != nil {
		return "", nil, err
	}
	values := map[string]string{}
	for k, v := range row.Values {
		values[k] = v
	}
	for k, v := range secrets {
		values[k] = v
	}
	return row.Kind, values, nil
}

// credsFor 读取参数里选择的凭据：参数键 → 凭据字段。凭据不存在或种类不对时报错。
func (s *Service) credsFor(ctx context.Context, fields []driver.Field, params map[string]any) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	for _, f := range fields {
		want, ok := credentialKind(f)
		if !ok {
			continue
		}
		name, _ := params[f.Key].(string)
		if name == "" {
			continue
		}
		kind, values, err := s.CredentialValues(ctx, name)
		if err != nil {
			return nil, err
		}
		if kind != want {
			return nil, invalid(fmt.Sprintf("「%s」选择的凭据 %s 不是 %s", f.Label, name, kindTitle(want)))
		}
		out[f.Key] = values
	}
	return out, nil
}

// CredentialSecrets 返回凭据里敏感字段的值，用于在日志里遮盖。
func CredentialSecrets(creds map[string]map[string]string) []string {
	var out []string
	for _, values := range creds {
		for k, v := range values {
			if isSensitiveCredentialField(k) && v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// isSensitiveCredentialField 判断字段在任一凭据种类里是不是敏感字段。
func isSensitiveCredentialField(key string) bool {
	for _, k := range credential.Kinds() {
		for _, f := range k.Fields {
			if f.Key == key && f.Sensitive {
				return true
			}
		}
	}
	return false
}

func kindTitle(name string) string {
	if k, ok := credential.Get(name); ok {
		return k.Title
	}
	return name
}

// checkCredentialRefs 校验应用参数里选择的凭据存在且种类正确。
func (s *Service) checkCredentialRefs(ctx context.Context, fields []driver.Field, params map[string]any) ([]driver.FieldError, error) {
	var errs []driver.FieldError
	for _, f := range fields {
		want, ok := credentialKind(f)
		if !ok {
			continue
		}
		name, _ := params[f.Key].(string)
		if name == "" {
			continue
		}
		row, err := s.store.GetCredential(ctx, name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			errs = append(errs, driver.FieldError{Field: f.Key, Message: "凭据 " + name + " 不存在，请先在「凭据」里添加"})
		case err != nil:
			return nil, err
		case row.Kind != want:
			errs = append(errs, driver.FieldError{Field: f.Key, Message: fmt.Sprintf("凭据 %s 不是%s", name, kindTitle(want))})
		}
	}
	return errs, nil
}
