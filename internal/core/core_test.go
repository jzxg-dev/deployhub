package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"deployhub/internal/detect"
	"deployhub/internal/driver/builtin"
	"deployhub/internal/remote"
	"deployhub/internal/store"
	"deployhub/internal/vault"
)

// fakeSSH 记录执行的脚本，返回预设结果。
type fakeSSH struct {
	out     string
	err     error
	scripts []string
}

func (f *fakeSSH) Run(_ context.Context, _ remote.Target, script string) (*remote.Result, error) {
	f.scripts = append(f.scripts, script)
	if f.err != nil {
		return nil, f.err
	}
	return &remote.Result{Stdout: f.out}, nil
}

func newService(t *testing.T) (*Service, *fakeSSH, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "deployhub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	v, _ := vault.New(bytes.Repeat([]byte{7}, 32))
	reg, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	ssh := &fakeSSH{out: "machine=x86_64\nuser=deploy\nos=Ubuntu\nsystemd=1\n"}
	s := New(st, v, reg, ssh)
	s.SetRepoDetector(detect.RepoDetector{LocalGoVersion: func(context.Context) string { return "go1.27" }})
	return s, ssh, st
}

func fieldMessages(err error) map[string]string {
	m := map[string]string{}
	var e *Error
	if errors.As(err, &e) {
		for _, f := range e.Fields {
			m[f.Field] = f.Message
		}
	}
	return m
}

func kind(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

func TestServerValidation(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	_, err := s.CreateServer(ctx, ServerInput{Name: "Bad", Host: "a b", Port: 70000, KeyPath: "/no/such/key",
		SSHOptions: []string{"ProxyCommand=nc %h %p"}})
	msgs := fieldMessages(err)
	for _, f := range []string{"name", "host", "port", "keyPath", "sshOptions"} {
		if msgs[f] == "" {
			t.Errorf("%s 应报错，实际 %v", f, msgs)
		}
	}
	if !strings.Contains(msgs["sshOptions"], "本机执行命令") {
		t.Errorf("ProxyCommand 应被拒绝：%s", msgs["sshOptions"])
	}
}

func TestServerTestSavesFacts(t *testing.T) {
	s, ssh, _ := newService(t)
	ctx := context.Background()
	if _, err := s.CreateServer(ctx, ServerInput{Name: "shop-main", Host: "1.2.3.4", User: "deploy"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.TestServer(ctx, "shop-main")
	if err != nil || !res.OK || res.Facts.Arch != "amd64" {
		t.Fatalf("TestServer = %+v %v", res, err)
	}
	srv, _ := s.GetServer(ctx, "shop-main")
	if srv.Facts == nil || !srv.Facts.Systemd || srv.Port != 22 {
		t.Fatalf("检测结果应保存，端口默认 22：%+v", srv)
	}

	ssh.err = &remote.ConnectError{Reason: "认证失败", Detail: "Permission denied"}
	res, _ = s.TestServer(ctx, "shop-main")
	if res.OK || !strings.Contains(res.Error, "认证失败") || res.Detail != "Permission denied" {
		t.Fatalf("连接失败时应返回原因：%+v", res)
	}
}

func TestSecrets(t *testing.T) {
	s, _, st := newService(t)
	ctx := context.Background()
	val := "https://api.example.com"
	if err := s.CreateSecret(ctx, SecretInput{Name: "bad-name", Value: &val}); fieldMessages(err)["name"] == "" {
		t.Fatal("名称格式不对应报错")
	}
	if err := s.CreateSecret(ctx, SecretInput{Name: "API_URL", Value: &val, Description: "接口"}); err != nil {
		t.Fatal(err)
	}
	enc, _ := st.SecretValue(ctx, "API_URL")
	if strings.Contains(enc, "example") {
		t.Fatal("库里应保存密文")
	}
	if got, _ := s.RevealSecret(ctx, "API_URL"); got != val {
		t.Fatalf("RevealSecret = %s", got)
	}
	if err := s.UpdateSecret(ctx, "API_URL", SecretInput{Description: "新说明"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.RevealSecret(ctx, "API_URL"); got != val {
		t.Fatal("不传值时值不变")
	}
	if _, err := s.RevealSecret(ctx, "NOPE"); kind(err) != KindNotFound {
		t.Fatal("不存在的密钥应返回 KindNotFound")
	}
}

// setup 建好项目、服务器和一个仓库目录
func setup(t *testing.T, s *Service) string {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CreateProject(ctx, ProjectInput{Name: "shop", Title: "示例商城"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServer(ctx, ServerInput{Name: "shop-main", Host: "1.2.3.4"}); err != nil {
		t.Fatal(err)
	}
	return t.TempDir()
}

func appParams(repo string) map[string]any {
	return map[string]any{
		"name": "shop-mcp", "title": "MCP", "repo_path": repo, "branch": "main", "server": "shop-main",
		"build_args":   map[string]any{"API": "${API_URL}"},
		"config_files": []any{map[string]any{"name": "config.toml", "content": "secret = 1"}},
	}
}

func TestAppLifecycle(t *testing.T) {
	s, _, st := newService(t)
	ctx := context.Background()
	repo := setup(t, s)

	// 引用的密钥不存在
	_, err := s.CreateApp(ctx, "shop", AppInput{Driver: "image", Params: appParams(repo)})
	if msg := fieldMessages(err)["build_args"]; !strings.Contains(msg, "${API_URL} 不存在") {
		t.Fatalf("应提示密钥不存在：%v", err)
	}
	val := "x"
	_ = s.CreateSecret(ctx, SecretInput{Name: "API_URL", Value: &val})

	a, err := s.CreateApp(ctx, "shop", AppInput{Driver: "image", Params: appParams(repo)})
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision != 1 || a.Server != "shop-main" || a.Params["transfer"] != "ssh" {
		t.Fatalf("新建结果不对：%+v", a)
	}
	files := a.Params["config_files"].([]any)
	if files[0].(map[string]any)["content"] != "secret = 1" {
		t.Fatal("GetApp 应返回解密后的配置文件内容")
	}
	// 库里是密文
	raw, _ := st.GetApp(ctx, "shop", "shop-mcp")
	if c := raw.Params["config_files"].([]any)[0].(map[string]any)["content"].(string); !vault.IsEncrypted(c) {
		t.Fatal("配置文件内容应加密保存")
	}
	// 列表里不返回内容
	list, _ := s.ListApps(ctx, "shop")
	if c := list[0].Params["config_files"].([]any)[0].(map[string]any)["content"]; c != "******" {
		t.Fatalf("列表里的敏感内容应隐藏：%v", c)
	}

	// 重名
	if _, err := s.CreateApp(ctx, "shop", AppInput{Driver: "image", Params: appParams(repo)}); fieldMessages(err)["name"] == "" {
		t.Fatal("同名应用应报错")
	}

	// 修改
	p := appParams(repo)
	p["title"] = "AI 接入"
	if _, err := s.UpdateApp(ctx, "shop", "shop-mcp", AppInput{Params: p, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateApp(ctx, "shop", "shop-mcp", AppInput{Params: p, Revision: 1}); kind(err) != KindConflict {
		t.Fatalf("基于旧版本修改应冲突：%v", err)
	}
	p["name"] = "renamed"
	if _, err := s.UpdateApp(ctx, "shop", "shop-mcp", AppInput{Params: p, Revision: 2}); fieldMessages(err)["name"] == "" {
		t.Fatal("应用标识不能改")
	}
	if _, err := s.UpdateApp(ctx, "shop", "shop-mcp", AppInput{Driver: "static", Params: appParams(repo), Revision: 2}); fieldMessages(err)["driver"] == "" {
		t.Fatal("部署方式不能改")
	}

	// 修改记录和恢复
	revs, _ := s.ListRevisions(ctx, "shop", "shop-mcp")
	if len(revs) != 2 || revs[1].Params["title"] != "MCP" {
		t.Fatalf("修改记录不对：%d", len(revs))
	}
	a, err = s.RestoreRevision(ctx, "shop", "shop-mcp", 1)
	if err != nil || a.Revision != 3 || a.Title != "MCP" {
		t.Fatalf("恢复后 = %+v %v", a, err)
	}

	// 被引用的密钥、服务器、有应用的项目不能删
	if err := s.DeleteSecret(ctx, "API_URL"); kind(err) != KindConflict {
		t.Fatalf("被引用的密钥不能删：%v", err)
	}
	secrets, _ := s.ListSecrets(ctx)
	if len(secrets[0].UsedBy) != 1 || secrets[0].UsedBy[0] != "shop/shop-mcp" {
		t.Fatalf("UsedBy = %v", secrets[0].UsedBy)
	}
	if err := s.DeleteServer(ctx, "shop-main"); kind(err) != KindConflict {
		t.Fatal("有应用的服务器不能删")
	}
	if err := s.DeleteProject(ctx, "shop"); kind(err) != KindConflict {
		t.Fatal("有应用的项目不能删")
	}
	if err := s.DeleteApp(ctx, "shop", "shop-mcp"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSecret(ctx, "API_URL"); err != nil {
		t.Fatal(err)
	}
}

func TestAppServerMustExist(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	repo := setup(t, s)
	p := appParams(repo)
	p["server"] = "nope"
	delete(p, "build_args")
	err := s.ValidateApp(ctx, "shop", "", AppInput{Driver: "image", Params: p})
	if !strings.Contains(fieldMessages(err)["server"], "不存在") {
		t.Fatalf("服务器不存在应报错：%v", err)
	}
}

func TestDetectRepo(t *testing.T) {
	s, _, _ := newService(t)
	r := s.DetectRepo(context.Background(), "/no/such")
	if r.Error == "" {
		t.Fatal("不存在的目录应报错")
	}
}

// checkSSH 对检测脚本返回环境信息，对检查脚本让包含 failWord 的检查失败。
type checkSSH struct{ failWord string }

func (f *checkSSH) Run(_ context.Context, _ remote.Target, script string) (*remote.Result, error) {
	if !strings.Contains(script, "CHECK") {
		return &remote.Result{Stdout: "machine=aarch64\nuser=deploy\nos=Debian\nsystemd=1\n"}, nil
	}
	var out strings.Builder
	for i, line := range strings.Split(strings.TrimSpace(script), "\n") {
		status := "ok"
		if f.failWord != "" && strings.Contains(line, f.failWord) {
			status = "fail"
		}
		out.WriteString("CHECK " + strconv.Itoa(i) + " " + status + "\n")
	}
	return &remote.Result{Stdout: out.String()}, nil
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "app", "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@e", "commit", "-q", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return root
}

func TestCheckAndPreview(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	setup(t, s)
	repo := gitRepo(t)
	params := map[string]any{"name": "app", "title": "应用", "repo_path": repo, "branch": "main", "server": "shop-main", "main": "./cmd/app"}
	if _, err := s.CreateApp(ctx, "shop", AppInput{Driver: "gobinary", Params: params}); err != nil {
		t.Fatal(err)
	}

	s.ssh = &checkSSH{failWord: "systemctl cat"}
	res, err := s.CheckApp(ctx, "shop", "app")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]CheckItem{}
	for _, it := range res.Items {
		byName[it.Name] = it
	}
	if byName["仓库"].Status != "ok" || byName["部署分支"].Status != "ok" || byName["连接"].Status != "ok" {
		t.Fatalf("本机和连接检查应通过：%+v", res.Items)
	}
	if byName["编译入口 ./cmd/app"].Status != "ok" {
		t.Fatalf("编译入口应存在：%+v", byName["编译入口 ./cmd/app"])
	}
	// 服务不存在只是提醒
	if it := byName["systemd 服务 app"]; it.Status != "warn" || !strings.Contains(it.Message, "首次部署时按模板安装") {
		t.Fatalf("服务不存在应提醒：%+v", it)
	}
	if !res.OK {
		t.Fatalf("只有提醒时应算通过：%+v", res.Items)
	}
	// 检查时更新了服务器检测结果
	srv, _ := s.GetServer(ctx, "shop-main")
	if srv.Facts == nil || srv.Facts.Arch != "arm64" {
		t.Fatal("检查时应更新服务器检测结果")
	}

	s.ssh = &checkSSH{failWord: "command -v systemctl"}
	res, _ = s.CheckApp(ctx, "shop", "app")
	if res.OK {
		t.Fatal("没有 systemd 时检查应不通过")
	}

	// 预览：已保存的参数
	pv, err := s.PreviewApp(ctx, "shop", "app", AppInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pv.Version, "main-") || len(pv.Plan.Steps) == 0 {
		t.Fatalf("预览 = %+v", pv)
	}
	// 预览：新建中、参数不完整
	pv, err = s.PreviewApp(ctx, "shop", "", AppInput{Driver: "image", Params: map[string]any{"title": "x"}})
	if err != nil || len(pv.Plan.Steps) == 0 {
		t.Fatalf("参数不完整也应能预览：%v", err)
	}
}

func TestImportHistory(t *testing.T) {
	s, _, st := newService(t)
	ctx := context.Background()
	setup(t, s)
	repo := t.TempDir()
	if _, err := s.CreateApp(ctx, "shop", AppInput{Driver: "gobinary", Params: map[string]any{
		"name": "shop-api", "title": "支付 API", "repo_path": repo, "branch": "main", "server": "shop-main", "main": "./cmd/shop-api"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "history.jsonl")
	lines := `{"unit": "shop-mcp", "action": "build", "version": "v0", "previous": "", "result": "built", "server": "s", "operator": "deploy", "time": "2026-11-02 18:16:32", "error": ""}
{"unit": "shop-api", "action": "deploy", "version": "main-48f8ac4-20261102-2000", "previous": "", "result": "failed", "server": "backend", "operator": "deploy", "time": "2026-11-02 20:00:56", "error": "rsync 失败"}
{"unit": "shop-api", "action": "deploy", "version": "main-39fe551-20261107-0211", "previous": "main-48f8ac4-20261102-2000", "result": "success", "server": "backend", "operator": "deploy", "time": "2026-11-07 02:12:00", "error": ""}
{"unit": "old-unit", "action": "deploy", "version": "v1", "previous": "", "result": "success", "server": "x", "operator": "deploy", "time": "2026-11-07 02:13:00", "error": ""}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := s.ImportHistory(ctx, "shop", path)
	if err != nil || res.Imported != 3 || res.Skipped != 1 || len(res.Unknown) != 1 || res.Unknown[0] != "old-unit" {
		t.Fatalf("导入结果 = %+v %v", res, err)
	}
	// 重复导入全部跳过
	res, _ = s.ImportHistory(ctx, "shop", path)
	if res.Imported != 0 || res.Skipped != 4 {
		t.Fatalf("重复导入应全部跳过：%+v", res)
	}
	jobs, _ := st.ListJobs(ctx, store.JobFilter{Project: "shop", App: "shop-api"})
	if len(jobs) != 2 || jobs[0].Status != "success" || jobs[0].Units[0].Title != "支付 API" || jobs[0].Initiator != "旧工具 deploy" {
		t.Fatalf("导入的记录不对：%+v", jobs[0])
	}
	if jobs[1].Units[0].Error != "rsync 失败" {
		t.Fatal("失败原因应保留")
	}
}

func TestExportImport(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	repo := setup(t, s)
	val := "https://x"
	_ = s.CreateSecret(ctx, SecretInput{Name: "API_URL", Value: &val})
	if _, err := s.CreateApp(ctx, "shop", AppInput{Driver: "image", Params: appParams(repo)}); err != nil {
		t.Fatal(err)
	}

	plain, err := s.ExportAll(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(plain)
	if strings.Contains(string(b), "https://x") || strings.Contains(string(b), "secret = 1") {
		t.Fatalf("不含敏感内容的导出里出现了明文：%s", b)
	}
	full, _ := s.ExportAll(ctx, true)
	b, _ = json.Marshal(full)
	if !strings.Contains(string(b), "https://x") || !strings.Contains(string(b), "secret = 1") {
		t.Fatal("包含敏感内容的导出应有密钥值和配置文件内容")
	}

	// 导入到一个新的数据库
	s2, _, _ := newService(t)
	var in Export
	_ = json.Unmarshal(b, &in)
	res, err := s2.ImportAll(ctx, &in)
	if err != nil || len(res.Failed) != 0 || len(res.Created) != 4 {
		t.Fatalf("导入结果 = %+v %v", res, err)
	}
	a, err := s2.GetApp(ctx, "shop", "shop-mcp")
	if err != nil || a.Params["config_files"].([]any)[0].(map[string]any)["content"] != "secret = 1" {
		t.Fatalf("导入后的应用不对：%+v %v", a, err)
	}
	// 再导入一次：已存在的跳过
	res, _ = s2.ImportAll(ctx, &in)
	if len(res.Created) != 0 || len(res.Skipped) != 4 {
		t.Fatalf("重复导入应全部跳过：%+v", res)
	}

	// 不含敏感内容的导出：密钥要手动补，配置文件内容为空时提醒
	s3, _, _ := newService(t)
	b, _ = json.Marshal(plain)
	var in2 Export
	_ = json.Unmarshal(b, &in2)
	res, _ = s3.ImportAll(ctx, &in2)
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, ";"), "API_URL") {
		t.Fatalf("应提醒手动添加密钥：%+v", res)
	}
	if len(res.Failed) != 1 || !strings.Contains(res.Failed[0], "shop-mcp") {
		t.Fatalf("引用的密钥不存在，应用导入应失败并说明：%+v", res)
	}
}
