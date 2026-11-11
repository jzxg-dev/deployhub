package engine

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"deployhub/internal/core"
)

// AppStatus 是总览页上一个应用的部署状态。
type AppStatus struct {
	Project string `json:"project"`
	App     string `json:"app"`
	// State：up_to_date 已是最新 / has_update 本地有新提交 / not_deployed 还没部署过 / unreachable 读取失败 / busy 正在部署
	State       string `json:"state"`
	Current     string `json:"current"`
	Branch      string `json:"branch"`
	LocalCommit string `json:"localCommit"`
	Subject     string `json:"subject"`
	Dirty       int    `json:"dirty"`
	Error       string `json:"error,omitempty"`
	Busy        int64  `json:"busy,omitempty"`
}

// StatusAll 读取所有应用的部署状态：线上版本（连服务器）和本地最新提交，并行读取。
func (e *Engine) StatusAll(ctx context.Context, project string) ([]*AppStatus, error) {
	apps, err := e.core.ListApps(ctx, project)
	if err != nil {
		return nil, err
	}
	out := make([]*AppStatus, len(apps))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, a := range apps {
		wg.Add(1)
		go func(i int, project, app string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			out[i] = e.appStatus(ctx, AppRef{Project: project, App: app})
		}(i, a.Project, a.Name)
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].App < out[j].App
	})
	return out, nil
}

func (e *Engine) appStatus(ctx context.Context, ref AppRef) *AppStatus {
	s := &AppStatus{Project: ref.Project, App: ref.App}
	t, err := e.core.DeployTarget(ctx, ref.Project, ref.App)
	if err != nil {
		s.State, s.Error = "unreachable", err.Error()
		return s
	}
	repo := e.core.AppRepo(ctx, ref.Project, ref.App, t.App.Params, core.RepoSyncIdle, nil)
	s.Branch, s.LocalCommit, s.Subject, s.Dirty = repo.Branch, repo.Commit, repo.Subject, repo.Dirty
	st := e.state(ctx, t, ref)
	s.Current, s.Busy = st.Current, st.Busy
	oc := commitOf(st.Current)
	switch {
	case st.Busy != 0:
		s.State = "busy"
	case !st.Reachable:
		s.State, s.Error = "unreachable", st.Error
	case st.Current == "":
		s.State = "not_deployed"
	case repo.Error != "":
		s.State, s.Error = "unreachable", repo.Error
	case oc != "" && (strings.HasPrefix(repo.Commit, oc) || strings.HasPrefix(oc, repo.Commit)):
		s.State = "up_to_date"
	default:
		s.State = "has_update"
	}
	return s
}
