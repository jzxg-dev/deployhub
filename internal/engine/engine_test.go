package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"deployhub/internal/core"
	"deployhub/internal/driver/builtin"
	"deployhub/internal/events"
	"deployhub/internal/model"
	"deployhub/internal/remote"
	"deployhub/internal/shell"
	"deployhub/internal/store"
	"deployhub/internal/vault"
)

// fakeServer 模拟服务器：记录执行的脚本，维护 current 和 releases。
type fakeServer struct {
	mu        sync.Mutex
	scripts   []string
	current   string
	releases  []string
	failOn    string // 脚本包含它时失败
	failTimes int    // 失败几次，0 表示一直失败
	failed    int
	blockOn   string // 脚本包含它时阻塞，直到 ctx 取消或 unblock 关闭
	unblock   chan struct{}
}

var (
	lnRe    = regexp.MustCompile(`ln -sfn releases/(\S+) current\.tmp`)
	mkRelRe = regexp.MustCompile(`mkdir -p \S*/releases/(\S+)$`)
)

func (f *fakeServer) record(s string) {
	f.mu.Lock()
	f.scripts = append(f.scripts, s)
	f.mu.Unlock()
}

func (f *fakeServer) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.scripts, "\n----\n")
}

func (f *fakeServer) Run(_ context.Context, _ remote.Target, script string) (*remote.Result, error) {
	f.record(script)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.Contains(script, "uname -m"):
		return &remote.Result{Stdout: "machine=x86_64\nuser=deploy\nos=Debian\nsystemd=1\nsudo=1\n"}, nil
	case strings.Contains(script, "readlink current"):
		out := "current=" + f.current + "\n"
		for i := len(f.releases) - 1; i >= 0; i-- {
			out += "release=" + f.releases[i] + "\n"
		}
		return &remote.Result{Stdout: out}, nil
	}
	return &remote.Result{}, nil
}

func (f *fakeServer) Stream(ctx context.Context, _ remote.Target, script string, onLine func(string)) error {
	f.record(script)
	if f.blockOn != "" && strings.Contains(script, f.blockOn) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.unblock:
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn != "" && strings.Contains(script, f.failOn) && (f.failTimes == 0 || f.failed < f.failTimes) {
		f.failed++
		onLine("模拟失败")
		return &shell.Error{Cmd: "ssh", ExitCode: 1}
	}
	if m := lnRe.FindStringSubmatch(script); m != nil {
		f.current = m[1]
	}
	if m := mkRelRe.FindStringSubmatch(strings.TrimSpace(script)); m != nil {
		f.releases = append(f.releases, m[1])
	}
	return nil
}

func (f *fakeServer) CopyFiles(_ context.Context, _ remote.Target, local []string, dir string, _ func(string)) error {
	f.record("scp " + strings.Join(local, " ") + " " + dir)
	return nil
}

func (f *fakeServer) Rsync(_ context.Context, _ remote.Target, localDir, dir string, _ []string, _ func(string)) error {
	f.record("rsync " + localDir + " " + dir)
	return nil
}

type env struct {
	e   *Engine
	svc *core.Service
	srv *fakeServer
	st  *store.Store
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/app\n\ngo 1.21\n",
		"cmd/app/main.go": "package main\n\nvar Version = \"dev\"\n\nfunc main() { println(Version) }\n",
	}
	for name, content := range files {
		p := filepath.Join(root, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@e", "commit", "-q", "-m", "初始提交"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return root
}

func newEnv(t *testing.T, srv *fakeServer) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "deployhub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	v, _ := vault.New(bytes.Repeat([]byte{5}, 32))
	reg, _ := builtin.Registry()
	svc := core.New(st, v, reg, srv)
	e, err := New(ctx, Options{Core: svc, SSH: srv, Hub: events.NewHub(), DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Wait)
	if _, err := svc.CreateProject(ctx, core.ProjectInput{Name: "proj", Title: "项目"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateServer(ctx, core.ServerInput{Name: "srv", Host: "1.2.3.4"}); err != nil {
		t.Fatal(err)
	}
	return &env{e: e, svc: svc, srv: srv, st: st}
}

func (en *env) app(t *testing.T, extra map[string]any) {
	t.Helper()
	params := map[string]any{
		"name": "app", "title": "应用", "repo_path": gitRepo(t), "branch": "main", "server": "srv", "sync": "offline",
		"main": "./cmd/app", "remote_dir": "/opt/proj/app",
	}
	for k, v := range extra {
		params[k] = v
	}
	if _, err := en.svc.CreateApp(context.Background(), "proj", core.AppInput{Driver: "gobinary", Params: params}); err != nil {
		t.Fatal(err)
	}
}

// wait 等任务结束。
func (en *env) wait(t *testing.T, id int64) *model.Job {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, err := en.e.GetJob(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != "queued" && j.Status != "running" {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("任务没有在 60 秒内结束")
	return nil
}

func (en *env) events(t *testing.T, id int64) []*model.JobEvent {
	t.Helper()
	evs, err := en.e.Events(context.Background(), id, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func steps(evs []*model.JobEvent, typ string) []string {
	var out []string
	for _, e := range evs {
		if e.Type == typ {
			out = append(out, e.Step)
		}
	}
	return out
}

var ref = AppRef{Project: "proj", App: "app"}

func TestDeploySuccess(t *testing.T) {
	srv := &fakeServer{current: "main-0000000-20260101-0000", releases: []string{"main-0000000-20260101-0000"}}
	en := newEnv(t, srv)
	en.app(t, map[string]any{"ldflags_x": map[string]any{"main.Version": "{version}"}})

	job, err := en.e.StartDeploy(context.Background(), DeployRequest{Apps: []AppRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	j := en.wait(t, job.ID)
	u := j.Units[0]
	if j.Status != "success" || u.Status != "success" {
		t.Fatalf("任务 %s / 应用 %s：%s", j.Status, u.Status, u.Error)
	}
	if !regexp.MustCompile(`^main-[0-9a-f]{7}-\d{8}-\d{4}$`).MatchString(u.Version) || u.Previous != "main-0000000-20260101-0000" || len(u.Commit) != 40 {
		t.Fatalf("版本信息不对：%+v", u)
	}
	done := strings.Join(steps(en.events(t, job.ID), "step_done"), ",")
	if done != "预检,准备服务器,构建,上传,切换,健康检查,清理" {
		t.Fatalf("完成的步骤：%s", done)
	}
	scripts := srv.all()
	for _, want := range []string{
		"mkdir -p /opt/proj/app/releases/" + u.Version,
		"ln -sfn releases/" + u.Version + " current.tmp",
		"sudo systemctl restart app",
		"systemctl is-active --quiet app",
		"deploy.log",
	} {
		if !strings.Contains(scripts, want) {
			t.Errorf("服务器上应执行包含 %q 的命令", want)
		}
	}
	// 本地确实编译出了产物，版本号注入
	bin := filepath.Join(en.e.dataDir, "build", "proj", "app", u.Version, "app")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("产物不存在：%v", err)
	}
	sums, _ := os.ReadFile(filepath.Join(filepath.Dir(bin), "SHA256SUMS"))
	if !strings.HasSuffix(strings.TrimSpace(string(sums)), "  app") {
		t.Fatalf("SHA256SUMS = %s", sums)
	}
	if en.e.Busy(ref) != 0 {
		t.Fatal("任务结束后应释放锁")
	}
}

func TestHealthFailRollsBack(t *testing.T) {
	srv := &fakeServer{current: "main-0000000-20260101-0000", releases: []string{"main-0000000-20260101-0000"}, failOn: "sleep 3", failTimes: 1}
	en := newEnv(t, srv)
	en.app(t, nil)
	job, _ := en.e.StartDeploy(context.Background(), DeployRequest{Apps: []AppRef{ref}})
	j := en.wait(t, job.ID)
	if j.Status != "rolled_back" || j.Units[0].Status != "rolled_back" {
		t.Fatalf("健康检查失败应自动回滚：%s %s %s", j.Status, j.Units[0].Status, j.Units[0].Error)
	}
	if srv.current != "main-0000000-20260101-0000" {
		t.Fatalf("应切回上一个版本，现在是 %s", srv.current)
	}
	if !strings.Contains(j.Units[0].Error, "60 秒内没有通过") {
		t.Fatalf("健康检查失败的提示应直白说明：%s", j.Units[0].Error)
	}
	if !strings.Contains(srv.all(), "rolled_back") {
		t.Fatal("服务器上的部署记录应写 rolled_back")
	}
	fails := steps(en.events(t, job.ID), "step_fail")
	if len(fails) != 1 || fails[0] != "健康检查" {
		t.Fatalf("失败的步骤：%v", fails)
	}
}

func TestFirstDeployFailNoRollback(t *testing.T) {
	srv := &fakeServer{failOn: "sleep 3"}
	en := newEnv(t, srv)
	en.app(t, nil)
	job, _ := en.e.StartDeploy(context.Background(), DeployRequest{Apps: []AppRef{ref}})
	j := en.wait(t, job.ID)
	if j.Units[0].Status != "failed" || !strings.Contains(j.Units[0].Error, "没有上一个版本") {
		t.Fatalf("没有上一个版本时应失败并说明：%+v", j.Units[0])
	}
}

func TestBusyAndCancel(t *testing.T) {
	srv := &fakeServer{blockOn: "mkdir -p \"$d\"", unblock: make(chan struct{})}
	en := newEnv(t, srv)
	en.app(t, nil)
	job, err := en.e.StartDeploy(context.Background(), DeployRequest{Apps: []AppRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	// 等它卡在「准备服务器」
	for i := 0; i < 500 && !strings.Contains(srv.all(), "mkdir -p \"$d\""); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	var busy *ErrBusy
	if _, err := en.e.StartDeploy(context.Background(), DeployRequest{Apps: []AppRef{ref}}); !errors.As(err, &busy) {
		t.Fatalf("同一个应用同时只能有一个任务：%v", err)
	}
	if err := en.e.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	j := en.wait(t, job.ID)
	if j.Status != "canceled" || j.Units[0].Status != "canceled" {
		t.Fatalf("取消后：%s %s %s", j.Status, j.Units[0].Status, j.Units[0].Error)
	}
	if strings.Contains(srv.all(), "ln -sfn") {
		t.Fatal("取消后不应切换版本")
	}
}

func TestRollback(t *testing.T) {
	srv := &fakeServer{current: "main-bbbbbbb-20260102-0000", releases: []string{"main-aaaaaaa-20260101-0000", "main-bbbbbbb-20260102-0000"}}
	en := newEnv(t, srv)
	en.app(t, nil)

	st, err := en.e.State(context.Background(), ref)
	if err != nil || st.Current != "main-bbbbbbb-20260102-0000" || len(st.Releases) != 2 || st.Releases[0] != "main-bbbbbbb-20260102-0000" {
		t.Fatalf("State = %+v %v", st, err)
	}

	job, err := en.e.StartRollback(context.Background(), RollbackRequest{App: ref})
	if err != nil {
		t.Fatal(err)
	}
	j := en.wait(t, job.ID)
	if j.Status != "success" || j.Units[0].Version != "main-aaaaaaa-20260101-0000" || j.Units[0].Previous != "main-bbbbbbb-20260102-0000" {
		t.Fatalf("回滚结果：%s %+v", j.Status, j.Units[0])
	}
	if srv.current != "main-aaaaaaa-20260101-0000" {
		t.Fatalf("应回滚到上一个版本，现在是 %s", srv.current)
	}

	job, _ = en.e.StartRollback(context.Background(), RollbackRequest{App: ref, Version: "nope"})
	if j := en.wait(t, job.ID); j.Status != "failed" || !strings.Contains(j.Units[0].Error, "没有版本 nope") {
		t.Fatalf("回滚到不存在的版本应失败：%+v", j.Units[0])
	}
}

func TestSecretsMaskedInLogs(t *testing.T) {
	srv := &fakeServer{}
	en := newEnv(t, srv)
	val := "s3cr3t-token-value"
	if err := en.svc.CreateSecret(context.Background(), core.SecretInput{Name: "TOKEN", Value: &val}); err != nil {
		t.Fatal(err)
	}
	en.app(t, map[string]any{"ldflags_x": map[string]any{"main.Version": "${TOKEN}"}})
	job, _ := en.e.StartDeploy(context.Background(), DeployRequest{Apps: []AppRef{ref}})
	en.wait(t, job.ID)
	sawBuild := false
	for _, ev := range en.events(t, job.ID) {
		if strings.Contains(ev.Message, val) {
			t.Fatalf("日志里出现了密钥的值：%s", ev.Message)
		}
		if strings.Contains(ev.Message, "-X main.Version=***") {
			sawBuild = true
		}
	}
	if !sawBuild {
		t.Fatal("构建命令里的密钥应显示为 ***")
	}
}

func TestBlockedByBranch(t *testing.T) {
	srv := &fakeServer{}
	en := newEnv(t, srv)
	en.app(t, map[string]any{"branch": "release"})
	plan, err := en.e.Plan(context.Background(), []AppRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	if plan.OK || !strings.Contains(plan.Items[0].Blocked, "部署要求 release 分支") {
		t.Fatalf("分支不对时不能部署：%+v", plan.Items[0])
	}
	job, _ := en.e.StartDeploy(context.Background(), DeployRequest{Apps: []AppRef{ref}})
	if j := en.wait(t, job.ID); j.Units[0].Status != "failed" || j.Units[0].Step != "预检" {
		t.Fatalf("预检应失败：%+v", j.Units[0])
	}
}

func TestHelpers(t *testing.T) {
	ts := time.Date(2026, 11, 7, 15, 30, 0, 0, time.Local)
	if v := makeVersion("feature/x", "0123456789abcdef", ts); v != "feature-x-0123456-20261107-1530" {
		t.Fatalf("makeVersion = %s", v)
	}
	if v := uniqueVersion("v", []string{"v", "v-2"}); v != "v-3" {
		t.Fatalf("uniqueVersion = %s", v)
	}
	if c := commitOf("main-39fe551-20261107-0211"); c != "39fe551" {
		t.Fatalf("commitOf = %s", c)
	}
	if c := commitOf("feature-x-39fe551-20261107-0211-2"); c != "39fe551" {
		t.Fatalf("commitOf 带后缀 = %s", c)
	}
	r := parseLast("2026-11-07 02:12:00\tshop-api\tmain-39fe551-20261107-0211\tdeploy\tsuccess\tdeploy")
	if r == nil || r.Version != "main-39fe551-20261107-0211" || r.Operator != "deploy" {
		t.Fatalf("parseLast = %+v", r)
	}
}

func (f *fakeServer) RunStdin(context.Context, remote.Target, string, io.Reader, func(string)) error {
	return nil
}

func TestPipeCompresses(t *testing.T) {
	var got string
	var infos []string
	err := pipe(context.Background(), shell.Cmd{Name: "sh", Args: []string{"-c", "printf 'hello image'; echo 警告 >&2"}}, func(r io.Reader) error {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		b, err := io.ReadAll(zr)
		got = string(b)
		return err
	}, func(f string, a ...any) { infos = append(infos, fmt.Sprintf(f, a...)) }, func(string) {})
	if err != nil || got != "hello image" {
		t.Fatalf("pipe = %q %v", got, err)
	}
	if len(infos) != 1 || !strings.Contains(infos[0], "传输了") {
		t.Fatalf("infos = %v", infos)
	}
	err = pipe(context.Background(), shell.Cmd{Name: "sh", Args: []string{"-c", "echo 镜像不存在 >&2; exit 1"}}, func(r io.Reader) error {
		_, err := io.ReadAll(r)
		return err
	}, func(string, ...any) {}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "镜像不存在") {
		t.Fatalf("本地命令失败时应报错并带上错误输出：%v", err)
	}
}

func TestStatusAll(t *testing.T) {
	srv := &fakeServer{}
	en := newEnv(t, srv)
	en.app(t, nil)
	list, err := en.e.StatusAll(context.Background(), "")
	if err != nil || len(list) != 1 || list[0].State != "not_deployed" || list[0].LocalCommit == "" {
		t.Fatalf("StatusAll = %+v %v", list[0], err)
	}
	// 线上版本的提交和本地一致 → 已是最新；不一致 → 有更新
	srv.current = "main-" + list[0].LocalCommit + "-20260101-0000"
	list, _ = en.e.StatusAll(context.Background(), "proj")
	if list[0].State != "up_to_date" {
		t.Fatalf("应为已是最新：%+v", list[0])
	}
	srv.current = "main-abcdef0-20260101-0000"
	list, _ = en.e.StatusAll(context.Background(), "proj")
	if list[0].State != "has_update" {
		t.Fatalf("应为有更新：%+v", list[0])
	}
}

// 代码来源是 git 地址：平台自己克隆，每次部署前拉取分支的最新提交。
func TestDeployFromGitURL(t *testing.T) {
	remoteRepo := gitRepo(t)
	// 应用里填的是符合格式的 https 地址，测试时用 git 的 insteadOf 把它换成本地目录
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	content := "[url \"file://" + remoteRepo + "\"]\n\tinsteadOf = https://git.example.test/app.git\n"
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)

	srv := &fakeServer{}
	en := newEnv(t, srv)
	en.svc.SetDataDir(t.TempDir())
	en.svc.SetGitProtocols("https:file")
	en.app(t, map[string]any{"repo_source": "git", "repo_url": "https://git.example.test/app.git"})
	head := func() string {
		out, err := exec.Command("git", "-C", remoteRepo, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	ctx := context.Background()

	// 部署确认：还没克隆过也能给出要部署的提交
	plan, err := en.e.Plan(ctx, []AppRef{ref})
	if err != nil || !plan.OK || !strings.HasPrefix(head(), plan.Items[0].Commit) || plan.Items[0].Dirty != 0 {
		t.Fatalf("部署确认 = %+v %v", plan.Items[0], err)
	}

	job, err := en.e.StartDeploy(ctx, DeployRequest{Apps: []AppRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	j := en.wait(t, job.ID)
	u := j.Units[0]
	if j.Status != "success" || u.Commit != head() {
		t.Fatalf("任务 %s，提交 %s（远程 %s）：%s", j.Status, u.Commit, head(), u.Error)
	}
	if _, err := os.Stat(filepath.Join(en.e.dataDir, "build", "proj", "app", u.Version, "app")); err != nil {
		t.Fatalf("应从平台克隆的代码编译出产物：%v", err)
	}

	// 远程有新提交：总览状态和下一次部署都用新的
	if err := os.WriteFile(filepath.Join(remoteRepo, "README.md"), []byte("新内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@e", "commit", "-q", "-m", "第二次提交"}} {
		if out, err := exec.Command("git", append([]string{"-C", remoteRepo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	srv.mu.Lock()
	srv.current, srv.releases = u.Version, []string{u.Version}
	srv.mu.Unlock()
	list, err := en.e.StatusAll(ctx, "proj")
	if err != nil || len(list) != 1 || list[0].State != "has_update" || list[0].Subject != "第二次提交" {
		t.Fatalf("总览状态 = %+v %v", list[0], err)
	}
	job, err = en.e.StartDeploy(ctx, DeployRequest{Apps: []AppRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	j = en.wait(t, job.ID)
	if j.Status != "success" || j.Units[0].Commit != head() {
		t.Fatalf("第二次部署 %s，提交 %s（远程 %s）：%s", j.Status, j.Units[0].Commit, head(), j.Units[0].Error)
	}
	var infos []string
	for _, e := range en.events(t, job.ID) {
		if e.Type == "info" {
			infos = append(infos, e.Message)
		}
	}
	// 刚才读总览状态时已经拉取过，这次部署说明当前是哪个提交
	if !strings.Contains(strings.Join(infos, "\n"), "第二次提交") {
		t.Fatalf("日志里应说明部署的是哪个提交：%v", infos)
	}
}

// 通过访问令牌发起的部署：应用要求审批时先进入待审批，批准后才执行。
func TestApprovalFlow(t *testing.T) {
	srv := &fakeServer{current: "main-0000000-20260101-0000", releases: []string{"main-0000000-20260101-0000"}}
	en := newEnv(t, srv)
	en.app(t, nil) // ai_approval 默认打开
	ctx := context.Background()
	viaToken := DeployRequest{Apps: []AppRef{ref}, Initiator: "token:Claude Code", ViaToken: true}

	job, err := en.e.StartDeploy(ctx, viaToken)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "pending_approval" || en.e.Running() != 0 || en.e.Busy(ref) != 0 {
		t.Fatalf("应进入待审批、不执行也不占用应用：%+v", job)
	}
	if len(srv.all()) != 0 {
		t.Fatalf("待审批时不应连服务器：%s", srv.all())
	}
	// 同一个应用不能再有第二个待审批的
	var pending *ErrPending
	if _, err := en.e.StartDeploy(ctx, viaToken); !errors.As(err, &pending) || pending.JobID != job.ID {
		t.Fatalf("重复发起应返回 ErrPending：%v", err)
	}
	// 拒绝：不执行，各应用标记为跳过；之后不能再批准
	if err := en.e.Reject(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	j, _ := en.e.GetJob(ctx, job.ID)
	if j.Status != "rejected" || j.Units[0].Status != "skipped" || j.FinishedAt == nil {
		t.Fatalf("拒绝后 = %+v %+v", j, j.Units[0])
	}
	if _, err := en.e.Approve(ctx, job.ID); !errors.Is(err, ErrNotPending) {
		t.Fatalf("已拒绝的任务不能批准：%v", err)
	}
	if err := en.e.Reject(ctx, job.ID); !errors.Is(err, ErrNotPending) {
		t.Fatalf("不能拒绝两次：%v", err)
	}

	// 批准：开始执行并成功，记录的发起方是令牌
	job, err = en.e.StartDeploy(ctx, viaToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := en.e.Approve(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	j = en.wait(t, job.ID)
	if j.Status != "success" || j.Initiator != "token:Claude Code" || j.Units[0].Status != "success" {
		t.Fatalf("批准后应执行成功：%s %s", j.Status, j.Units[0].Error)
	}
	if _, err := en.e.Approve(ctx, job.ID); !errors.Is(err, ErrNotPending) {
		t.Fatalf("不能批准两次：%v", err)
	}

	// 发起方自己撤回
	job, _ = en.e.StartDeploy(ctx, viaToken)
	if err := en.e.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	if j, _ := en.e.GetJob(ctx, job.ID); j.Status != "canceled" {
		t.Fatalf("撤回后 = %s", j.Status)
	}

	// 回滚同样要审批
	rb, err := en.e.StartRollback(ctx, RollbackRequest{App: ref, Version: "main-0000000-20260101-0000", Initiator: "token:x", ViaToken: true})
	if err != nil || rb.Status != "pending_approval" {
		t.Fatalf("回滚应待审批：%+v %v", rb, err)
	}
	if _, err := en.e.Approve(ctx, rb.ID); err != nil {
		t.Fatal(err)
	}
	if j := en.wait(t, rb.ID); j.Kind != "rollback" || j.Status != "success" {
		t.Fatalf("批准后的回滚 = %s %s", j.Status, j.Units[0].Error)
	}

	// 页面上发起的不用审批
	pg, err := en.e.StartDeploy(ctx, DeployRequest{Apps: []AppRef{ref}})
	if err != nil || pg.Status != "queued" {
		t.Fatalf("页面发起应直接执行：%+v %v", pg, err)
	}
	en.wait(t, pg.ID)
}

// 应用关掉「AI 部署需要审批」后，令牌发起的部署直接执行；批准时应用正在部署则拒绝。
func TestApprovalOffAndBusy(t *testing.T) {
	srv := &fakeServer{blockOn: "systemctl restart", unblock: make(chan struct{})}
	en := newEnv(t, srv)
	en.app(t, map[string]any{"ai_approval": false})
	ctx := context.Background()
	job, err := en.e.StartDeploy(ctx, DeployRequest{Apps: []AppRef{ref}, Initiator: "token:ci", ViaToken: true})
	if err != nil || job.Status != "queued" {
		t.Fatalf("关掉审批后应直接执行：%+v %v", job, err)
	}
	close(srv.unblock)
	if j := en.wait(t, job.ID); j.Status != "success" {
		t.Fatalf("部署 %s：%s", j.Status, j.Units[0].Error)
	}
}

func TestIdempotencyKey(t *testing.T) {
	srv := &fakeServer{}
	en := newEnv(t, srv)
	en.app(t, nil)
	ctx := context.Background()
	first, err := en.e.StartDeploy(ctx, DeployRequest{Apps: []AppRef{ref}, IdempotencyKey: "retry-1"})
	if err != nil {
		t.Fatal(err)
	}
	// 重试：返回原来的任务（哪怕它还在执行），不报「正在部署」，也不新建
	again, err := en.e.StartDeploy(ctx, DeployRequest{Apps: []AppRef{ref}, IdempotencyKey: "retry-1"})
	if err != nil || again.ID != first.ID {
		t.Fatalf("同一个幂等键应返回原来的任务：%+v %v", again, err)
	}
	en.wait(t, first.ID)
	again, _ = en.e.StartDeploy(ctx, DeployRequest{Apps: []AppRef{ref}, IdempotencyKey: "retry-1"})
	if again.ID != first.ID {
		t.Fatal("任务结束后同一个幂等键仍然返回原来的任务")
	}
	other, err := en.e.StartDeploy(ctx, DeployRequest{Apps: []AppRef{ref}, IdempotencyKey: "retry-2"})
	if err != nil || other.ID == first.ID {
		t.Fatalf("不同的幂等键是新任务：%+v %v", other, err)
	}
	en.wait(t, other.ID)
	jobs, _ := en.e.ListJobs(ctx, store.JobFilter{})
	if len(jobs) != 2 {
		t.Fatalf("应只有 2 个任务，实际 %d", len(jobs))
	}
}

func TestJobLogs(t *testing.T) {
	srv := &fakeServer{failOn: "sha256sum -c"}
	en := newEnv(t, srv)
	en.app(t, nil)
	ctx := context.Background()
	job, err := en.e.StartDeploy(ctx, DeployRequest{Apps: []AppRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	// WaitJob：等到任务结束
	j, err := en.e.WaitJob(ctx, job.ID, 60*time.Second)
	if err != nil || j.Status != "failed" || !Finished(j.Status) {
		t.Fatalf("WaitJob = %+v %v", j, err)
	}

	all, err := en.e.Logs(ctx, job.ID, LogQuery{After: -1, Limit: 2000})
	if err != nil || len(all.Lines) < 8 || all.Status != "failed" {
		t.Fatalf("全部日志 = %d 行 %v", len(all.Lines), err)
	}
	text := strings.Join(all.Lines, "\n")
	for _, want := range []string{"▶ 预检", "✓ 预检 完成", "✗ 上传 失败"} {
		if !strings.Contains(text, want) {
			t.Errorf("日志里应有 %q：\n%s", want, text)
		}
	}
	// 最后几行
	tail, _ := en.e.Logs(ctx, job.ID, LogQuery{Tail: 3})
	if len(tail.Lines) != 3 || tail.Omitted != len(all.Lines)-3 || tail.Lines[2] != all.Lines[len(all.Lines)-1] {
		t.Fatalf("tail = %+v", tail)
	}
	// 只要失败的步骤：有上传那一步的，没有构建成功的
	failed, _ := en.e.Logs(ctx, job.ID, LogQuery{FailedOnly: true, After: -1})
	ftext := strings.Join(failed.Lines, "\n")
	if !strings.Contains(ftext, "✗ 上传 失败") || strings.Contains(ftext, "预检") || len(failed.Lines) >= len(all.Lines) {
		t.Fatalf("只看失败步骤 =\n%s", ftext)
	}
	// 分页：每次 4 行，用 next 接着读，拼起来和全部一样
	var paged []string
	after := int64(-1)
	for i := 0; i < 100; i++ {
		p, _ := en.e.Logs(ctx, job.ID, LogQuery{After: after, Limit: 4})
		paged = append(paged, p.Lines...)
		if p.Omitted == 0 {
			break
		}
		after = p.Next
	}
	if strings.Join(paged, "\n") != text {
		t.Fatalf("分页读到 %d 行，全部 %d 行", len(paged), len(all.Lines))
	}
	if _, err := en.e.Logs(ctx, 9999, LogQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的任务：%v", err)
	}
}
