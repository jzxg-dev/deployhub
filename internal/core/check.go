package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"deployhub/internal/detect"
	"deployhub/internal/driver"
	"deployhub/internal/model"
	"deployhub/internal/remote"
)

// planContext 组装生成计划需要的信息。srv 为 nil 时服务器信息用占位符。
func (s *Service) planContext(project, app string, params map[string]any, srv *model.Server, repo *detect.Repo) *driver.PlanContext {
	c := &driver.PlanContext{Project: project, App: app, Params: params}
	if c.App == "" {
		c.App = "<应用>"
	}
	if srv != nil {
		t := ServerTarget(srv)
		c.Server = driver.ServerInfo{Name: srv.Name, Dest: t.Host, Label: t.Label(), User: srv.User}
		if srv.User != "" {
			c.Server.Dest = srv.User + "@" + srv.Host
		}
		if srv.Facts != nil {
			c.Server.Arch = srv.Facts.Arch
			// 以检测到的实际登录用户为准：ssh 配置里的用户名不一定是服务器上的用户（如 OrbStack 用虚拟机名）
			if srv.Facts.User != "" {
				c.Server.User = srv.Facts.User
			}
		}
	} else {
		c.Server = driver.ServerInfo{Dest: "<服务器>", Label: "<服务器>"}
	}
	c.Repo = driver.RepoInfo{Path: repo.Path, Subject: repo.Subject, Hints: repo.Hints}
	if c.Repo.Path == "" {
		c.Repo.Path = "<仓库目录>"
	}
	branch, commit := repo.Branch, repo.Commit
	if branch == "" {
		branch = "<分支>"
	}
	if b, _ := params["branch"].(string); b != "" {
		branch = b
	}
	if commit == "" {
		commit = "<commit>"
	}
	c.Version = fmt.Sprintf("%s-%s-%s", branch, commit, s.now().Format("20060102-1504"))
	return c
}

// Preview 是命令预览的结果。
type Preview struct {
	Version string       `json:"version"`
	Plan    *driver.Plan `json:"plan"`
}

// PreviewApp 生成部署计划预览。in.Params 不为空时用提交的参数（表单还没保存），否则用已保存的参数。
// appName 为空表示新建中的应用。参数不完整也能预览，缺的部分用占位符。
func (s *Service) PreviewApp(ctx context.Context, projectName, appName string, in AppInput) (*Preview, error) {
	if _, err := s.GetProject(ctx, projectName); err != nil {
		return nil, err
	}
	drv := in.Driver
	params := in.Params
	if appName != "" {
		a, err := s.GetApp(ctx, projectName, appName)
		if err != nil {
			return nil, err
		}
		drv = a.Driver
		if params == nil {
			params = a.Params
		}
	}
	d, ok := s.drivers.Get(drv)
	if !ok {
		return nil, fieldErr("driver", "不认识的部署方式："+drv)
	}
	planner, ok := d.(driver.Planner)
	if !ok {
		return nil, invalid("部署方式 " + drv + " 不支持预览")
	}
	fields, _ := s.fieldsOf(drv)
	cleaned, _ := driver.Clean(fields, params) // 预览时忽略校验错误
	name, _ := cleaned["name"].(string)

	var srv *model.Server
	if sn, _ := cleaned["server"].(string); sn != "" {
		srv, _ = s.store.GetServer(ctx, sn)
	}
	// 预览在填表时反复调用：不联网，git 地址来源的只看已经克隆下来的内容
	repo := &detect.Repo{Hints: map[string]string{}}
	if s.RepoDir(projectName, name, cleaned) != "" && (name != "" || !ManagedRepo(cleaned)) {
		repo = s.AppRepo(ctx, projectName, name, cleaned, RepoNoSync, nil)
	}
	c := s.planContext(projectName, name, cleaned, srv, repo)
	// 预览里显示凭据的不敏感字段（如账号 ID），敏感字段留空
	if creds, err := s.credsFor(ctx, fields, cleaned); err == nil {
		for _, values := range creds {
			for k := range values {
				if isSensitiveCredentialField(k) {
					values[k] = ""
				}
			}
		}
		c.Creds = creds
	}
	return &Preview{Version: c.Version, Plan: planner.Plan(c)}, nil
}

// CheckItem 是一项检查的结果。
type CheckItem struct {
	Group   string `json:"group"`
	Name    string `json:"name"`
	Status  string `json:"status"` // ok、warn、fail、skip
	Message string `json:"message"`
}

// CheckResult 是部署前检查的结果。
type CheckResult struct {
	OK       bool        `json:"ok"` // 没有 fail
	Items    []CheckItem `json:"items"`
	Duration int64       `json:"durationMs"`
}

// CheckApp 检查应用能不能部署：本机仓库和工具、服务器连接、目录权限、必需文件等。只读，不改动服务器。
func (s *Service) CheckApp(ctx context.Context, projectName, appName string) (*CheckResult, error) {
	start := time.Now()
	a, err := s.GetApp(ctx, projectName, appName)
	if err != nil {
		return nil, err
	}
	d, _ := s.drivers.Get(a.Driver)
	planner, ok := d.(driver.Planner)
	if !ok {
		return nil, invalid("部署方式 " + a.Driver + " 不支持检查")
	}
	res := &CheckResult{Items: []CheckItem{}}
	add := func(group, name, status, msg string) {
		res.Items = append(res.Items, CheckItem{Group: group, Name: name, Status: status, Message: msg})
	}

	// 仓库：git 地址来源的先拉取最新提交
	repo := s.AppRepo(ctx, projectName, a.Name, a.Params, RepoSyncIdle, nil)
	repoOK := repo.Error == ""
	if !repoOK {
		add("本机", "仓库", "fail", repo.Error)
	} else {
		add("本机", "仓库", "ok", fmt.Sprintf("%s（%s %s）", repo.Path, repo.Branch, repo.Commit))
		if want, _ := a.Params["branch"].(string); want != "" && repo.Branch != want {
			add("本机", "部署分支", "fail", fmt.Sprintf("仓库当前在 %s 分支，部署要求 %s 分支", repo.Branch, want))
		} else {
			add("本机", "部署分支", "ok", repo.Branch)
		}
		if repo.Dirty > 0 {
			status := "warn"
			if a.Params["dirty"] == "block" {
				status = "fail"
			}
			add("本机", "未提交的改动", status, fmt.Sprintf("有 %d 个文件未提交", repo.Dirty))
		}
		for _, n := range repo.Notes {
			add("本机", "提示", "warn", n)
		}
	}

	// 服务器连接（顺便更新检测结果）；不需要服务器的部署方式跳过
	var srv *model.Server
	connErr := errors.New("这种部署方式没有服务器")
	tctx, cancel := ctxTimeout(ctx, 90*time.Second)
	defer cancel()
	if d.Spec().UsesServer() {
		if srv, err = s.ConnectableServer(ctx, a.Server); err != nil {
			return nil, err
		}
		var facts *model.ServerFacts
		facts, connErr = detect.Server(tctx, s.ssh, ServerTarget(srv))
		if connErr == nil {
			srv.Facts = facts
			_ = s.store.SetServerFacts(ctx, srv.Name, facts, s.now())
			add("服务器", "连接", "ok", fmt.Sprintf("%s（%s，%s）", ServerTarget(srv).Label(), facts.OS, facts.Machine))
		} else {
			add("服务器", "连接", "fail", connErr.Error())
		}
	}

	c := s.planContext(projectName, a.Name, a.Params, srv, repo)
	fields, _ := s.fieldsOf(a.Driver)
	creds, credErr := s.credsFor(ctx, fields, a.Params)
	if credErr != nil {
		add("账号", "凭据", "fail", credErr.Error())
	}
	c.Creds = creds
	var remoteChecks []driver.Check
	for _, ch := range planner.Checks(c) {
		switch {
		case ch.Local != nil:
			if !repoOK && !ch.NoRepo {
				add(ch.Group, ch.Name, "skip", "仓库不可用，跳过")
				continue
			}
			if credErr != nil && ch.NeedCreds {
				add(ch.Group, ch.Name, "skip", "凭据不可用，跳过")
				continue
			}
			ok, msg := ch.Local()
			add(ch.Group, ch.Name, status(ok, ch.Warn), msg)
		case ch.Remote != "":
			remoteChecks = append(remoteChecks, ch)
		}
	}

	if len(remoteChecks) > 0 {
		if connErr != nil {
			for _, ch := range remoteChecks {
				add(ch.Group, ch.Name, "skip", "连不上服务器，跳过："+connErr.Error())
			}
		} else {
			results, err := s.runRemoteChecks(tctx, ServerTarget(srv), remoteChecks)
			for i, ch := range remoteChecks {
				switch {
				case err != nil:
					add(ch.Group, ch.Name, "skip", "检查失败："+err.Error())
				case results[i]:
					add(ch.Group, ch.Name, "ok", ch.OK)
				default:
					add(ch.Group, ch.Name, status(false, ch.Warn), ch.Fail)
				}
			}
		}
	}

	res.OK = true
	for _, it := range res.Items {
		if it.Status == "fail" {
			res.OK = false
		}
	}
	res.Duration = time.Since(start).Milliseconds()
	return res, nil
}

func status(ok, warn bool) string {
	switch {
	case ok:
		return "ok"
	case warn:
		return "warn"
	}
	return "fail"
}

// runRemoteChecks 把所有服务器检查合成一个脚本，一次 ssh 执行完。返回每项是否通过。
func (s *Service) runRemoteChecks(ctx context.Context, t remote.Target, checks []driver.Check) ([]bool, error) {
	var b strings.Builder
	for i, ch := range checks {
		fmt.Fprintf(&b, "if ( %s ) >/dev/null 2>&1 </dev/null; then echo \"CHECK %d ok\"; else echo \"CHECK %d fail\"; fi\n", ch.Remote, i, i)
	}
	res, err := s.ssh.Run(ctx, t, b.String())
	if err != nil {
		return nil, err
	}
	results := make([]bool, len(checks))
	seen := 0
	for _, line := range strings.Split(res.Stdout, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != "CHECK" {
			continue
		}
		i, err := strconv.Atoi(f[1])
		if err != nil || i < 0 || i >= len(checks) {
			continue
		}
		results[i] = f[2] == "ok"
		seen++
	}
	if seen != len(checks) {
		return nil, errors.New("服务器返回的检查结果不完整：" + strings.TrimSpace(res.Stderr))
	}
	return results, nil
}
