// Package mcp 是 MCP 服务（Streamable HTTP）：让 Claude Code 这类 AI 客户端查看应用、发起部署、读日志、新建应用。
//
// 每个工具只是转调业务层和部署引擎，和 HTTP API 用的是同一套逻辑；能用哪些工具看访问令牌的权限级别，
// 权限不够的工具不会出现在工具列表里。密钥、凭据、访问令牌的管理和任务审批不提供，只能在页面上做。
// 不管令牌是什么级别，都读不到密钥明文和配置文件内容。
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"deployhub/internal/core"
	"deployhub/internal/driver"
	"deployhub/internal/engine"
	"deployhub/internal/model"
	"deployhub/internal/store"
)

// Session 是一次 MCP 请求对应的账号数据和访问令牌。
type Session struct {
	Core    *core.Service
	Engine  *engine.Engine
	Token   *model.AccessToken
	Version string
}

// NewHandler 创建 MCP 的 HTTP 处理器。resolve 返回请求对应的 Session（认证由调用方先做完）。
// 无状态：每个请求单独处理，不保留会话，所以令牌被吊销后立刻失效。
func NewHandler(resolve func(*http.Request) *Session, logger *slog.Logger) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		s := resolve(r)
		if s == nil {
			return nil
		}
		return newServer(s)
	}, &sdk.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		Logger:       logger,
		// Host 校验由平台自己的 Guard 做（它认识 -public-url 指定的对外地址，SDK 自带的只认 localhost）
		DisableLocalhostProtection: true,
	})
}

const instructions = `deployhub 是部署平台：应用写成「项目/应用」，如 shop/shop-api。
部署的一般步骤：plan_deploy 看计划（有没有被阻止、新旧版本）→ deploy 发起 → wait_job 等结果（部署时间长就反复调用）→ 失败时用 get_job_logs（failed_only）读失败步骤的日志找原因。
deploy 和 rollback 返回的任务状态如果是 pending_approval，说明应用要求人工审批：告诉用户到 deployhub 页面上批准，然后用 wait_job 等；不要重复发起。
健康检查失败时平台会自动回滚到上一个版本，任务状态是 rolled_back。
新建或修改应用前先用 list_drivers 看这种部署方式有哪些参数，用 validate_app 校验。读到的敏感参数（如配置文件内容）是占位值 ******，修改应用时原样带回去或不带都会保留原来的内容。
密钥、凭据、访问令牌的管理和任务审批只能由用户在页面上做。`

func newServer(s *Session) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "deployhub", Title: "deployhub 部署平台", Version: s.Version}, &sdk.ServerOptions{Instructions: instructions})
	t := &tools{s}
	rank := model.LevelRank(s.Token.Level)

	// 只读
	add(srv, "list_apps", "所有项目和应用：部署方式、服务器、线上版本、和最新提交比是否有更新。要连每台服务器，可能要几秒；不需要线上状态时把 with_status 设为 false。", readOnly, t.listApps)
	add(srv, "get_app", "一个应用的参数（敏感参数不返回内容）、线上版本和服务器上保留的版本（回滚可以选它们）。", readOnly, t.getApp)
	add(srv, "list_jobs", "部署记录，新的在前。status 填 pending_approval 可以只看待审批的。", readOnly, t.listJobs)
	add(srv, "get_job", "任务详情：状态、各应用的进度、失败的步骤和原因。", readOnly, t.getJob)
	add(srv, "get_job_logs", "任务日志，分页返回。默认最后 100 行；failed_only 只要失败步骤的日志；用返回的 next 作为 after 接着往后读。", readOnly, t.getJobLogs)
	add(srv, "plan_deploy", "部署计划：每个应用要部署的提交和版本号、线上版本、提醒、有没有被阻止（blocked）。不执行。", readOnly, t.planDeploy)
	add(srv, "wait_job", "等任务结束，最多等 timeout_seconds 秒（默认 30，最多 50），返回当前状态和最后几行日志。任务还在执行就再调用一次。待审批的任务会立刻返回。", readOnly, t.waitJob)
	add(srv, "list_drivers", "部署方式。不带 driver 时列出有哪些；带 driver 时返回这种部署方式的全部参数（通用参数和它自己的参数）：键、类型、是否必填、默认值、说明。", readOnly, t.listDrivers)
	add(srv, "detect_repo", "识别运行 deployhub 的机器上的一个仓库目录：分支、最新提交、推荐的部署方式和参数。", readOnly, t.detectRepo)
	add(srv, "validate_app", "校验应用参数，不保存。新建时不带 app；修改已有应用时带 app。参数有误时逐个字段说明。", readOnly, t.validateApp)

	// 可部署
	if rank >= model.LevelRank(model.LevelDeploy) {
		add(srv, "deploy", "发起部署，立刻返回任务（不等执行完）。应用要求审批时任务状态是 pending_approval，要等用户在页面上批准。", destructive, t.deploy)
		add(srv, "rollback", "把应用回滚到服务器上保留的某个版本（get_app 的 releases），不填 version 时回到上一个版本。立刻返回任务；审批同 deploy。", destructive, t.rollback)
		add(srv, "cancel_job", "取消任务：构建和上传阶段直接中止，已经开始切换版本的会把切换和健康检查做完再停。待审批的任务是撤回。", destructive, t.cancelJob)
	}
	// 管理
	if rank >= model.LevelRank(model.LevelAdmin) {
		add(srv, "create_app", "在项目里新建应用。先用 list_drivers 看参数、validate_app 校验。", additive, t.createApp)
		add(srv, "update_app", "修改应用的参数，生成新的配置版本。params 是完整的参数（先 get_app 拿到再改），revision 填 get_app 返回的版本号。", destructive, t.updateApp)
	}
	return srv
}

type hint int

const (
	readOnly    hint = iota // 不改任何东西
	additive                // 只新增
	destructive             // 会改变线上或已有配置，客户端执行前应先问用户
)

func add[In any](srv *sdk.Server, name, desc string, h hint, fn func(context.Context, In) (any, error)) {
	yes, no := true, false
	ann := &sdk.ToolAnnotations{OpenWorldHint: &no}
	switch h {
	case readOnly:
		ann.ReadOnlyHint = true
	case additive:
		ann.DestructiveHint = &no
	case destructive:
		ann.DestructiveHint = &yes
	}
	sdk.AddTool(srv, &sdk.Tool{Name: name, Description: desc, Annotations: ann},
		func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
			out, err := fn(ctx, in)
			if err != nil {
				return nil, nil, describe(err)
			}
			return nil, out, nil
		})
}

// describe 把错误整理成给 AI 看的一句话：校验错误带上每个字段的说明。
func describe(err error) error {
	var ce *core.Error
	if errors.As(err, &ce) && len(ce.Fields) > 0 {
		parts := make([]string, len(ce.Fields))
		for i, f := range ce.Fields {
			parts[i] = f.Field + "：" + f.Message
		}
		return fmt.Errorf("%s。%s", ce.Message, strings.Join(parts, "；"))
	}
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("任务不存在")
	}
	return err
}

type tools struct{ s *Session }

func (t *tools) initiator() string { return "token:" + t.s.Token.Name }

func refsOf(names []string) ([]engine.AppRef, error) {
	if len(names) == 0 {
		return nil, errors.New("apps 不能为空，写成 项目/应用")
	}
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

// ---------- 只读 ----------

type listAppsIn struct {
	Project    string `json:"project,omitempty" jsonschema:"只看这个项目；不填是所有项目"`
	WithStatus *bool  `json:"with_status,omitempty" jsonschema:"是否读取线上版本和状态（要连服务器），默认 true"`
}

type appSummary struct {
	Ref     string `json:"app"` // 项目/应用
	Title   string `json:"title"`
	Driver  string `json:"driver"`
	Server  string `json:"server,omitempty"`
	State   string `json:"state,omitempty"`
	Current string `json:"online_version,omitempty"`
	Commit  string `json:"latest_commit,omitempty"`
	Subject string `json:"latest_commit_subject,omitempty"`
	Error   string `json:"error,omitempty"`
	Busy    int64  `json:"running_job,omitempty"`
}

var stateText = map[string]string{
	"up_to_date": "已是最新", "has_update": "有更新（线上版本不是最新提交）", "not_deployed": "还没部署过", "unreachable": "读取失败", "busy": "正在部署",
}

func (t *tools) listApps(ctx context.Context, in listAppsIn) (any, error) {
	apps, err := t.s.Core.ListApps(ctx, in.Project)
	if err != nil {
		return nil, err
	}
	out := make([]*appSummary, len(apps))
	byRef := map[string]*appSummary{}
	for i, a := range apps {
		out[i] = &appSummary{Ref: a.Project + "/" + a.Name, Title: a.Title, Driver: a.Driver, Server: a.Server}
		byRef[out[i].Ref] = out[i]
	}
	if in.WithStatus == nil || *in.WithStatus {
		list, err := t.s.Engine.StatusAll(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		for _, st := range list {
			if s := byRef[st.Project+"/"+st.App]; s != nil {
				s.State, s.Current, s.Commit, s.Subject, s.Error, s.Busy = stateText[st.State], st.Current, st.LocalCommit, st.Subject, st.Error, st.Busy
			}
		}
	}
	return map[string]any{"apps": out}, nil
}

type appIn struct {
	App string `json:"app" jsonschema:"项目/应用，如 shop/shop-api"`
}

func (t *tools) getApp(ctx context.Context, in appIn) (any, error) {
	ref, err := engine.ParseRef(in.App)
	if err != nil {
		return nil, err
	}
	a, err := t.s.Core.GetAppRedacted(ctx, ref.Project, ref.App)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"app": in.App, "title": a.Title, "driver": a.Driver, "server": a.Server, "revision": a.Revision, "params": a.Params}
	if st, err := t.s.Engine.State(ctx, ref); err != nil {
		out["state_error"] = err.Error()
	} else {
		out["state"] = st
	}
	return out, nil
}

type listJobsIn struct {
	Project string `json:"project,omitempty" jsonschema:"只看这个项目"`
	App     string `json:"app,omitempty" jsonschema:"只看这个应用的名称（不带项目），要同时填 project"`
	Status  string `json:"status,omitempty" jsonschema:"只看这个状态：pending_approval、queued、running、success、failed、rolled_back、canceled、rejected"`
	Limit   int    `json:"limit,omitempty" jsonschema:"最多几条，默认 20，最多 100"`
}

func (t *tools) listJobs(ctx context.Context, in listJobsIn) (any, error) {
	if in.Limit <= 0 {
		in.Limit = 20
	}
	if in.Limit > 100 {
		in.Limit = 100
	}
	jobs, err := t.s.Engine.ListJobs(ctx, store.JobFilter{Project: in.Project, App: in.App, Status: in.Status, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	return map[string]any{"jobs": jobs}, nil
}

type jobIn struct {
	JobID int64 `json:"job_id" jsonschema:"任务编号"`
}

func (t *tools) getJob(ctx context.Context, in jobIn) (any, error) {
	return t.s.Engine.GetJob(ctx, in.JobID)
}

type jobLogsIn struct {
	JobID      int64 `json:"job_id" jsonschema:"任务编号"`
	Tail       int   `json:"tail,omitempty" jsonschema:"只要最后几行，默认 100，最多 500"`
	After      int64 `json:"after,omitempty" jsonschema:"接着上一次返回的 next 往后读（这时不按 tail 取，一次最多 500 行）；填 -1 表示从头读"`
	FailedOnly bool  `json:"failed_only,omitempty" jsonschema:"只要失败步骤的日志和错误"`
}

func (t *tools) getJobLogs(ctx context.Context, in jobLogsIn) (any, error) {
	q := engine.LogQuery{Tail: in.Tail, After: in.After, FailedOnly: in.FailedOnly, Limit: 500}
	if q.Tail <= 0 && q.After == 0 {
		q.Tail = 100
	}
	return t.s.Engine.Logs(ctx, in.JobID, q)
}

type planIn struct {
	Apps []string `json:"apps" jsonschema:"要部署的应用，每个写成 项目/应用"`
}

func (t *tools) planDeploy(ctx context.Context, in planIn) (any, error) {
	refs, err := refsOf(in.Apps)
	if err != nil {
		return nil, err
	}
	return t.s.Engine.Plan(ctx, refs)
}

type waitIn struct {
	JobID   int64 `json:"job_id" jsonschema:"任务编号"`
	Timeout int   `json:"timeout_seconds,omitempty" jsonschema:"最多等几秒，默认 30，最多 50"`
}

func (t *tools) waitJob(ctx context.Context, in waitIn) (any, error) {
	if in.Timeout <= 0 {
		in.Timeout = 30
	}
	if in.Timeout > 50 {
		in.Timeout = 50
	}
	job, err := t.s.Engine.WaitJob(ctx, in.JobID, time.Duration(in.Timeout)*time.Second)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"job": job, "finished": engine.Finished(job.Status)}
	switch job.Status {
	case "pending_approval":
		out["note"] = "任务在等人工审批：请用户到 deployhub 页面上批准或拒绝，之后再调用 wait_job"
	case "queued", "running":
		out["note"] = "任务还在执行，再调用一次 wait_job 继续等"
	}
	if logs, err := t.s.Engine.Logs(ctx, in.JobID, engine.LogQuery{Tail: 20}); err == nil {
		out["last_lines"], out["logs_next"] = logs.Lines, logs.Next
	}
	return out, nil
}

type driversIn struct {
	Driver string `json:"driver,omitempty" jsonschema:"部署方式的标识，如 gobinary；不填时只列出有哪些部署方式"`
}

func (t *tools) listDrivers(_ context.Context, in driversIn) (any, error) {
	reg := t.s.Core.Drivers()
	if in.Driver == "" {
		cat := reg.Catalog()
		list := make([]map[string]string, len(cat.Drivers))
		for i, d := range cat.Drivers {
			list[i] = map[string]string{"driver": d.Name, "title": d.Title, "summary": d.Summary}
		}
		return map[string]any{"drivers": list, "note": "带上 driver 再调用一次，可以看到这种部署方式的全部参数"}, nil
	}
	d, ok := reg.Get(in.Driver)
	if !ok {
		return nil, fmt.Errorf("没有部署方式 %s，不带 driver 调用可以看到有哪些", in.Driver)
	}
	spec := d.Spec()
	return map[string]any{
		"driver": spec.Name, "title": spec.Title, "description": spec.Description, "default_health_check": spec.DefaultCheck,
		"params": driver.FieldsOf(spec),
		"note":   "params 里 required 为 true 的必填；有 showIf 的只在条件满足时才需要；optionsFrom 为 servers 或 credentials:<种类> 的填已有的服务器或凭据的名称；值里可以用 ${名称} 引用「密钥与变量」",
	}, nil
}

type detectIn struct {
	Path string `json:"path" jsonschema:"运行 deployhub 的机器上的仓库目录，可以用 ~"`
}

func (t *tools) detectRepo(ctx context.Context, in detectIn) (any, error) {
	return t.s.Core.DetectRepo(ctx, in.Path), nil
}

type validateIn struct {
	Project string         `json:"project" jsonschema:"项目标识"`
	App     string         `json:"app,omitempty" jsonschema:"已有应用的名称（不带项目）；新建时不填"`
	Driver  string         `json:"driver,omitempty" jsonschema:"部署方式，新建时必填"`
	Params  map[string]any `json:"params" jsonschema:"应用参数，键见 list_drivers"`
}

func (t *tools) validateApp(ctx context.Context, in validateIn) (any, error) {
	if err := t.s.Core.ValidateApp(ctx, in.Project, in.App, core.AppInput{Driver: in.Driver, Params: in.Params}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ---------- 可部署 ----------

type deployIn struct {
	Apps            []string `json:"apps" jsonschema:"要部署的应用，每个写成 项目/应用；多个时按各自的部署顺序依次执行"`
	ContinueOnError bool     `json:"continue_on_error,omitempty" jsonschema:"某个应用失败后是否继续部署剩下的，默认停止"`
	IdempotencyKey  string   `json:"idempotency_key,omitempty" jsonschema:"幂等键：同一个键重复调用返回原来的任务，不会重复部署。不确定上一次调用有没有成功时用同一个键重试"`
}

// started 整理发起部署或回滚后的返回：任务，以及待审批时该怎么办。
func started(job *model.Job) map[string]any {
	out := map[string]any{"job": job}
	if job.Status == "pending_approval" {
		out["note"] = "任务在等人工审批，还没有执行：请用户到 deployhub 页面上批准，然后用 wait_job 等结果。不要重复发起"
	} else {
		out["note"] = "任务已经开始，用 wait_job 等结果"
	}
	return out
}

func (t *tools) deploy(ctx context.Context, in deployIn) (any, error) {
	refs, err := refsOf(in.Apps)
	if err != nil {
		return nil, err
	}
	job, err := t.s.Engine.StartDeploy(ctx, engine.DeployRequest{Apps: refs, ContinueOnError: in.ContinueOnError,
		Initiator: t.initiator(), ViaToken: true, IdempotencyKey: strings.TrimSpace(in.IdempotencyKey)})
	if err != nil {
		return nil, err
	}
	return started(job), nil
}

type rollbackIn struct {
	App            string `json:"app" jsonschema:"项目/应用"`
	Version        string `json:"version,omitempty" jsonschema:"目标版本（get_app 的 releases 里的一个）；不填时回到当前版本的上一个版本"`
	IdempotencyKey string `json:"idempotency_key,omitempty" jsonschema:"幂等键，同 deploy"`
}

func (t *tools) rollback(ctx context.Context, in rollbackIn) (any, error) {
	ref, err := engine.ParseRef(in.App)
	if err != nil {
		return nil, err
	}
	job, err := t.s.Engine.StartRollback(ctx, engine.RollbackRequest{App: ref, Version: in.Version,
		Initiator: t.initiator(), ViaToken: true, IdempotencyKey: strings.TrimSpace(in.IdempotencyKey)})
	if err != nil {
		return nil, err
	}
	return started(job), nil
}

func (t *tools) cancelJob(ctx context.Context, in jobIn) (any, error) {
	// 先确认任务是这个账号的（引擎只认识自己账号正在执行的任务，这里顺便给出明确的「不存在」）
	if _, err := t.s.Engine.GetJob(ctx, in.JobID); err != nil {
		return nil, err
	}
	if err := t.s.Engine.Cancel(in.JobID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "note": "已请求取消，用 wait_job 看最终状态"}, nil
}

// ---------- 管理 ----------

type createAppIn struct {
	Project string         `json:"project" jsonschema:"项目标识（项目要已经存在）"`
	Driver  string         `json:"driver" jsonschema:"部署方式，如 gobinary"`
	Params  map[string]any `json:"params" jsonschema:"应用参数，键见 list_drivers；其中 name 是应用标识"`
}

func (t *tools) createApp(ctx context.Context, in createAppIn) (any, error) {
	return t.s.Core.CreateAppRedacted(ctx, in.Project, core.AppInput{Driver: in.Driver, Params: in.Params})
}

type updateAppIn struct {
	App      string         `json:"app" jsonschema:"项目/应用"`
	Params   map[string]any `json:"params" jsonschema:"完整的应用参数（不是只传要改的）；敏感参数不传或传占位值时保留原来的内容"`
	Revision int            `json:"revision" jsonschema:"基于的配置版本号（get_app 返回的 revision）；应用在这之后被别处改过时会拒绝"`
}

func (t *tools) updateApp(ctx context.Context, in updateAppIn) (any, error) {
	ref, err := engine.ParseRef(in.App)
	if err != nil {
		return nil, err
	}
	return t.s.Core.UpdateAppKeepSensitive(ctx, ref.Project, ref.App, core.AppInput{Params: in.Params, Revision: in.Revision})
}
