// Package engine 是部署引擎：创建任务、按步骤执行部署和回滚、自动回滚、取消，并把过程写成任务事件推给页面。
//
// 任务在服务进程里执行（关掉浏览器不影响），同一个应用同时只能有一个任务。
// 服务重启时，上次没执行完的任务标记为失败。
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"sort"
	"strings"
	"sync"
	"time"

	"deployhub/internal/core"
	"deployhub/internal/events"
	"deployhub/internal/model"
	"deployhub/internal/remote"
	"deployhub/internal/store"
)

// Options 是创建引擎需要的依赖。
type Options struct {
	Core    *core.Service
	SSH     remote.Transport
	Hub     *events.Hub
	DataDir string // 构建产物放在 <DataDir>/build
	Logger  *slog.Logger
}

// Engine 是部署引擎。
type Engine struct {
	core    *core.Service
	store   *store.Store
	ssh     remote.Transport
	hub     *events.Hub
	dataDir string
	log     *slog.Logger
	now     func() time.Time
	// operator 写进服务器上的 deploy.log
	operator string

	mu      sync.Mutex
	cancels map[int64]context.CancelFunc // 进行中的任务
	locks   map[string]int64             // 项目/应用 → 任务编号
	wg      sync.WaitGroup
}

// New 创建引擎，并把上次没执行完的任务标记为失败。
func New(ctx context.Context, o Options) (*Engine, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	e := &Engine{
		core: o.Core, store: o.Core.Store(), ssh: o.SSH, hub: o.Hub, dataDir: o.DataDir, log: o.Logger,
		now: time.Now, operator: operator(),
		cancels: map[int64]context.CancelFunc{}, locks: map[string]int64{},
	}
	// 应用正在部署时，别处（检查、总览状态）不去动平台克隆的仓库
	o.Core.SetRepoBusy(func(project, app string) bool { return e.Busy(AppRef{Project: project, App: app}) != 0 })
	n, err := e.store.InterruptJobs(ctx, "deployhub 在任务执行中退出，任务中断；请查看服务器上的实际状态")
	if err != nil {
		return nil, err
	}
	if n > 0 {
		e.log.Warn("上次退出时有任务没执行完，已标记为失败", "count", n)
	}
	return e, nil
}

// Wait 等待所有任务结束（服务退出时用）。
func (e *Engine) Wait() { e.wg.Wait() }

// Running 返回正在执行的任务数。
func (e *Engine) Running() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cancels)
}

func operator() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username + "@deployhub"
	}
	if n := os.Getenv("USER"); n != "" {
		return n + "@deployhub"
	}
	return "deployhub"
}

// AppRef 指定一个应用。
type AppRef struct {
	Project string `json:"project"`
	App     string `json:"app"`
}

func (r AppRef) key() string { return r.Project + "/" + r.App }

// ParseRef 解析「项目/应用」。
func ParseRef(s string) (AppRef, error) {
	p, a, ok := strings.Cut(s, "/")
	if !ok || p == "" || a == "" {
		return AppRef{}, fmt.Errorf("应用应写成 项目/应用，如 shop/shop-api：%s", s)
	}
	return AppRef{Project: p, App: a}, nil
}

// DeployRequest 是发起部署的请求。
type DeployRequest struct {
	Apps []AppRef `json:"apps"`
	// ContinueOnError 为 true 时某个应用失败后继续部署剩下的；默认停止，剩下的标记为跳过。
	ContinueOnError bool   `json:"continueOnError"`
	Initiator       string `json:"-"`
	// ViaToken 表示通过访问令牌（AI、脚本）发起：应用打开了「AI 部署需要审批」时，任务先进入待审批。
	ViaToken bool `json:"-"`
	// IdempotencyKey 是幂等键：同一个键重复提交时返回原来的任务，不再新建。
	IdempotencyKey string `json:"-"`
}

// RollbackRequest 是发起回滚的请求。
type RollbackRequest struct {
	App AppRef `json:"app"`
	// Version 是目标版本，为空时回到当前版本的上一个版本。
	Version        string `json:"version"`
	Initiator      string `json:"-"`
	ViaToken       bool   `json:"-"`
	IdempotencyKey string `json:"-"`
}

// ErrPending 表示应用已经有一个待审批的任务。
type ErrPending struct {
	App   string
	JobID int64
}

func (e *ErrPending) Error() string {
	return fmt.Sprintf("%s 已经有一个待审批的任务（#%d），等它批准或拒绝后再发起", e.App, e.JobID)
}

// ErrNotPending 表示任务不是待审批状态（已经批准、拒绝或撤回）。
var ErrNotPending = errors.New("任务不是待审批状态，可能已经处理过了")

// needsApproval 读取应用的「AI 部署需要审批」，没保存这项时按默认值（需要）。
func needsApproval(params map[string]any) bool {
	v, ok := params["ai_approval"].(bool)
	return !ok || v
}

// existingJob 按幂等键找已经创建的任务，没有时返回 nil。
func (e *Engine) existingJob(ctx context.Context, key string) *model.Job {
	if key == "" {
		return nil
	}
	if j, err := e.store.JobByIdempotencyKey(ctx, key); err == nil {
		return j
	}
	return nil
}

// ErrBusy 表示应用正在部署。
type ErrBusy struct {
	App   string
	JobID int64
}

func (e *ErrBusy) Error() string {
	return fmt.Sprintf("%s 正在部署（任务 #%d），等它结束后再操作", e.App, e.JobID)
}

// StartDeploy 创建部署任务并在后台执行，立刻返回任务。
func (e *Engine) StartDeploy(ctx context.Context, req DeployRequest) (*model.Job, error) {
	if j := e.existingJob(ctx, req.IdempotencyKey); j != nil {
		return j, nil
	}
	if len(req.Apps) == 0 {
		return nil, errors.New("请选择要部署的应用")
	}
	approval := false
	seen := map[string]bool{}
	var units []*model.JobUnit
	orders := map[*model.JobUnit]int{}
	for _, ref := range req.Apps {
		if seen[ref.key()] {
			return nil, fmt.Errorf("应用 %s 重复", ref.key())
		}
		seen[ref.key()] = true
		t, err := e.core.DeployTarget(ctx, ref.Project, ref.App)
		if err != nil {
			return nil, err
		}
		u := &model.JobUnit{
			AppID: &t.App.ID, Project: t.App.Project, App: t.App.Name, Title: t.App.Title,
			Driver: t.App.Driver, Server: t.App.Server, Revision: t.App.Revision,
		}
		orders[u] = appOrder(t.App.Params)
		units = append(units, u)
		approval = approval || (req.ViaToken && needsApproval(t.App.Params))
	}
	// 按应用的部署顺序执行（小的在前），顺序相同的保持提交时的先后
	sort.SliceStable(units, func(i, j int) bool { return orders[units[i]] < orders[units[j]] })
	job := &model.Job{Kind: "deploy", Initiator: initiator(req.Initiator), Units: units,
		IdempotencyKey: req.IdempotencyKey, ContinueOnError: req.ContinueOnError}
	return e.submit(ctx, job, approval)
}

// submit 保存新任务：需要审批的进入待审批，否则立刻在后台执行。幂等键和别的请求撞上时返回那个任务。
func (e *Engine) submit(ctx context.Context, job *model.Job, approval bool) (*model.Job, error) {
	var err error
	if approval {
		err = e.createPending(ctx, job)
	} else {
		err = e.startJob(ctx, job)
	}
	if errors.Is(err, store.ErrDuplicate) {
		if j := e.existingJob(ctx, job.IdempotencyKey); j != nil {
			return j, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

// createPending 保存一个待审批的任务：不执行，也不占用应用，等页面上批准。
func (e *Engine) createPending(ctx context.Context, job *model.Job) error {
	for _, u := range job.Units {
		if u.AppID == nil {
			continue
		}
		id, err := e.store.PendingJobForApp(ctx, *u.AppID)
		if err != nil {
			return err
		}
		if id != 0 {
			return &ErrPending{App: u.Project + "/" + u.App, JobID: id}
		}
	}
	job.Status = "pending_approval"
	if err := e.store.CreateJob(ctx, job); err != nil {
		return err
	}
	e.note(job.ID, "info", "等待审批：这些应用打开了「AI 部署需要审批」，在页面上批准后才开始执行")
	return nil
}

// note 给任务记一条说明，并把任务的最新状态推给页面。
func (e *Engine) note(jobID int64, typ, msg string) {
	ev := &model.JobEvent{JobID: jobID, Type: typ, Message: msg, Time: e.now()}
	if err := e.store.AddJobEvent(context.Background(), ev); err == nil {
		e.hub.Publish(events.Event{Type: "job_event", Data: ev})
	}
	if j, err := e.store.GetJob(context.Background(), jobID); err == nil {
		e.hub.Publish(events.Event{Type: "job", Data: j})
	}
}

// Approve 批准待审批的任务，开始执行。执行时会重新预检（批准前代码和服务器可能有变化）。
func (e *Engine) Approve(ctx context.Context, jobID int64) (*model.Job, error) {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != "pending_approval" {
		return nil, ErrNotPending
	}
	if err := e.startJob(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

// Reject 拒绝待审批的任务，不执行。
func (e *Engine) Reject(ctx context.Context, jobID int64) error {
	return e.resolvePending(ctx, jobID, "rejected", "已在页面上拒绝，没有执行")
}

func (e *Engine) resolvePending(ctx context.Context, jobID int64, status, msg string) error {
	if err := e.store.ResolvePendingJob(ctx, jobID, status, msg); err != nil {
		if errors.Is(err, store.ErrConflict) {
			if _, gerr := e.store.GetJob(ctx, jobID); gerr != nil {
				return gerr
			}
			return ErrNotPending
		}
		return err
	}
	e.note(jobID, "warn", msg)
	return nil
}

// StartRollback 创建回滚任务并在后台执行。
func (e *Engine) StartRollback(ctx context.Context, req RollbackRequest) (*model.Job, error) {
	if j := e.existingJob(ctx, req.IdempotencyKey); j != nil {
		return j, nil
	}
	t, err := e.core.DeployTarget(ctx, req.App.Project, req.App.App)
	if err != nil {
		return nil, err
	}
	job := &model.Job{Kind: "rollback", Initiator: initiator(req.Initiator), IdempotencyKey: req.IdempotencyKey, Units: []*model.JobUnit{{
		AppID: &t.App.ID, Project: t.App.Project, App: t.App.Name, Title: t.App.Title,
		Driver: t.App.Driver, Server: t.App.Server, Revision: t.App.Revision, Target: req.Version, Version: req.Version,
	}}}
	return e.submit(ctx, job, req.ViaToken && needsApproval(t.App.Params))
}

// appOrder 读取应用的部署顺序，没填时是 100。
func appOrder(params map[string]any) int {
	switch v := params["order"].(type) {
	case float64:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	}
	return 100
}

func initiator(s string) string {
	if s == "" {
		return "page"
	}
	return s
}

// startJob 加锁、保存任务（待审批的任务是改成排队中）、在后台执行。
func (e *Engine) startJob(ctx context.Context, job *model.Job) error {
	body := func(r *run) { r.deployAll(job.ContinueOnError) }
	if job.Kind == "rollback" {
		body = func(r *run) { r.rollbackAll() }
	}
	e.mu.Lock()
	for _, u := range job.Units {
		key := u.Project + "/" + u.App
		if id, busy := e.locks[key]; busy {
			e.mu.Unlock()
			return &ErrBusy{App: key, JobID: id}
		}
	}
	var err error
	if job.ID == 0 {
		err = e.store.CreateJob(ctx, job)
	} else if err = e.store.ResolvePendingJob(ctx, job.ID, "queued", ""); errors.Is(err, store.ErrConflict) {
		err = ErrNotPending
	}
	if err != nil {
		e.mu.Unlock()
		return err
	}
	job.Status = "queued"
	runCtx, cancel := context.WithCancel(context.Background())
	e.cancels[job.ID] = cancel
	for _, u := range job.Units {
		e.locks[u.Project+"/"+u.App] = job.ID
	}
	e.mu.Unlock()

	r := &run{e: e, ctx: runCtx, job: job, repos: map[string]*repoState{}, servers: map[string]*serverState{}}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() {
			e.mu.Lock()
			delete(e.cancels, job.ID)
			for _, u := range job.Units {
				if e.locks[u.Project+"/"+u.App] == job.ID {
					delete(e.locks, u.Project+"/"+u.App)
				}
			}
			e.mu.Unlock()
			cancel()
		}()
		defer func() {
			if p := recover(); p != nil {
				e.log.Error("任务执行异常", "job", job.ID, "panic", p)
				r.finishJob("failed", fmt.Sprint("任务执行异常：", p))
			}
		}()
		body(r)
	}()
	return nil
}

// Cancel 取消任务。已经开始切换版本的应用会把切换和健康检查做完再停。待审批的任务直接撤回。
func (e *Engine) Cancel(jobID int64) error {
	e.mu.Lock()
	cancel, ok := e.cancels[jobID]
	e.mu.Unlock()
	if !ok {
		if err := e.resolvePending(context.Background(), jobID, "canceled", "发起方撤回了这个待审批的任务"); err == nil {
			return nil
		}
		return errors.New("任务已经结束")
	}
	cancel()
	return nil
}

// Busy 返回应用正在进行的任务编号，没有时为 0。
func (e *Engine) Busy(ref AppRef) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.locks[ref.key()]
}

// GetJob 读取任务。
func (e *Engine) GetJob(ctx context.Context, id int64) (*model.Job, error) {
	return e.store.GetJob(ctx, id)
}

// ListJobs 返回任务列表。
func (e *Engine) ListJobs(ctx context.Context, f store.JobFilter) ([]*model.Job, error) {
	return e.store.ListJobs(ctx, f)
}

// Events 返回任务事件。
func (e *Engine) Events(ctx context.Context, jobID, after int64, limit int) ([]*model.JobEvent, error) {
	return e.store.JobEvents(ctx, jobID, after, limit)
}
