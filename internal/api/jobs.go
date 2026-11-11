package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"deployhub/internal/core"
	"deployhub/internal/engine"
	"deployhub/internal/store"
)

// statusAll 返回所有应用（或 ?project= 指定项目）的部署状态，要连每台服务器，可能要几秒。
func (s *server) statusAll(w http.ResponseWriter, r *http.Request) {
	list, err := s.engine(r).StatusAll(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// failJob 把引擎的错误换成 HTTP 响应：应用正在部署 409，业务错误按类别，其余是请求本身有问题（400）。
func (s *server) failJob(w http.ResponseWriter, r *http.Request, err error) {
	var busy *engine.ErrBusy
	var pending *engine.ErrPending
	var ce *core.Error
	switch {
	case errors.As(err, &busy):
		writeError(w, http.StatusConflict, "busy", err.Error())
	case errors.As(err, &pending):
		writeError(w, http.StatusConflict, "pending_approval", err.Error())
	case errors.Is(err, engine.ErrNotPending):
		writeError(w, http.StatusConflict, "not_pending", err.Error())
	case errors.As(err, &ce):
		s.fail(w, r, err)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "任务不存在")
	default:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	}
}

func parseRefs(names []string) ([]engine.AppRef, error) {
	refs := make([]engine.AppRef, 0, len(names))
	for _, n := range names {
		ref, err := engine.ParseRef(n)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (s *server) deployPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Apps []string `json:"apps"`
	}
	if !decode(w, r, &in) {
		return
	}
	refs, err := parseRefs(in.Apps)
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	plan, err := s.engine(r).Plan(r.Context(), refs)
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *server) startDeploy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Apps            []string `json:"apps"`
		ContinueOnError bool     `json:"continueOnError"`
	}
	if !decode(w, r, &in) {
		return
	}
	refs, err := parseRefs(in.Apps)
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	job, err := s.engine(r).StartDeploy(r.Context(), engine.DeployRequest{Apps: refs, ContinueOnError: in.ContinueOnError,
		Initiator: s.initiator(r), ViaToken: s.token(r) != nil, IdempotencyKey: key})
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *server) startRollback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		App     string `json:"app"`
		Version string `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	ref, err := engine.ParseRef(in.App)
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	job, err := s.engine(r).StartRollback(r.Context(), engine.RollbackRequest{App: ref, Version: in.Version,
		Initiator: s.initiator(r), ViaToken: s.token(r) != nil, IdempotencyKey: key})
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *server) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	jobs, err := s.engine(r).ListJobs(r.Context(), store.JobFilter{Project: q.Get("project"), App: q.Get("app"), Status: q.Get("status"), Limit: limit, Before: before})
	s.respond(w, r, http.StatusOK, jobs, err)
}

func jobID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "任务编号不对")
		return 0, false
	}
	return id, true
}

func (s *server) getJob(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	job, err := s.engine(r).GetJob(r.Context(), id)
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *server) jobEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 20000 {
		limit = 20000
	}
	evs, err := s.engine(r).Events(r.Context(), id, after, limit)
	s.respond(w, r, http.StatusOK, evs, err)
}

func (s *server) cancelJob(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	if err := s.engine(r).Cancel(id); err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// idempotencyKey 读取请求头 Idempotency-Key：同一个键重复提交部署或回滚时返回原来的任务，网络重试不会重复部署。
func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 200 {
		writeError(w, http.StatusBadRequest, "bad_request", "Idempotency-Key 最多 200 个字符")
		return "", false
	}
	return key, true
}

// approveJob 批准待审批的任务（只能在页面上）。
func (s *server) approveJob(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	job, err := s.engine(r).Approve(r.Context(), id)
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// rejectJob 拒绝待审批的任务（只能在页面上）。
func (s *server) rejectJob(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	if err := s.engine(r).Reject(r.Context(), id); err != nil {
		s.failJob(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// jobLogs 返回任务日志的文本（分页），给脚本和 AI 用：可以只取最后几行或只取失败的步骤。
func (s *server) jobLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	tail, _ := strconv.Atoi(q.Get("tail"))
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	page, err := s.engine(r).Logs(r.Context(), id, engine.LogQuery{Tail: tail, After: after, Limit: limit, FailedOnly: q.Get("failed") == "1" || q.Get("failed") == "true"})
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) appState(w http.ResponseWriter, r *http.Request) {
	st, err := s.engine(r).State(r.Context(), engine.AppRef{Project: r.PathValue("project"), App: r.PathValue("app")})
	if err != nil {
		s.failJob(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
