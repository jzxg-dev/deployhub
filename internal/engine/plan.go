package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"deployhub/internal/core"
	"deployhub/internal/detect"
	"deployhub/internal/driver"
)

// LastRecord 是服务器 deploy.log 的最后一条记录。
type LastRecord struct {
	Time     string `json:"time"`
	Version  string `json:"version"`
	Action   string `json:"action"`
	Result   string `json:"result"`
	Operator string `json:"operator"`
}

func parseLast(line string) *LastRecord {
	f := strings.Split(line, "\t")
	if len(f) < 5 {
		return nil
	}
	r := &LastRecord{Time: f[0], Version: f[2], Action: f[3], Result: f[4]}
	if len(f) > 5 {
		r.Operator = f[5]
	}
	return r
}

// AppState 是应用的线上状态。
type AppState struct {
	Reachable bool        `json:"reachable"`
	Error     string      `json:"error,omitempty"`
	Current   string      `json:"current"`
	Releases  []string    `json:"releases"` // 新的在前
	Last      *LastRecord `json:"last,omitempty"`
	Busy      int64       `json:"busy,omitempty"` // 正在进行的任务编号
}

// queryExec 组装只读查询用的执行上下文（读取线上状态）。
func (e *Engine) queryExec(ctx context.Context, t *core.DeployTarget) *driver.Exec {
	// 只读线上状态不需要 git 信息，但有的部署方式要从仓库里读配置（如 Workers 的 Worker 名称）
	repoPath := e.core.RepoDir(t.App.Project, t.App.Name, t.App.Params)
	ur := &unitRun{t: t, params: t.App.Params, repo: &detect.Repo{Path: repoPath, Hints: map[string]string{}}, r: &run{e: e}}
	x := &driver.Exec{Ctx: ctx, C: e.core.PlanContext(t.App.Project, t.App.Name, t.App.Params, t.Server, ur.repo, "")}
	x.C.Creds = t.Creds
	if t.Server == nil {
		noServer(x)
		return x
	}
	target := t.Target()
	x.Query = func(script string) (string, error) {
		res, err := e.ssh.Run(ctx, target, script)
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return res.Stdout, fmt.Errorf("退出码 %d：%s", res.ExitCode, strings.TrimSpace(res.Stderr))
		}
		return res.Stdout, nil
	}
	return x
}

// State 读取应用的线上状态（只读）。
func (e *Engine) State(ctx context.Context, ref AppRef) (*AppState, error) {
	t, err := e.core.DeployTarget(ctx, ref.Project, ref.App)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return e.state(ctx, t, ref), nil
}

func (e *Engine) state(ctx context.Context, t *core.DeployTarget, ref AppRef) *AppState {
	out := &AppState{Releases: []string{}, Busy: e.Busy(ref)}
	st, err := t.Executor.State(e.queryExec(ctx, t))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Reachable, out.Current, out.Releases, out.Last = true, st.Current, st.Releases, parseLast(st.Last)
	return out
}

// PlanItem 是部署确认里的一个应用。
type PlanItem struct {
	Project  string   `json:"project"`
	App      string   `json:"app"`
	Title    string   `json:"title"`
	Driver   string   `json:"driver"`
	Server   string   `json:"server"`
	Branch   string   `json:"branch"`
	Commit   string   `json:"commit"`
	Subject  string   `json:"subject"`
	Dirty    int      `json:"dirty"`
	Version  string   `json:"version"` // 按本地最新提交预计的版本号；部署时会先同步远程，实际可能不同
	Online   string   `json:"online"`  // 线上版本
	Blocked  string   `json:"blocked,omitempty"`
	Warnings []string `json:"warnings"`
}

// DeployPlan 是部署确认的内容。
type DeployPlan struct {
	Items    []*PlanItem `json:"items"`
	Warnings []string    `json:"warnings"`
	OK       bool        `json:"ok"` // 没有不能部署的应用
}

var versionCommitRe = regexp.MustCompile(`-([0-9a-f]{7,40})-\d{8}-\d{4}(?:-\d+)?$`)

// commitOf 从版本号里取出 commit。
func commitOf(version string) string {
	if m := versionCommitRe.FindStringSubmatch(version); m != nil {
		return m[1]
	}
	return ""
}

// Plan 生成部署确认：每个应用的服务器、预计版本、线上版本，以及提醒。只读，不同步代码。
func (e *Engine) Plan(ctx context.Context, refs []AppRef) (*DeployPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	plan := &DeployPlan{Items: []*PlanItem{}, Warnings: []string{}, OK: true}
	now := e.now()
	selected := map[string]bool{}
	newCommit := map[string]string{} // 仓库 → 本次部署的 commit
	for _, ref := range refs {
		selected[ref.key()] = true
		t, err := e.core.DeployTarget(ctx, ref.Project, ref.App)
		if err != nil {
			return nil, err
		}
		it := &PlanItem{Project: t.App.Project, App: t.App.Name, Title: t.App.Title, Driver: t.App.Driver, Server: t.App.Server, Warnings: []string{}}
		plan.Items = append(plan.Items, it)

		repo := e.core.AppRepo(ctx, ref.Project, ref.App, t.App.Params, core.RepoSyncIdle, nil)
		want, _ := t.App.Params["branch"].(string)
		it.Branch, it.Commit, it.Subject, it.Dirty = repo.Branch, repo.Commit, repo.Subject, repo.Dirty
		switch {
		case repo.Error != "":
			it.Blocked = repo.Error
		case repo.Branch != want:
			it.Blocked = fmt.Sprintf("仓库当前在 %s 分支，部署要求 %s 分支", repo.Branch, want)
		case repo.Dirty > 0 && t.App.Params["dirty"] == "block":
			it.Blocked = fmt.Sprintf("有 %d 个文件未提交，应用设置为禁止部署", repo.Dirty)
		case repo.Dirty > 0:
			it.Warnings = append(it.Warnings, fmt.Sprintf("有 %d 个文件未提交，构建会包含这些改动", repo.Dirty))
		}
		if repo.Error == "" {
			it.Version = makeVersion(repo.Branch, repo.Commit, now)
			newCommit[repo.Path] = repo.Commit
		}
		if id := e.Busy(ref); id != 0 {
			it.Blocked = fmt.Sprintf("正在部署（任务 #%d）", id)
		}
		st := e.state(ctx, t, ref)
		if !st.Reachable {
			if t.Server != nil {
				it.Blocked = "服务器连不上：" + st.Error
			} else {
				it.Blocked = "读取线上版本失败：" + st.Error
			}
		}
		it.Online = st.Current
		if st.Reachable && st.Current == "" {
			it.Warnings = append(it.Warnings, "还没有部署过，第一次部署失败时没有可回滚的版本")
		}
		if it.Blocked != "" {
			plan.OK = false
		}
	}

	// 同一个仓库的其他应用：部署后运行的提交会不一致（只提醒，不阻止）
	seenProject := map[string]bool{}
	for _, it := range plan.Items {
		if seenProject[it.Project] {
			continue
		}
		seenProject[it.Project] = true
		apps, err := e.core.ListApps(ctx, it.Project)
		if err != nil {
			return nil, err
		}
		for _, a := range apps {
			ref := AppRef{Project: a.Project, App: a.Name}
			if selected[ref.key()] {
				continue
			}
			if core.ManagedRepo(a.Params) {
				continue // 平台克隆的仓库每个应用一份，不存在「共用一个工作区」
			}
			repo := e.core.AppRepo(ctx, a.Project, a.Name, a.Params, core.RepoNoSync, nil)
			nc, ok := newCommit[repo.Path]
			if !ok || repo.Error != "" {
				continue
			}
			t, err := e.core.DeployTarget(ctx, a.Project, a.Name)
			if err != nil {
				continue
			}
			st := e.state(ctx, t, ref)
			if oc := commitOf(st.Current); st.Reachable && oc != "" && !strings.HasPrefix(nc, oc) && !strings.HasPrefix(oc, nc) {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s 和本次部署的应用来自同一个仓库，它线上是 %s，本次部署 %s，部署后两者运行的代码不一致", a.Name, oc, nc))
			}
		}
	}
	return plan, nil
}
