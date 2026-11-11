package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"deployhub/internal/core"
)

func (s *server) importHistory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Project string `json:"project"`
		Path    string `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := s.core(r).ImportHistory(r.Context(), in.Project, in.Path)
	s.respond(w, r, http.StatusOK, res, err)
}

func (s *server) exportAll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Sensitive bool `json:"sensitive"`
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := s.core(r).ExportAll(r.Context(), in.Sensitive)
	s.respond(w, r, http.StatusOK, res, err)
}

func (s *server) importAll(w http.ResponseWriter, r *http.Request) {
	var in core.Export
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<20))
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "不是有效的 JSON："+err.Error())
		return
	}
	res, err := s.core(r).ImportAll(r.Context(), &in)
	s.respond(w, r, http.StatusOK, res, err)
}

const maxBody = 2 << 20

// decode 读取 JSON 请求体，不认识的字段报错。
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "请求格式不对："+err.Error())
		return false
	}
	return true
}

// fail 把业务错误换成 HTTP 响应。
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var e *core.Error
	if errors.As(err, &e) {
		status, code := http.StatusInternalServerError, "error"
		switch e.Kind {
		case core.KindInvalid:
			status, code = http.StatusUnprocessableEntity, "invalid"
		case core.KindNotFound:
			status, code = http.StatusNotFound, "not_found"
		case core.KindConflict:
			status, code = http.StatusConflict, "conflict"
		case core.KindFailed:
			status, code = http.StatusBadGateway, "failed"
		}
		writeError(w, status, code, e.Message, e.Fields...)
		return
	}
	s.Logger.Error("处理请求失败", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "服务内部错误："+err.Error())
}

func (s *server) respond(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

func (s *server) ok(w http.ResponseWriter, r *http.Request, err error) {
	s.respond(w, r, http.StatusOK, map[string]bool{"ok": true}, err)
}

// ---------- 服务器 ----------

func (s *server) listServers(w http.ResponseWriter, r *http.Request) {
	list, err := s.core(r).ListServers(r.Context())
	s.respond(w, r, http.StatusOK, list, err)
}

func (s *server) getServer(w http.ResponseWriter, r *http.Request) {
	srv, err := s.core(r).GetServer(r.Context(), r.PathValue("name"))
	s.respond(w, r, http.StatusOK, srv, err)
}

func (s *server) createServer(w http.ResponseWriter, r *http.Request) {
	var in core.ServerInput
	if !decode(w, r, &in) {
		return
	}
	srv, err := s.core(r).CreateServer(r.Context(), in)
	s.respond(w, r, http.StatusCreated, srv, err)
}

func (s *server) updateServer(w http.ResponseWriter, r *http.Request) {
	var in core.ServerInput
	if !decode(w, r, &in) {
		return
	}
	srv, err := s.core(r).UpdateServer(r.Context(), r.PathValue("name"), in)
	s.respond(w, r, http.StatusOK, srv, err)
}

func (s *server) deleteServer(w http.ResponseWriter, r *http.Request) {
	s.ok(w, r, s.core(r).DeleteServer(r.Context(), r.PathValue("name")))
}

func (s *server) testServerInput(w http.ResponseWriter, r *http.Request) {
	var in core.ServerInput
	if !decode(w, r, &in) {
		return
	}
	res, err := s.core(r).TestServerInput(r.Context(), in)
	s.respond(w, r, http.StatusOK, res, err)
}

func (s *server) testServer(w http.ResponseWriter, r *http.Request) {
	res, err := s.core(r).TestServer(r.Context(), r.PathValue("name"))
	s.respond(w, r, http.StatusOK, res, err)
}

// ---------- 密钥与变量 ----------

func (s *server) listSecrets(w http.ResponseWriter, r *http.Request) {
	list, err := s.core(r).ListSecrets(r.Context())
	s.respond(w, r, http.StatusOK, list, err)
}

func (s *server) createSecret(w http.ResponseWriter, r *http.Request) {
	var in core.SecretInput
	if !decode(w, r, &in) {
		return
	}
	err := s.core(r).CreateSecret(r.Context(), in)
	s.respond(w, r, http.StatusCreated, map[string]bool{"ok": true}, err)
}

func (s *server) updateSecret(w http.ResponseWriter, r *http.Request) {
	var in core.SecretInput
	if !decode(w, r, &in) {
		return
	}
	s.ok(w, r, s.core(r).UpdateSecret(r.Context(), r.PathValue("name"), in))
}

func (s *server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	s.ok(w, r, s.core(r).DeleteSecret(r.Context(), r.PathValue("name")))
}

func (s *server) revealSecret(w http.ResponseWriter, r *http.Request) {
	v, err := s.core(r).RevealSecret(r.Context(), r.PathValue("name"))
	s.respond(w, r, http.StatusOK, map[string]string{"value": v}, err)
}

// ---------- 项目 ----------

func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.core(r).ListProjects(r.Context())
	s.respond(w, r, http.StatusOK, list, err)
}

func (s *server) getProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	p, err := s.core(r).GetProject(r.Context(), name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	apps, err := s.core(r).ListApps(r.Context(), name)
	s.respond(w, r, http.StatusOK, map[string]any{"project": p, "apps": apps}, err)
}

func (s *server) createProject(w http.ResponseWriter, r *http.Request) {
	var in core.ProjectInput
	if !decode(w, r, &in) {
		return
	}
	p, err := s.core(r).CreateProject(r.Context(), in)
	s.respond(w, r, http.StatusCreated, p, err)
}

func (s *server) updateProject(w http.ResponseWriter, r *http.Request) {
	var in core.ProjectInput
	if !decode(w, r, &in) {
		return
	}
	p, err := s.core(r).UpdateProject(r.Context(), r.PathValue("project"), in)
	s.respond(w, r, http.StatusOK, p, err)
}

func (s *server) deleteProject(w http.ResponseWriter, r *http.Request) {
	s.ok(w, r, s.core(r).DeleteProject(r.Context(), r.PathValue("project")))
}

// ---------- 应用 ----------

func (s *server) listAllApps(w http.ResponseWriter, r *http.Request) {
	list, err := s.core(r).ListApps(r.Context(), "")
	s.respond(w, r, http.StatusOK, list, err)
}

func (s *server) getApp(w http.ResponseWriter, r *http.Request) {
	// 访问令牌读不到敏感参数的内容（如配置文件）
	get := s.core(r).GetApp
	if s.token(r) != nil {
		get = s.core(r).GetAppRedacted
	}
	a, err := get(r.Context(), r.PathValue("project"), r.PathValue("app"))
	s.respond(w, r, http.StatusOK, a, err)
}

func (s *server) createApp(w http.ResponseWriter, r *http.Request) {
	var in core.AppInput
	if !decode(w, r, &in) {
		return
	}
	create := s.core(r).CreateApp
	if s.token(r) != nil {
		create = s.core(r).CreateAppRedacted
	}
	a, err := create(r.Context(), r.PathValue("project"), in)
	s.respond(w, r, http.StatusCreated, a, err)
}

func (s *server) updateApp(w http.ResponseWriter, r *http.Request) {
	var in core.AppInput
	if !decode(w, r, &in) {
		return
	}
	// 访问令牌读到的敏感参数是占位值，提交回来时保留原来的内容
	update := s.core(r).UpdateApp
	if s.token(r) != nil {
		update = s.core(r).UpdateAppKeepSensitive
	}
	a, err := update(r.Context(), r.PathValue("project"), r.PathValue("app"), in)
	s.respond(w, r, http.StatusOK, a, err)
}

func (s *server) deleteApp(w http.ResponseWriter, r *http.Request) {
	s.ok(w, r, s.core(r).DeleteApp(r.Context(), r.PathValue("project"), r.PathValue("app")))
}

func (s *server) validateNewApp(w http.ResponseWriter, r *http.Request) {
	var in core.AppInput
	if !decode(w, r, &in) {
		return
	}
	s.ok(w, r, s.core(r).ValidateApp(r.Context(), r.PathValue("project"), "", in))
}

func (s *server) validateApp(w http.ResponseWriter, r *http.Request) {
	var in core.AppInput
	if !decode(w, r, &in) {
		return
	}
	s.ok(w, r, s.core(r).ValidateApp(r.Context(), r.PathValue("project"), r.PathValue("app"), in))
}

func (s *server) listRevisions(w http.ResponseWriter, r *http.Request) {
	revs, err := s.core(r).ListRevisions(r.Context(), r.PathValue("project"), r.PathValue("app"))
	s.respond(w, r, http.StatusOK, revs, err)
}

func (s *server) restoreRevision(w http.ResponseWriter, r *http.Request) {
	rev, err := strconv.Atoi(r.PathValue("rev"))
	if err != nil || rev <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "版本号不对")
		return
	}
	a, err := s.core(r).RestoreRevision(r.Context(), r.PathValue("project"), r.PathValue("app"), rev)
	s.respond(w, r, http.StatusOK, a, err)
}

func (s *server) checkApp(w http.ResponseWriter, r *http.Request) {
	res, err := s.core(r).CheckApp(r.Context(), r.PathValue("project"), r.PathValue("app"))
	s.respond(w, r, http.StatusOK, res, err)
}

// appRepo 返回应用仓库当前的分支和提交（不联网）。
func (s *server) appRepo(w http.ResponseWriter, r *http.Request) {
	repo, err := s.core(r).AppRepoInfo(r.Context(), r.PathValue("project"), r.PathValue("app"))
	s.respond(w, r, http.StatusOK, repo, err)
}

func (s *server) previewApp(w http.ResponseWriter, r *http.Request) {
	var in core.AppInput
	if !decode(w, r, &in) {
		return
	}
	res, err := s.core(r).PreviewApp(r.Context(), r.PathValue("project"), r.PathValue("app"), in)
	s.respond(w, r, http.StatusOK, res, err)
}

func (s *server) detectRepo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	writeJSON(w, http.StatusOK, s.core(r).DetectRepo(r.Context(), in.Path))
}

// ---------- 凭据 ----------

func (s *server) listCredentials(w http.ResponseWriter, r *http.Request) {
	list, err := s.core(r).ListCredentials(r.Context())
	s.respond(w, r, http.StatusOK, list, err)
}

func (s *server) getCredential(w http.ResponseWriter, r *http.Request) {
	c, err := s.core(r).GetCredential(r.Context(), r.PathValue("name"))
	s.respond(w, r, http.StatusOK, c, err)
}

func (s *server) createCredential(w http.ResponseWriter, r *http.Request) {
	var in core.CredentialInput
	if !decode(w, r, &in) {
		return
	}
	c, err := s.core(r).CreateCredential(r.Context(), in)
	s.respond(w, r, http.StatusCreated, c, err)
}

func (s *server) updateCredential(w http.ResponseWriter, r *http.Request) {
	var in core.CredentialInput
	if !decode(w, r, &in) {
		return
	}
	c, err := s.core(r).UpdateCredential(r.Context(), r.PathValue("name"), in)
	s.respond(w, r, http.StatusOK, c, err)
}

func (s *server) deleteCredential(w http.ResponseWriter, r *http.Request) {
	s.ok(w, r, s.core(r).DeleteCredential(r.Context(), r.PathValue("name")))
}

// testCredentialInput 验证表单里还没保存的凭据；带 ?name= 时是修改已有凭据，没填的 Token 用已保存的。
func (s *server) testCredentialInput(w http.ResponseWriter, r *http.Request) {
	var in core.CredentialInput
	if !decode(w, r, &in) {
		return
	}
	info, err := s.core(r).TestCredentialInput(r.Context(), r.URL.Query().Get("name"), in)
	s.respond(w, r, http.StatusOK, map[string]any{"info": info}, err)
}

func (s *server) testCredential(w http.ResponseWriter, r *http.Request) {
	c, err := s.core(r).TestCredential(r.Context(), r.PathValue("name"))
	s.respond(w, r, http.StatusOK, c, err)
}
