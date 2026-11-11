package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"deployhub/internal/credential"
	"deployhub/internal/driver"
	"deployhub/internal/model"
	"deployhub/internal/remote"
	"deployhub/internal/store"
)

// ---------- 导入旧工具的部署历史 ----------

// HistoryImportResult 是导入部署历史的结果。
type HistoryImportResult struct {
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"` // 已经导入过的、只构建没部署的
	Unknown  []string `json:"unknown"` // 项目里没有对应应用的单元名（仍然导入，只是不关联应用）
}

// oldRecord 是旧部署工具的 logs/history.jsonl 里的一行（每行一个 JSON：unit、action、version、previous、result、server、operator、time、error）。
type oldRecord struct {
	Unit     string `json:"unit"`
	Action   string `json:"action"` // deploy / rollback / build
	Version  string `json:"version"`
	Previous string `json:"previous"`
	Result   string `json:"result"` // success / failed / rolled_back / built / dry_run
	Server   string `json:"server"`
	Operator string `json:"operator"`
	Time     string `json:"time"` // 2026-11-07 02:12:00（本地时间）
	Error    string `json:"error"`
}

// ImportHistory 把旧工具的部署历史导入为部署记录，单元名对应项目里的应用名。重复导入时跳过已经导入过的。
func (s *Service) ImportHistory(ctx context.Context, projectName, path string) (*HistoryImportResult, error) {
	if _, err := s.GetProject(ctx, projectName); err != nil {
		return nil, err
	}
	f, err := os.Open(remote.ExpandHome(strings.TrimSpace(path)))
	if err != nil {
		return nil, fieldErr("path", "打不开文件："+err.Error())
	}
	defer f.Close()
	apps, err := s.store.ListApps(ctx, projectName)
	if err != nil {
		return nil, err
	}
	byName := map[string]*model.App{}
	for _, a := range apps {
		byName[a.Name] = a
	}
	res := &HistoryImportResult{Unknown: []string{}}
	unknown := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var r oldRecord
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			return nil, invalid(fmt.Sprintf("第 %d 行不是有效的 JSON：%v", line, err))
		}
		status := map[string]string{"success": "success", "failed": "failed", "rolled_back": "rolled_back"}[r.Result]
		if (r.Action != "deploy" && r.Action != "rollback") || status == "" {
			res.Skipped++ // 只构建、dry-run 不算部署
			continue
		}
		at, err := time.ParseInLocation("2006-01-02 15:04:05", r.Time, time.Local)
		if err != nil {
			return nil, invalid(fmt.Sprintf("第 %d 行的时间格式不对：%s", line, r.Time))
		}
		initiator := "旧工具 " + r.Operator
		if dup, err := s.store.ImportedExists(ctx, initiator, at, projectName, r.Unit, r.Version, r.Action); err != nil {
			return nil, err
		} else if dup {
			res.Skipped++
			continue
		}
		u := &model.JobUnit{Project: projectName, App: r.Unit, Title: r.Unit, Driver: "", Server: r.Server,
			Version: r.Version, Previous: r.Previous, Status: status, Error: r.Error}
		if a := byName[r.Unit]; a != nil {
			u.AppID, u.Title, u.Driver, u.Server = &a.ID, a.Title, a.Driver, a.Server
		} else if !unknown[r.Unit] {
			unknown[r.Unit] = true
			res.Unknown = append(res.Unknown, r.Unit)
		}
		job := &model.Job{Kind: r.Action, Status: status, Initiator: initiator, Error: r.Error, CreatedAt: at, Units: []*model.JobUnit{u}}
		if err := s.store.ImportJob(ctx, job); err != nil {
			return nil, err
		}
		res.Imported++
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

// ---------- 导出和导入配置 ----------

// ExportFormat 是导出文件的格式标识。
const ExportFormat = "deployhub/v1"

// Export 是导出的配置：服务器、密钥（默认不含值）、项目和应用。
type Export struct {
	Format      string            `json:"format"`
	ExportedAt  time.Time         `json:"exportedAt"`
	Sensitive   bool              `json:"sensitive"` // 是否包含密钥的值和配置文件内容（明文）
	Servers     []ServerInput     `json:"servers"`
	Secrets     []ExportSecret    `json:"secrets"`
	Credentials []CredentialInput `json:"credentials"`
	Projects    []ExportProject   `json:"projects"`
}

// ExportSecret 是导出的密钥。
type ExportSecret struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Value       *string `json:"value,omitempty"`
}

// ExportProject 是导出的项目和它的应用。
type ExportProject struct {
	ProjectInput
	Apps []ExportApp `json:"apps"`
}

// ExportApp 是导出的应用。
type ExportApp struct {
	Driver string         `json:"driver"`
	Params map[string]any `json:"params"`
}

// ExportAll 导出全部配置。sensitive 为 false 时不导出密钥的值和配置文件的内容。
func (s *Service) ExportAll(ctx context.Context, sensitive bool) (*Export, error) {
	out := &Export{Format: ExportFormat, ExportedAt: s.now(), Sensitive: sensitive,
		Servers: []ServerInput{}, Secrets: []ExportSecret{}, Credentials: []CredentialInput{}, Projects: []ExportProject{}}
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	for _, srv := range servers {
		out.Servers = append(out.Servers, ServerInput{Name: srv.Name, Title: srv.Title, Host: srv.Host, Port: srv.Port,
			User: srv.User, KeyPath: srv.KeyPath, KeyCredential: srv.KeyCredential, SSHOptions: srv.SSHOptions})
	}
	secrets, err := s.store.ListSecrets(ctx)
	if err != nil {
		return nil, err
	}
	for _, sec := range secrets {
		e := ExportSecret{Name: sec.Name, Description: sec.Description}
		if sensitive {
			v, err := s.RevealSecret(ctx, sec.Name)
			if err != nil {
				return nil, err
			}
			e.Value = &v
		}
		out.Secrets = append(out.Secrets, e)
	}
	creds, err := s.store.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range creds {
		values := map[string]string{}
		for k, v := range c.Values {
			values[k] = v
		}
		if sensitive {
			secrets, err := s.decryptSecrets(c.SecretsEnc)
			if err != nil {
				return nil, err
			}
			for k, v := range secrets {
				values[k] = v
			}
		}
		out.Credentials = append(out.Credentials, CredentialInput{Name: c.Name, Kind: c.Kind, Title: c.Title, Values: values})
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		ep := ExportProject{ProjectInput: ProjectInput{Name: p.Name, Title: p.Title, Description: p.Description}, Apps: []ExportApp{}}
		apps, err := s.store.ListApps(ctx, p.Name)
		if err != nil {
			return nil, err
		}
		for _, a := range apps {
			if err := s.decrypt(a.Driver, a.Params); err != nil {
				return nil, err
			}
			if !sensitive {
				if fields, ok := s.fieldsOf(a.Driver); ok {
					_ = driver.Sensitive(fields, a.Params, func(string) (string, error) { return "", nil })
				}
			}
			ep.Apps = append(ep.Apps, ExportApp{Driver: a.Driver, Params: a.Params})
		}
		out.Projects = append(out.Projects, ep)
	}
	return out, nil
}

// ImportResult 是导入配置的结果。
type ImportResult struct {
	Created  []string `json:"created"`
	Skipped  []string `json:"skipped"` // 已存在的不覆盖
	Failed   []string `json:"failed"`
	Warnings []string `json:"warnings"`
}

// ImportAll 导入配置：按凭据、服务器、密钥、项目、应用的顺序新建（服务器可能用凭据里的 SSH 私钥），已存在的（同名）跳过，不覆盖。
func (s *Service) ImportAll(ctx context.Context, in *Export) (*ImportResult, error) {
	if in.Format != ExportFormat {
		return nil, invalid("不是 deployhub 导出的配置文件（format 应为 " + ExportFormat + "）")
	}
	res := &ImportResult{Created: []string{}, Skipped: []string{}, Failed: []string{}, Warnings: []string{}}
	exists := func(err error) bool {
		var e *Error
		return errors.As(err, &e) && e.Kind == KindConflict
	}
	report := func(what string, err error) {
		switch {
		case err == nil:
			res.Created = append(res.Created, what)
		case exists(err):
			res.Skipped = append(res.Skipped, what+"（已存在）")
		default:
			res.Failed = append(res.Failed, what+"："+describe(err))
		}
	}
	for _, c := range in.Credentials {
		what := "凭据 " + c.Name
		if _, err := s.store.GetCredential(ctx, c.Name); err == nil {
			res.Skipped = append(res.Skipped, what+"（已存在）")
			continue
		}
		missing := missingSensitive(c)
		_, err := s.createCredential(ctx, c, missing)
		report(what, err)
		if err == nil && missing {
			res.Warnings = append(res.Warnings, what+" 没有导出敏感字段的值（如 API Token、私钥），部署前需要在「凭据」里补上")
		}
	}
	for _, srv := range in.Servers {
		_, err := s.CreateServer(ctx, srv)
		report("服务器 "+srv.Name, err)
	}
	for _, sec := range in.Secrets {
		if sec.Value == nil {
			if _, err := s.store.SecretValue(ctx, sec.Name); errors.Is(err, store.ErrNotFound) {
				res.Warnings = append(res.Warnings, "密钥 "+sec.Name+" 没有导出值，需要在「密钥与变量」里手动添加")
			}
			continue
		}
		report("密钥 "+sec.Name, s.CreateSecret(ctx, SecretInput{Name: sec.Name, Value: sec.Value, Description: sec.Description}))
	}
	for _, p := range in.Projects {
		_, err := s.CreateProject(ctx, p.ProjectInput)
		report("项目 "+p.Name, err)
		if err != nil && !exists(err) {
			continue
		}
		for _, a := range p.Apps {
			name, _ := a.Params["name"].(string)
			what := "应用 " + p.Name + "/" + name
			if _, err := s.store.GetApp(ctx, p.Name, name); err == nil {
				res.Skipped = append(res.Skipped, what+"（已存在）")
				continue
			}
			_, err := s.CreateApp(ctx, p.Name, AppInput{Driver: a.Driver, Params: a.Params})
			report(what, err)
			if err == nil {
				for _, f := range driver.Records(a.Params, "config_files") {
					if src, _ := f["source"].(string); src != "server" {
						if c, _ := f["content"].(string); c == "" {
							res.Warnings = append(res.Warnings, fmt.Sprintf("%s 的配置文件 %v 没有内容，部署前需要在应用配置里填写", what, f["name"]))
						}
					}
				}
			}
		}
	}
	return res, nil
}

// missingSensitive 表示导出的凭据缺少敏感字段的值（导出时没选包含敏感内容）。
func missingSensitive(c CredentialInput) bool {
	kind, ok := credential.Get(c.Kind)
	if !ok {
		return false
	}
	for _, f := range kind.Fields {
		if f.Sensitive && f.Required && c.Values[f.Key] == "" {
			return true
		}
	}
	return false
}

// describe 把错误换成一句话，校验错误带上具体字段。
func describe(err error) string {
	var e *Error
	if errors.As(err, &e) && len(e.Fields) > 0 {
		parts := make([]string, len(e.Fields))
		for i, f := range e.Fields {
			parts[i] = f.Field + " " + f.Message
		}
		return strings.Join(parts, "；")
	}
	return err.Error()
}
