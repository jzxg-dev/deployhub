package engine

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"deployhub/internal/cloudflare/cftest"
	"deployhub/internal/core"
	"deployhub/internal/model"
)

const (
	cfAccount = "0123456789abcdef0123456789abcdef"
	cfToken   = "good-token-1234567890"
)

// workersRepo 是一个 Workers 仓库：wrangler.jsonc（带注释和结尾逗号）、假 wrangler。
func workersRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"wrangler.jsonc": "{\n  // 测试用的 Worker\n  \"name\": \"demo-worker\",\n  \"main\": \"worker.js\",\n  \"compatibility_date\": \"2026-09-18\",\n}\n",
		"worker.js":      "export default { fetch() { return new Response('ok') } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := cftest.WriteFakeWrangler(root); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "init", "-q", "-b", "main")
	commitAll(t, root, "初始提交")
	return root
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "-c", "user.name=t", "-c", "user.email=t@e", "commit", "-q", "-m", msg)
}

type workersEnv struct {
	*env
	cf   *cftest.Server
	url  string
	repo string
}

func newWorkersEnv(t *testing.T, extra map[string]any) *workersEnv {
	t.Helper()
	cf := cftest.New(cfAccount, cfToken)
	ts := httptest.NewServer(cf)
	t.Cleanup(ts.Close)
	t.Setenv("CLOUDFLARE_API_BASE_URL", ts.URL+"/client/v4")
	en := newEnv(t, &fakeServer{})
	ctx := context.Background()
	if _, err := en.svc.CreateCredential(ctx, core.CredentialInput{Name: "cf", Kind: "cloudflare", Title: "测试账号",
		Values: map[string]string{"account_id": cfAccount, "api_token": cfToken}}); err != nil {
		t.Fatal(err)
	}
	repo := workersRepo(t)
	params := map[string]any{
		"name": "app", "title": "Worker", "repo_path": repo, "branch": "main", "sync": "offline",
		"account": "cf", "build_command": cftest.InstallCommand,
		"health_mode": "http", "health_url": ts.URL + "/__worker/demo-worker", "health_timeout": 5,
	}
	for k, v := range extra {
		params[k] = v
	}
	if _, err := en.svc.CreateApp(ctx, "proj", core.AppInput{Driver: "workers", Params: params}); err != nil {
		t.Fatal(err)
	}
	return &workersEnv{env: en, cf: cf, url: ts.URL, repo: repo}
}

func (w *workersEnv) deploy(t *testing.T) (*model.Job, []*model.JobEvent) {
	t.Helper()
	job, err := w.e.StartDeploy(context.Background(), DeployRequest{Apps: []AppRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	j := w.wait(t, job.ID)
	return j, w.events(t, job.ID)
}

func logText(evs []*model.JobEvent) string {
	var b strings.Builder
	for _, e := range evs {
		b.WriteString(e.Type + " " + e.Message + "\n")
	}
	return b.String()
}

func TestWorkersDeployRollbackAndManualRollback(t *testing.T) {
	w := newWorkersEnv(t, nil)

	// 第一次部署：新建 Worker
	j, evs := w.deploy(t)
	u := j.Units[0]
	if j.Status != "success" {
		t.Fatalf("第一次部署应成功：%s %s\n%s", j.Status, u.Error, logText(evs))
	}
	if u.Server != "" || u.Previous != "" {
		t.Fatalf("Workers 没有服务器，第一次部署没有线上版本：%+v", u)
	}
	if done := strings.Join(steps(evs, "step_done"), ","); done != "预检,检查账号,构建,发布,健康检查" {
		t.Fatalf("完成的步骤：%s", done)
	}
	if w.srv.all() != "" {
		t.Fatalf("Workers 不应该连服务器：%s", w.srv.all())
	}
	v1 := u.Version
	if _, msg := w.cf.Current("demo-worker"); !strings.HasPrefix(msg, v1+" 初始提交") {
		t.Fatalf("版本说明应以版本号开头：%q", msg)
	}
	log := logText(evs)
	if strings.Contains(log, cfToken) || !strings.Contains(log, "token=***") {
		t.Fatalf("日志里的 API Token 应被遮盖：\n%s", log)
	}

	// 第二次：坏版本（Worker 返回 500），健康检查失败后自动回滚到第一次的版本
	if err := os.WriteFile(filepath.Join(w.repo, "BROKEN"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, w.repo, "坏版本")
	j, evs = w.deploy(t)
	u = j.Units[0]
	if j.Status != "rolled_back" || u.Previous != v1 {
		t.Fatalf("坏版本应自动回滚：%s %+v\n%s", j.Status, u, logText(evs))
	}
	if !strings.Contains(u.Error, "HTTP 地址") {
		t.Fatalf("失败原因应说明健康检查：%s", u.Error)
	}
	if _, msg := w.cf.Current("demo-worker"); !strings.HasPrefix(msg, v1+" ") {
		t.Fatalf("自动回滚后线上应是第一次的版本：%q", msg)
	}
	broken := u.Version

	// 第三次：修好后部署成功，再手动回滚到第一次的版本
	if err := os.Remove(filepath.Join(w.repo, "BROKEN")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, w.repo, "修好")
	j, evs = w.deploy(t)
	if j.Status != "success" || j.Units[0].Previous != v1 {
		t.Fatalf("修好后应部署成功：%s %+v\n%s", j.Status, j.Units[0], logText(evs))
	}
	v3 := j.Units[0].Version

	st, err := w.e.State(context.Background(), ref)
	if err != nil || !st.Reachable || st.Current != v3 {
		t.Fatalf("线上状态：%+v %v", st, err)
	}
	if len(st.Releases) != 3 || st.Releases[0] != v3 || st.Releases[1] != broken || st.Releases[2] != v1 {
		t.Fatalf("可以回滚的版本（新的在前）：%v", st.Releases)
	}

	job, err := w.e.StartRollback(context.Background(), RollbackRequest{App: ref, Version: v1})
	if err != nil {
		t.Fatal(err)
	}
	j = w.wait(t, job.ID)
	if j.Status != "success" || j.Units[0].Previous != v3 {
		t.Fatalf("手动回滚应成功：%s %+v\n%s", j.Status, j.Units[0], logText(w.events(t, job.ID)))
	}
	st, _ = w.e.State(context.Background(), ref)
	if st.Current != v1 || st.Last == nil || st.Last.Action != "rollback" {
		t.Fatalf("回滚后线上应是 %s：%+v %+v", v1, st, st.Last)
	}
	status, _ := w.e.StatusAll(context.Background(), "proj")
	if len(status) != 1 || status[0].State != "has_update" {
		t.Fatalf("回滚到旧版本后应显示有更新：%+v", status[0])
	}
}

func TestWorkersStateWithForeignVersions(t *testing.T) {
	w := newWorkersEnv(t, nil)
	// 控制台发布的版本：没有部署平台的版本号，用标签或版本 ID 前 8 位
	w.cf.AddVersion("demo-worker", "", "")
	id := w.cf.AddVersion("demo-worker", "v1.2", "控制台发布")
	st, err := w.e.State(context.Background(), ref)
	if err != nil || st.Current != "v1.2" || len(st.Releases) != 2 || len(st.Releases[1]) != 8 {
		t.Fatalf("状态：%+v %v", st, err)
	}
	// 第一次通过部署平台部署时，线上已有的版本可以作为回滚目标
	j, evs := w.deploy(t)
	if j.Status != "success" || j.Units[0].Previous != "v1.2" {
		t.Fatalf("部署：%s %+v\n%s", j.Status, j.Units[0], logText(evs))
	}
	job, _ := w.e.StartRollback(context.Background(), RollbackRequest{App: ref})
	j = w.wait(t, job.ID)
	if cur, _ := w.cf.Current("demo-worker"); j.Status != "success" || cur != id {
		t.Fatalf("回滚到上一个版本：%s %+v", j.Status, j.Units[0])
	}
}

func TestWorkersBadToken(t *testing.T) {
	w := newWorkersEnv(t, nil)
	if _, err := w.svc.UpdateCredential(context.Background(), "cf", core.CredentialInput{Title: "坏的",
		Values: map[string]string{"account_id": cfAccount, "api_token": "wrong-token"}}); err != nil {
		t.Fatal(err)
	}
	j, evs := w.deploy(t)
	if j.Status != "failed" || !strings.Contains(j.Units[0].Error, "API Token") {
		t.Fatalf("Token 不对时应在预检失败并说明：%s %+v\n%s", j.Status, j.Units[0], logText(evs))
	}
}
