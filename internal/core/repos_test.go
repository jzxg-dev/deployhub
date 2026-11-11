package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"deployhub/internal/credential"
)

// newKey 用 ssh-keygen 生成一把没有口令的测试私钥，返回私钥内容。
func newKey(t *testing.T, passphrase string) string {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("本机没有 ssh-keygen")
	}
	path := filepath.Join(t.TempDir(), "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", passphrase, "-C", "deployhub-test", "-f", path).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func keyInput(name, key string) CredentialInput {
	return CredentialInput{Name: name, Kind: credential.SSHKey, Title: "部署密钥", Values: map[string]string{credential.SSHKeyField: key}}
}

func TestSSHKeyCredential(t *testing.T) {
	s, _, _ := newService(t)
	s.SetDataDir(t.TempDir())
	ctx := context.Background()
	key := newKey(t, "")

	// 格式不对、填成公钥、带口令的私钥都不让保存
	for name, bad := range map[string]string{
		"不是私钥": "hello",
		"公钥":   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ deploy",
		"内容损坏": "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
		"带口令":  newKey(t, "secret-pass"),
	} {
		_, err := s.CreateCredential(ctx, keyInput("bad", bad))
		if fieldMessages(err)[credential.SSHKeyField] == "" {
			t.Errorf("%s 应该在私钥字段上报错，实际 %v", name, err)
		}
	}

	// Windows 换行、首尾空白都能用
	c, err := s.CreateCredential(ctx, keyInput("deploy-key", "\n  "+strings.ReplaceAll(key, "\n", "\r\n")+"  "))
	if err != nil {
		t.Fatalf("保存私钥：%v", err)
	}
	if len(c.Secrets) != 1 || len(c.Values) != 0 {
		t.Fatalf("私钥应作为敏感字段保存，不返回内容：%+v", c)
	}
	c, err = s.TestCredential(ctx, "deploy-key")
	if err != nil || len(c.Info) != 2 || !strings.HasPrefix(c.Info[1].Value, "ssh-ed25519 ") || !strings.Contains(c.Info[0].Value, "SHA256:") {
		t.Fatalf("验证应返回指纹和公钥：%+v %v", c, err)
	}

	// 服务器选平台保存的密钥
	if _, err := s.CreateServer(ctx, ServerInput{Name: "srv", Host: "1.2.3.4", KeyCredential: "nope"}); fieldMessages(err)["keyCredential"] == "" {
		t.Fatalf("不存在的凭据应报错：%v", err)
	}
	if _, err := s.CreateCredential(ctx, cfInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServer(ctx, ServerInput{Name: "srv", Host: "1.2.3.4", KeyCredential: cfInput().Name}); fieldMessages(err)["keyCredential"] == "" {
		t.Fatalf("不是 SSH 私钥的凭据应报错：%v", err)
	}
	if _, err := s.CreateServer(ctx, ServerInput{Name: "srv", Host: "1.2.3.4", KeyCredential: "deploy-key", KeyPath: "~/.ssh/id_ed25519"}); fieldMessages(err)["keyPath"] == "" {
		t.Fatalf("两种密钥只能填一种：%v", err)
	}
	srv, err := s.CreateServer(ctx, ServerInput{Name: "srv", Host: "1.2.3.4", KeyCredential: "deploy-key"})
	if err != nil || srv.KeyCredential != "deploy-key" {
		t.Fatalf("新增服务器 = %+v %v", srv, err)
	}
	// 没准备私钥文件时不能悄悄用本机默认的密钥
	if got := ServerTarget(srv).KeyPath; got == "" || fileExists(got) {
		t.Fatalf("没准备私钥时应指向不存在的文件：%q", got)
	}
	srv, err = s.ConnectableServer(ctx, "srv")
	if err != nil {
		t.Fatal(err)
	}
	file := ServerTarget(srv).KeyPath
	st, err := os.Stat(file)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("私钥文件应存在且权限 600：%v %v", st, err)
	}
	if b, _ := os.ReadFile(file); string(b) != key {
		t.Fatal("私钥文件内容应和原私钥一致（换行已整理）")
	}
	if dir, _ := os.Stat(filepath.Dir(file)); dir.Mode().Perm() != 0o700 {
		t.Fatalf("私钥目录权限应为 700：%v", dir.Mode())
	}
	if res, err := s.TestServer(ctx, "srv"); err != nil || !res.OK {
		t.Fatalf("测试连接 = %+v %v", res, err)
	}
	if res, err := s.TestServerInput(ctx, ServerInput{Host: "1.2.3.4", KeyCredential: "deploy-key"}); err != nil || !res.OK {
		t.Fatalf("用没保存的服务器测试连接 = %+v %v", res, err)
	}

	// 还有服务器在用时不能删
	list, _ := s.ListCredentials(ctx)
	var used []string
	for _, c := range list {
		if c.Name == "deploy-key" {
			used = c.UsedBy
		}
	}
	if len(used) != 1 || used[0] != "服务器 srv" {
		t.Fatalf("应列出在用的服务器：%v", used)
	}
	if err := s.DeleteCredential(ctx, "deploy-key"); kind(err) != KindConflict {
		t.Fatalf("还在用的凭据不应能删除：%v", err)
	}

	// 换私钥后旧文件删除，新私钥用新文件
	key2 := newKey(t, "")
	if _, err := s.UpdateCredential(ctx, "deploy-key", keyInput("", key2)); err != nil {
		t.Fatal(err)
	}
	if fileExists(file) {
		t.Fatal("换私钥后旧的私钥文件应删除")
	}
	srv, _ = s.ConnectableServer(ctx, "srv")
	if b, _ := os.ReadFile(srv.KeyFile); string(b) != key2 {
		t.Fatal("应使用新私钥")
	}
	// 服务器改用别的方式后可以删，私钥文件一起删
	if _, err := s.UpdateServer(ctx, "srv", ServerInput{Host: "1.2.3.4"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCredential(ctx, "deploy-key"); err != nil {
		t.Fatal(err)
	}
	if fileExists(srv.KeyFile) {
		t.Fatal("删除凭据后私钥文件应删除")
	}

	// 导出导入：凭据在服务器之前导入
	if _, err := s.CreateCredential(ctx, keyInput("deploy-key", key)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateServer(ctx, "srv", ServerInput{Host: "1.2.3.4", KeyCredential: "deploy-key"}); err != nil {
		t.Fatal(err)
	}
	exp, err := s.ExportAll(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	s2, _, _ := newService(t)
	s2.SetDataDir(t.TempDir())
	res, err := s2.ImportAll(ctx, exp)
	if err != nil || len(res.Failed) != 0 {
		t.Fatalf("导入 = %+v %v", res, err)
	}
	if srv, err := s2.ConnectableServer(ctx, "srv"); err != nil || srv.KeyFile == "" {
		t.Fatalf("导入后服务器应能用平台保存的密钥：%+v %v", srv, err)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@e"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, repo, name, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", msg)
	return gitIn(t, repo, "rev-parse", "--short", "HEAD")
}

func TestManagedRepo(t *testing.T) {
	s, _, _ := newService(t)
	s.SetDataDir(t.TempDir())
	s.SetGitProtocols("file")
	ctx := context.Background()
	remote := gitRepo(t)
	first := gitIn(t, remote, "rev-parse", "--short", "HEAD")
	params := map[string]any{"repo_source": "git", "repo_url": "file://" + remote, "branch": "main"}

	// 没克隆过、不联网时：结果带错误，不创建目录
	dir := s.RepoDir("proj", "app", params)
	if !strings.HasPrefix(dir, s.dataDir) || !strings.HasSuffix(dir, filepath.Join("repos", "proj", "app")) {
		t.Fatalf("平台克隆的位置不对：%s", dir)
	}
	if repo := s.AppRepo(ctx, "proj", "app", params, RepoNoSync, nil); repo.Error == "" || fileExists(dir) {
		t.Fatalf("没克隆过时应提示：%+v", repo)
	}

	var logs []string
	logf := func(_ bool, msg string) { logs = append(logs, msg) }
	repo := s.AppRepo(ctx, "proj", "app", params, RepoSyncJob, logf)
	if repo.Error != "" || repo.Branch != "main" || repo.Commit != first || repo.Path != dir {
		t.Fatalf("克隆后 = %+v", repo)
	}
	if len(logs) < 2 || !strings.Contains(logs[0], "克隆") {
		t.Fatalf("应说明克隆过程：%v", logs)
	}
	// 地址不写进 .git/config（里面可能有令牌）
	if cfg, _ := os.ReadFile(filepath.Join(dir, ".git", "config")); strings.Contains(string(cfg), remote) {
		t.Fatalf(".git/config 里不应保存地址：%s", cfg)
	}

	// 远程有新提交：拉取后是最新的；构建留下的文件不算未提交的改动，本地对已跟踪文件的改动被丢弃
	second := commitFile(t, remote, "new.txt", "v2", "第二次提交")
	_ = os.WriteFile(filepath.Join(dir, "build-output.bin"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("被改乱了"), 0o644)
	repo = s.AppRepo(ctx, "proj", "app", params, RepoSyncIdle, nil)
	if repo.Error != "" || repo.Commit != second || repo.Dirty != 0 {
		t.Fatalf("拉取后 = %+v", repo)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "go.mod")); strings.Contains(string(b), "被改乱了") {
		t.Fatal("工作区应和远程分支一致")
	}
	if !fileExists(filepath.Join(dir, "build-output.bin")) {
		t.Fatal("没被跟踪的构建产物应保留（当缓存用）")
	}

	// 应用正在部署时，别处不动工作区；部署任务自己可以
	third := commitFile(t, remote, "new.txt", "v3", "第三次提交")
	s.SetRepoBusy(func(project, app string) bool { return project == "proj" && app == "app" })
	if repo = s.AppRepo(ctx, "proj", "app", params, RepoSyncIdle, nil); repo.Commit != second {
		t.Fatalf("正在部署时不应更新工作区：%s", repo.Commit)
	}
	if repo = s.AppRepo(ctx, "proj", "app", params, RepoSyncJob, nil); repo.Commit != third {
		t.Fatalf("部署任务应拉到最新：%s", repo.Commit)
	}
	s.SetRepoBusy(nil)

	// 换分支
	gitIn(t, remote, "checkout", "-q", "-b", "release")
	fourth := commitFile(t, remote, "new.txt", "v4", "发布分支的提交")
	params["branch"] = "release"
	if repo = s.AppRepo(ctx, "proj", "app", params, RepoSyncIdle, nil); repo.Branch != "release" || repo.Commit != fourth {
		t.Fatalf("换分支后 = %+v", repo)
	}
	params["branch"] = "nope"
	if repo = s.AppRepo(ctx, "proj", "app", params, RepoSyncIdle, nil); !strings.Contains(repo.Error, "没有这个分支") {
		t.Fatalf("分支不存在应说明：%q", repo.Error)
	}

	// 每个应用各克隆一份
	params["branch"] = "main"
	other := s.AppRepo(ctx, "proj", "other", params, RepoSyncIdle, nil)
	if other.Error != "" || other.Path == dir {
		t.Fatalf("另一个应用应有自己的克隆：%+v", other)
	}
}

func TestManagedRepoErrors(t *testing.T) {
	s, _, _ := newService(t)
	s.SetDataDir(t.TempDir())
	ctx := context.Background()
	token := "ghp_supersecrettoken123"
	if err := s.CreateSecret(ctx, SecretInput{Name: "GIT_TOKEN", Value: &token}); err != nil {
		t.Fatal(err)
	}
	s.SetGitProtocols("file")
	// 地址里的令牌不能出现在错误信息里，地址换回填写的样子
	params := map[string]any{"repo_source": "git", "repo_url": "file:///no/such/${GIT_TOKEN}/repo.git", "branch": "main"}
	repo := s.AppRepo(ctx, "proj", "app", params, RepoSyncIdle, nil)
	if repo.Error == "" || strings.Contains(repo.Error, token) || !strings.Contains(repo.Error, "${GIT_TOKEN}") {
		t.Fatalf("错误信息应遮住令牌：%q", repo.Error)
	}
	// 第一次就失败不留下空仓库
	if fileExists(s.RepoDir("proj", "app", params)) {
		t.Fatal("克隆失败后不应留下目录")
	}
	// 引用的密钥不存在
	params["repo_url"] = "https://x:${MISSING}@git.example.test/a.git"
	if repo := s.AppRepo(ctx, "proj", "app", params, RepoSyncIdle, nil); !strings.Contains(repo.Error, "MISSING") {
		t.Fatalf("应提示密钥不存在：%q", repo.Error)
	}
	// 只允许 https、ssh：默认设置下本地路径和其他协议拉不了
	s.SetGitProtocols("https:http:ssh")
	params["repo_url"] = "file://" + gitRepo(t)
	if repo := s.AppRepo(ctx, "proj", "app", params, RepoSyncIdle, nil); repo.Error == "" {
		t.Fatal("默认不应允许 file 协议")
	}
	// 选的私钥凭据不存在
	params["repo_credential"] = "nope"
	if repo := s.AppRepo(ctx, "proj", "app", params, RepoSyncIdle, nil); !strings.Contains(repo.Error, "nope") {
		t.Fatalf("应提示凭据不存在：%q", repo.Error)
	}
}

// 应用参数里的 git 地址来源：校验、显示条件、凭据引用。
func TestGitSourceAppParams(t *testing.T) {
	s, _, _ := newService(t)
	s.SetDataDir(t.TempDir())
	ctx := context.Background()
	setup(t, s)
	base := func(extra map[string]any) AppInput {
		p := map[string]any{"name": "shop-api", "title": "支付 API", "repo_source": "git", "branch": "main", "server": "shop-main", "main": "./cmd/shop-api"}
		for k, v := range extra {
			p[k] = v
		}
		return AppInput{Driver: "gobinary", Params: p}
	}
	if msgs := fieldMessages(s.ValidateApp(ctx, "shop", "", base(nil))); msgs["repo_url"] == "" || msgs["repo_path"] != "" {
		t.Fatalf("git 来源应要求 git 地址、不要求仓库目录：%v", msgs)
	}
	for _, bad := range []string{"/local/path", "file:///etc", "ext::sh -c id", "-oProxyCommand=id", "git.example.com/a.git"} {
		if msgs := fieldMessages(s.ValidateApp(ctx, "shop", "", base(map[string]any{"repo_url": bad}))); msgs["repo_url"] == "" {
			t.Errorf("git 地址 %q 应拒绝", bad)
		}
	}
	if msgs := fieldMessages(s.ValidateApp(ctx, "shop", "", base(map[string]any{"repo_url": "git@github.com:example/pay.git", "branch": "--upload-pack=id"}))); msgs["branch"] == "" {
		t.Error("以 - 开头的分支名应拒绝")
	}
	if msgs := fieldMessages(s.ValidateApp(ctx, "shop", "", base(map[string]any{"repo_url": "git@github.com:example/pay.git", "repo_credential": "nope"}))); msgs["repo_credential"] == "" {
		t.Error("不存在的私钥凭据应报错")
	}
	if _, err := s.CreateCredential(ctx, keyInput("git-key", newKey(t, ""))); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"git@github.com:example/pay.git", "ssh://git@git.example.com:2222/pay.git", "https://x-access-token:${GIT_TOKEN:-none}@github.com/example/pay.git"} {
		if err := s.ValidateApp(ctx, "shop", "", base(map[string]any{"repo_url": url, "repo_credential": "git-key"})); err != nil {
			t.Errorf("git 地址 %q 应接受：%v", url, err)
		}
	}
	a, err := s.CreateApp(ctx, "shop", base(map[string]any{"repo_url": "git@github.com:example/pay.git", "repo_credential": "git-key", "sync": "offline", "repo_path": "/ignored"}))
	if err != nil {
		t.Fatal(err)
	}
	// 本机目录才有的参数不保存
	for _, k := range []string{"repo_path", "sync", "dirty"} {
		if _, ok := a.Params[k]; ok {
			t.Errorf("git 来源不应保存 %s", k)
		}
	}
	// 凭据被应用选择时不能删
	if err := s.DeleteCredential(ctx, "git-key"); kind(err) != KindConflict {
		t.Fatalf("被应用选择的私钥不应能删除：%v", err)
	}
	// 删除应用时把平台克隆的目录一起删掉
	dir := s.RepoDir("shop", "shop-api", a.Params)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteApp(ctx, "shop", "shop-api"); err != nil {
		t.Fatal(err)
	}
	if fileExists(dir) {
		t.Fatal("删除应用后平台克隆的目录应删除")
	}
}
