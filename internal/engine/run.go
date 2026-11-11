package engine

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"deployhub/internal/core"
	"deployhub/internal/detect"
	"deployhub/internal/driver"
	"deployhub/internal/events"
	"deployhub/internal/model"
	"deployhub/internal/remote"
	"deployhub/internal/shell"
)

// run 是一个正在执行的任务。
type run struct {
	e       *Engine
	ctx     context.Context
	job     *model.Job
	start   time.Time
	repos   map[string]*repoState
	servers map[string]*serverState
}

type serverState struct {
	srv *model.Server
	err error
}

// ---------- 事件 ----------

const maxMessage = 8000

func (r *run) emit(seq *int, typ, step, msg string) {
	if len(msg) > maxMessage {
		msg = msg[:maxMessage] + "…（已截断）"
	}
	ev := &model.JobEvent{JobID: r.job.ID, UnitSeq: seq, Type: typ, Step: step, Message: msg, Time: r.e.now()}
	if err := r.e.store.AddJobEvent(context.Background(), ev); err != nil {
		r.e.log.Error("保存任务事件失败", "job", r.job.ID, "err", err)
	}
	r.e.hub.Publish(events.Event{Type: "job_event", Data: ev})
}

// publish 把任务的最新状态推给页面。
func (r *run) publish() {
	if j, err := r.e.store.GetJob(context.Background(), r.job.ID); err == nil {
		r.e.hub.Publish(events.Event{Type: "job", Data: j})
	}
}

func (r *run) info(format string, a ...any) { r.emit(nil, "info", "", fmt.Sprintf(format, a...)) }

func (r *run) startJob() {
	r.start = r.e.now()
	r.job.Status = "running"
	_ = r.e.store.SetJobStatus(context.Background(), r.job.ID, "running", "")
	names := make([]string, len(r.job.Units))
	for i, u := range r.job.Units {
		names[i] = u.Project + "/" + u.App
	}
	action := map[string]string{"deploy": "部署", "rollback": "回滚"}[r.job.Kind]
	r.info("开始%s：%s", action, strings.Join(names, "、"))
	r.publish()
}

func (r *run) finishJob(status, msg string) {
	r.job.Status = status
	_ = r.e.store.SetJobStatus(context.Background(), r.job.ID, status, msg)
	text := map[string]string{"success": "任务完成", "failed": "任务失败", "rolled_back": "任务失败，已回滚", "canceled": "任务已取消"}[status]
	if msg != "" {
		text += "：" + msg
	}
	r.emit(nil, map[bool]string{true: "info", false: "error"}[status == "success"], "", text)
	r.publish()
}

func (r *run) canceled() bool { return r.ctx.Err() != nil }

// jobStatus 根据各应用的结果得出任务状态。
func (r *run) jobStatus() string {
	if r.canceled() {
		return "canceled"
	}
	status := "success"
	for _, u := range r.job.Units {
		switch u.Status {
		case "failed", "skipped", "canceled":
			return "failed"
		case "rolled_back":
			status = "rolled_back"
		}
	}
	return status
}

// preflightServers 先确认本次涉及的服务器都连得上，有一台连不上就在构建前停止。
func (r *run) preflightServers() bool {
	ok := true
	for _, u := range r.job.Units {
		if u.Server == "" {
			continue // 不需要服务器的部署方式（如 Workers）
		}
		if _, done := r.servers[u.Server]; done {
			continue
		}
		st := &serverState{}
		r.servers[u.Server] = st
		srv, err := r.e.core.ConnectableServer(r.ctx, u.Server)
		if err == nil {
			ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
			_, err = r.e.core.DetectServer(ctx, srv)
			cancel()
		}
		st.srv, st.err = srv, err
		if err != nil {
			ok = false
			r.emit(nil, "error", "", fmt.Sprintf("服务器 %s 连不上：%v", u.Server, err))
		} else {
			r.info("服务器 %s 连接正常（%s）", u.Server, remote.Target{Host: srv.Host, Port: srv.Port, User: srv.User}.Label())
		}
	}
	if !ok {
		for _, u := range r.job.Units {
			msg := "有服务器连不上，没有开始"
			if st := r.servers[u.Server]; st != nil && st.err != nil {
				msg = "服务器连不上：" + st.err.Error()
			}
			r.setUnit(u, "skipped", "", msg)
		}
	}
	return ok
}

func (r *run) setUnit(u *model.JobUnit, status, step, msg string) {
	now := r.e.now()
	u.Status, u.Error = status, msg
	if step != "" {
		u.Step = step
	}
	switch status {
	case "running":
		u.StartedAt = &now
	case "pending":
	default:
		u.FinishedAt = &now
	}
	_ = r.e.store.UpdateUnit(context.Background(), u)
	r.publish()
}

// ---------- 部署 ----------

func (r *run) deployAll(continueOnError bool) {
	r.startJob()
	if !r.preflightServers() {
		r.finishJob("failed", "有服务器连不上，没有开始部署")
		return
	}
	stop := false
	for _, u := range r.job.Units {
		switch {
		case r.canceled():
			r.setUnit(u, "canceled", "", "任务已取消，没有开始")
			continue
		case stop:
			r.setUnit(u, "skipped", "", "前面的应用部署失败，没有继续")
			continue
		}
		ur := &unitRun{r: r, u: u, seq: u.Seq}
		ur.deploy()
		if (u.Status == "failed" || u.Status == "rolled_back") && !continueOnError {
			stop = true
		}
	}
	r.finishJob(r.jobStatus(), "")
}

func (r *run) rollbackAll() {
	r.startJob()
	if !r.preflightServers() {
		r.finishJob("failed", "服务器连不上，没有开始回滚")
		return
	}
	ur := &unitRun{r: r, u: r.job.Units[0], seq: 0}
	ur.rollback()
	r.finishJob(r.jobStatus(), "")
}

// unitRun 是任务里一个应用的执行。
type unitRun struct {
	r    *run
	u    *model.JobUnit
	seq  int
	t    *core.DeployTarget
	repo *detect.Repo
	// params 是 ${名称} 已替换的参数
	params map[string]any
	mask   *strings.Replacer
	step   string
}

func (ur *unitRun) emit(typ, msg string) {
	if ur.mask != nil {
		msg = ur.mask.Replace(msg)
	}
	seq := ur.seq
	ur.r.emit(&seq, typ, ur.step, msg)
}

func (ur *unitRun) info(format string, a ...any) { ur.emit("info", fmt.Sprintf(format, a...)) }
func (ur *unitRun) warn(format string, a ...any) { ur.emit("warn", fmt.Sprintf(format, a...)) }

// stepError 表示某个步骤失败。
type stepError struct {
	step string
	err  error
}

func (e *stepError) Error() string { return e.step + "失败：" + e.err.Error() }
func (e *stepError) Unwrap() error { return e.err }

// run 执行一个步骤：记录开始、耗时、失败原因。
func (ur *unitRun) run(name string, fn func() error) error {
	ur.step = name
	ur.u.Step = name
	_ = ur.r.e.store.UpdateUnit(context.Background(), ur.u)
	ur.r.publish()
	ur.emit("step_start", name)
	start := time.Now()
	err := fn()
	if err != nil {
		msg := err.Error()
		if ur.mask != nil {
			msg = ur.mask.Replace(msg)
		}
		ur.emit("step_fail", msg)
		return &stepError{step: name, err: err}
	}
	ur.emit("step_done", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	return nil
}

// load 读取应用、服务器、仓库，替换 ${名称}。
func (ur *unitRun) load(ctx context.Context) error {
	t, err := ur.r.e.core.DeployTarget(ctx, ur.u.Project, ur.u.App)
	if err != nil {
		return err
	}
	ur.t = t
	if t.Server != nil {
		if st := ur.r.servers[t.Server.Name]; st != nil && st.srv != nil {
			ur.t.Server = st.srv // 用刚检测过的环境信息
		}
	}
	refs := driver.RefNames(driver.Refs(t.Fields, t.App.Params))
	secrets, err := ur.r.e.core.SecretValues(ctx, refs)
	if err != nil {
		return err
	}
	params, err := driver.Expand(t.Fields, t.App.Params, func(n string) (string, bool) { v, ok := secrets[n]; return v, ok })
	if err != nil {
		return err
	}
	ur.params = params
	// 凭据里的敏感字段（如 API Token）也不能出现在日志里
	hidden := map[string]string{}
	for k, v := range secrets {
		hidden[k] = v
	}
	for i, v := range core.CredentialSecrets(t.Creds) {
		hidden[fmt.Sprintf("\x00cred%d", i)] = v
	}
	ur.mask = masker(hidden)
	// git 地址来源的在这里拉取最新提交；本机目录的同步在预检里按同步方式做
	ur.repo = ur.r.e.core.AppRepo(ctx, ur.u.Project, ur.u.App, params, core.RepoSyncJob, func(warn bool, msg string) {
		if ur.mask != nil {
			msg = ur.mask.Replace(msg)
		}
		if warn {
			ur.warn("%s", msg)
		} else {
			ur.info("%s", msg)
		}
	})
	return nil
}

// masker 把密钥的值替换成 ***，用于日志。太短的值（少于 4 个字符）不替换，避免误伤。
func masker(secrets map[string]string) *strings.Replacer {
	var vals []string
	for _, v := range secrets {
		if len(v) >= 4 {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return nil
	}
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	pairs := make([]string, 0, len(vals)*2)
	for _, v := range vals {
		pairs = append(pairs, v, "***")
	}
	return strings.NewReplacer(pairs...)
}

// exec 组装执行上下文。ctx 不同（可取消 / 不可取消）时重新组装。
func (ur *unitRun) exec(ctx context.Context, version, commit string) *driver.Exec {
	e := ur.r.e
	t := ur.t.Target()
	c := e.core.PlanContext(ur.u.Project, ur.u.App, ur.params, ur.t.Server, ur.repo, version)
	c.Creds = ur.t.Creds
	_, localRsync := exec.LookPath("rsync")
	x := &driver.Exec{
		Ctx: ctx, C: c, Commit: commit,
		Rsync: localRsync == nil && ur.t.Server != nil && ur.t.Server.Facts != nil && ur.t.Server.Facts.Rsync,
	}
	out := func(line string) { ur.emit("output", line) }
	x.Local = func(cmd shell.Cmd) error {
		ur.emit("command", "$ "+cmd.String())
		return shell.Run(ctx, cmd, out)
	}
	x.CacheDir = filepath.Join(e.dataDir, "build", ur.u.Project, ur.u.App, ".cache")
	x.Info, x.Warn = ur.info, ur.warn
	if ur.t.Server == nil {
		noServer(x)
		return x
	}
	x.Remote = func(script string) error {
		ur.emit("command", strings.TrimSpace(script))
		return e.ssh.Stream(ctx, t, script, out)
	}
	x.Query = func(script string) (string, error) {
		res, err := e.ssh.Run(ctx, t, script)
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return res.Stdout, fmt.Errorf("退出码 %d：%s", res.ExitCode, strings.TrimSpace(res.Stderr))
		}
		return res.Stdout, nil
	}
	x.CopyFiles = func(local []string, remoteDir string) error {
		names := make([]string, len(local))
		for i, l := range local {
			names[i] = filepath.Base(l)
		}
		ur.emit("command", fmt.Sprintf("scp %s → %s:%s/", strings.Join(names, " "), t.Label(), remoteDir))
		return e.ssh.CopyFiles(ctx, t, local, remoteDir, out)
	}
	x.RsyncDir = func(localDir, remoteDir string, extra []string) error {
		ur.emit("command", fmt.Sprintf("rsync %s %s/ → %s:%s/", strings.Join(extra, " "), localDir, t.Label(), remoteDir))
		return e.ssh.Rsync(ctx, t, localDir, remoteDir, extra, out)
	}
	x.Pipe = func(local shell.Cmd, remoteCmd string) error {
		ur.emit("command", fmt.Sprintf("%s | gzip | ssh %s %s", local.String(), t.Label(), driver.Q(remoteCmd)))
		return pipe(ctx, local, func(r io.Reader) error { return e.ssh.RunStdin(ctx, t, remoteCmd, r, out) }, ur.info, out)
	}
	return x
}

var errNoServer = errors.New("这种部署方式没有服务器，不能在服务器上执行命令")

// noServer 让服务器相关的方法直接报错（不需要服务器的部署方式误用时能看出来）。
func noServer(x *driver.Exec) {
	x.Remote = func(string) error { return errNoServer }
	x.Query = func(string) (string, error) { return "", errNoServer }
	x.CopyFiles = func([]string, string) error { return errNoServer }
	x.RsyncDir = func(string, string, []string) error { return errNoServer }
	x.Pipe = func(shell.Cmd, string) error { return errNoServer }
}

// pipe 执行本地命令，标准输出经 gzip 压缩后交给 send；本地命令的错误输出按行进日志。
// send 失败时结束本地命令，避免它卡在写输出上。
func pipe(ctx context.Context, local shell.Cmd, send func(io.Reader) error, info func(string, ...any), out func(string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, local.Name, local.Args...)
	cmd.Dir = local.Dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	pr, pw := io.Pipe()
	counter := &countWriter{}
	done := make(chan error, 1)
	go func() {
		gz, _ := gzip.NewWriterLevel(io.MultiWriter(pw, counter), gzip.BestSpeed)
		_, err := io.Copy(gz, stdout)
		if cerr := gz.Close(); err == nil {
			err = cerr
		}
		if werr := cmd.Wait(); werr != nil {
			err = fmt.Errorf("%s 失败：%v %s", local.String(), werr, strings.TrimSpace(stderr.String()))
		}
		pw.CloseWithError(err)
		done <- err
	}()
	start := time.Now()
	err = send(pr)
	if err != nil {
		cancel()
	}
	pr.CloseWithError(io.ErrClosedPipe)
	localErr := <-done // 等本地命令结束后再读它的错误输出
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if l != "" {
			out(l)
		}
	}
	if err == nil && localErr != nil && !errors.Is(localErr, io.ErrClosedPipe) {
		err = localErr
	}
	if err == nil {
		info("传输了 %.1f MB（压缩后），用时 %.1f 秒", float64(counter.n)/(1<<20), time.Since(start).Seconds())
	}
	return err
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// health 执行健康检查；没通过时给出直白的说明，而不是 ssh 的退出码。
func (ur *unitRun) health(x *driver.Exec) error {
	if hc, ok := ur.t.Executor.(driver.HealthChecker); ok {
		return hc.Health(x)
	}
	err := x.Remote(ur.t.Executor.HealthScript(x))
	var se *shell.Error
	if errors.As(err, &se) && se.ExitCode > 0 {
		timeout := x.C.Int("health_timeout", 60)
		return fmt.Errorf("%d 秒内没有通过（检查方式：%s），详见上面的输出", timeout, healthMode(x.C.Str("health_mode")))
	}
	return err
}

func healthMode(m string) string {
	switch m {
	case "http":
		return "HTTP 地址"
	case "command":
		return "自定义命令"
	}
	return "默认检查"
}

// makeVersion 生成版本号：<分支>-<短 commit>-<YYYYMMDD-HHMM>，和旧工具一致。
func makeVersion(branch, commit string, t time.Time) string {
	b := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' {
			return r
		}
		return '-'
	}, branch)
	if len(commit) > 7 {
		commit = commit[:7]
	}
	return fmt.Sprintf("%s-%s-%s", b, commit, t.Format("20060102-1504"))
}

// uniqueVersion 同一分钟内重复部署同一个提交时，加上 -2、-3，避免覆盖正在运行的版本目录。
func uniqueVersion(v string, existing []string) string {
	has := map[string]bool{}
	for _, r := range existing {
		has[r] = true
	}
	if !has[v] {
		return v
	}
	for i := 2; ; i++ {
		if c := fmt.Sprintf("%s-%d", v, i); !has[c] {
			return c
		}
	}
}

func (ur *unitRun) finish(status, msg string) {
	ur.step = ""
	ur.r.setUnit(ur.u, status, "", msg)
	text := map[string]string{
		"success": "完成", "failed": "失败", "rolled_back": "失败，已自动回滚到上一个版本", "canceled": "已取消",
	}[status]
	if msg != "" {
		text += "：" + msg
	}
	if ur.mask != nil {
		text = ur.mask.Replace(text)
	}
	seq := ur.seq
	typ := "info"
	if status != "success" {
		typ = "error"
	}
	ur.r.emit(&seq, typ, "", fmt.Sprintf("%s/%s %s", ur.u.Project, ur.u.App, text))
}

// fail 根据错误结束应用：取消导致的记为取消。
func (ur *unitRun) fail(err error) {
	if ur.r.canceled() && errors.Is(err, context.Canceled) {
		ur.finish("canceled", "")
		return
	}
	ur.finish("failed", err.Error())
}

func (ur *unitRun) deploy() {
	r := ur.r
	ur.r.setUnit(ur.u, "running", "", "")
	var x *driver.Exec
	var version, previous, commit string

	err := ur.run("预检", func() error {
		if err := ur.load(r.ctx); err != nil {
			return err
		}
		repo := ur.repo
		if repo.Error != "" {
			return errors.New(repo.Error)
		}
		want, _ := ur.params["branch"].(string)
		if repo.Branch != want {
			return fmt.Errorf("仓库当前在 %s 分支，部署要求 %s 分支", repo.Branch, want)
		}
		if repo.Dirty > 0 {
			if ur.params["dirty"] == "block" {
				return fmt.Errorf("仓库有 %d 个文件未提交，应用设置为有未提交改动时禁止部署", repo.Dirty)
			}
			ur.warn("仓库有 %d 个文件未提交，构建会包含这些改动", repo.Dirty)
		}
		rs, ok := r.repos[repo.Path]
		if !ok {
			var c string
			var err error
			if core.ManagedRepo(ur.params) {
				// 平台克隆的仓库刚才已经拉取过，直接用当前提交
				c, err = git(r.ctx, repo.Path, "rev-parse", "HEAD")
			} else {
				mode, _ := ur.params["sync"].(string)
				c, err = syncRepo(r.ctx, repo.Path, mode, ur.info, ur.warn)
			}
			rs = &repoState{branch: repo.Branch, commit: c, err: err}
			r.repos[repo.Path] = rs
		}
		if rs.err != nil {
			return rs.err
		}
		commit = rs.commit
		x = ur.exec(r.ctx, makeVersion(repo.Branch, commit, r.start), commit)
		st, err := ur.t.Executor.State(x)
		if err != nil {
			return fmt.Errorf("读取线上版本：%w", err)
		}
		previous = st.Current
		version = uniqueVersion(x.C.Version, st.Releases)
		x.C.Version = version
		ur.u.Version, ur.u.Previous, ur.u.Commit = version, previous, commit
		_ = r.e.store.UpdateUnit(context.Background(), ur.u)
		if previous == "" {
			ur.info("新版本 %s；之前还没有部署过（没有可回滚的版本）", version)
		} else {
			ur.info("新版本 %s，线上版本 %s", version, previous)
		}
		return nil
	})
	if err != nil {
		ur.fail(err)
		return
	}

	work := filepath.Join(r.e.dataDir, "build", ur.u.Project, ur.u.App, version)
	x.WorkDir, x.TmpDir = work, work+".tmp"
	defer os.RemoveAll(x.TmpDir)
	if err := os.MkdirAll(x.TmpDir, 0o700); err != nil {
		ur.fail(err)
		return
	}
	spec := ur.t.Spec

	for _, s := range []struct {
		key string
		fn  func() error
	}{
		{driver.StepPrepare, func() error { return ur.t.Executor.Prepare(x) }},
		{driver.StepBuild, func() error {
			if err := os.RemoveAll(work); err != nil {
				return err
			}
			if err := os.MkdirAll(work, 0o755); err != nil {
				return err
			}
			return ur.t.Executor.Build(x)
		}},
		{driver.StepUpload, func() error { return ur.t.Executor.Upload(x) }},
	} {
		name := spec.StepLabel(s.key)
		if name == "" {
			continue // 这种部署方式没有这一步
		}
		if r.canceled() {
			ur.finish("canceled", "")
			return
		}
		if err := ur.run(name, s.fn); err != nil {
			ur.fail(err)
			return
		}
	}
	if r.canceled() {
		ur.finish("canceled", "")
		return
	}

	// 从切换开始不再响应取消：切换、健康检查、必要时的回滚必须做完，不能停在一半
	ax := ur.exec(context.WithoutCancel(r.ctx), version, commit)
	ax.WorkDir, ax.TmpDir = x.WorkDir, x.TmpDir
	err = ur.run(spec.StepLabel(driver.StepActivate), func() error { return ur.t.Executor.Activate(ax, version) })
	if err == nil {
		err = ur.run(spec.StepLabel(driver.StepHealth), func() error { return ur.health(ax) })
	}
	if err != nil {
		status, msg := "failed", err.Error()
		if previous != "" {
			rb := ur.run("回滚到 "+previous, func() error {
				if err := ur.t.Executor.Activate(ax, previous); err != nil {
					return err
				}
				return ur.health(ax)
			})
			if rb == nil {
				status = "rolled_back"
			} else {
				msg += "；自动回滚也失败了，请立即手动处理：" + rb.Error()
			}
		} else {
			msg += "；没有上一个版本（之前没有部署过），无法自动回滚，请手动处理"
		}
		ur.record(ax, "deploy", version, status)
		ur.finish(status, msg)
		return
	}
	ur.record(ax, "deploy", version, "success")
	if name := spec.StepLabel(driver.StepCleanup); name != "" {
		if err := ur.run(name, func() error { return ur.t.Executor.Cleanup(ax) }); err != nil {
			ur.warn("清理旧版本失败（不影响本次部署）：%v", err)
		}
	}
	cleanLocalBuilds(filepath.Dir(work), 3)
	ur.finish("success", "")
}

func (ur *unitRun) rollback() {
	r := ur.r
	ur.r.setUnit(ur.u, "running", "", "")
	var ax *driver.Exec
	var current, target string
	err := ur.run("预检", func() error {
		if err := ur.load(r.ctx); err != nil {
			return err
		}
		ax = ur.exec(context.WithoutCancel(r.ctx), ur.u.Target, "")
		st, err := ur.t.Executor.State(ax)
		if err != nil {
			return fmt.Errorf("读取线上版本：%w", err)
		}
		current = st.Current
		target = ur.u.Target
		if target == "" {
			target = previousRelease(st)
			if target == "" {
				return errors.New("没有更早的版本可以回滚")
			}
		}
		found := false
		for _, v := range st.Releases {
			found = found || v == target
		}
		if !found {
			return fmt.Errorf("没有版本 %s（保留的版本：%s）", target, strings.Join(st.Releases, "、"))
		}
		if target == current {
			return fmt.Errorf("%s 已经是当前版本", target)
		}
		ax.C.Version = target
		ur.u.Version, ur.u.Previous = target, current
		_ = r.e.store.UpdateUnit(context.Background(), ur.u)
		ur.info("从 %s 回滚到 %s", orNone(current), target)
		return nil
	})
	if err != nil {
		ur.fail(err)
		return
	}
	err = ur.run("切换", func() error { return ur.t.Executor.Activate(ax, target) })
	if err == nil {
		err = ur.run("健康检查", func() error { return ur.health(ax) })
	}
	if err != nil {
		msg := err.Error()
		if current != "" {
			back := ur.run("切回 "+current, func() error {
				if err := ur.t.Executor.Activate(ax, current); err != nil {
					return err
				}
				return ur.health(ax)
			})
			if back == nil {
				msg += "；已切回原来的版本 " + current
			} else {
				msg += "；切回原来的版本也失败了，请立即手动处理：" + back.Error()
			}
		}
		ur.record(ax, "rollback", target, "failed")
		ur.finish("failed", msg)
		return
	}
	ur.record(ax, "rollback", target, "success")
	ur.finish("success", "")
}

// previousRelease 返回当前版本之后（更早）的一个版本；当前版本不在列表里时返回最新的那个。
func previousRelease(st *driver.OnlineState) string {
	for i, v := range st.Releases {
		if v == st.Current {
			if i+1 < len(st.Releases) {
				return st.Releases[i+1]
			}
			return ""
		}
	}
	for _, v := range st.Releases {
		if v != st.Current {
			return v
		}
	}
	return ""
}

func orNone(s string) string {
	if s == "" {
		return "（无）"
	}
	return s
}

// record 在服务器的 deploy.log 追加一行，格式和旧工具一致：时间、应用、版本、动作、结果、操作人（制表符分隔）。
func (ur *unitRun) record(x *driver.Exec, action, version, result string) {
	file := ur.t.Executor.LogFile(x)
	if file == "" {
		return // 没有服务器，部署记录只在本地任务里
	}
	line := strings.Join([]string{ur.r.e.now().Format("2006-01-02 15:04:05"), ur.u.App, version, action, result, ur.r.e.operator}, "\t")
	if _, err := x.Query(fmt.Sprintf("printf '%%s\\n' %s >> %s", driver.Q(line), driver.Q(file))); err != nil {
		ur.warn("写入服务器上的部署记录 %s 失败：%v", file, err)
	}
}

// cleanLocalBuilds 本地构建目录只保留最近 keep 个版本。
func cleanLocalBuilds(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		name string
		mod  time.Time
	}
	var list []item
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if info, err := e.Info(); err == nil {
			list = append(list, item{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod.After(list[j].mod) })
	for i := keep; i < len(list); i++ {
		os.RemoveAll(filepath.Join(dir, list[i].name))
	}
}
