package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deployhub/internal/cloudflare/cftest"
)

const (
	testAccount = "0123456789abcdef0123456789abcdef"
	testToken   = "token-abcdefgh-123"
)

func cfInput() CredentialInput {
	return CredentialInput{Name: "cf-main", Kind: "cloudflare", Title: "主账号",
		Values: map[string]string{"account_id": testAccount, "api_token": testToken}}
}

func TestCredentials(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()

	err := func() error {
		_, err := s.CreateCredential(ctx, CredentialInput{Name: "Bad", Kind: "cloudflare", Values: map[string]string{"account_id": "xyz"}})
		return err
	}()
	if m := fieldMessages(err); m["name"] == "" || m["account_id"] == "" || m["api_token"] == "" {
		t.Fatalf("应校验标识、账号 ID 和 Token：%v", m)
	}
	if _, err := s.CreateCredential(ctx, CredentialInput{Name: "x1", Kind: "nope"}); fieldMessages(err)["kind"] == "" {
		t.Fatal("不认识的种类应报错")
	}

	c, err := s.CreateCredential(ctx, cfInput())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), testToken) || len(c.Secrets) != 1 || c.Values["account_id"] != testAccount {
		t.Fatalf("返回的凭据不应含 Token：%s", b)
	}
	if _, err := s.CreateCredential(ctx, cfInput()); kind(err) != KindConflict {
		t.Fatal("重名应冲突")
	}

	// 修改时 Token 留空表示不改
	if _, err := s.UpdateCredential(ctx, "cf-main", CredentialInput{Title: "改名", Values: map[string]string{"account_id": testAccount}}); err != nil {
		t.Fatal(err)
	}
	if _, v, _ := s.CredentialValues(ctx, "cf-main"); v["api_token"] != testToken {
		t.Fatalf("Token 留空应保留原来的值：%v", v)
	}
	if _, err := s.UpdateCredential(ctx, "cf-main", CredentialInput{Kind: "other"}); fieldMessages(err)["kind"] == "" {
		t.Fatal("种类不能修改")
	}

	// 验证（模拟的 Cloudflare 接口）
	cf := cftest.New(testAccount, testToken)
	ts := httptest.NewServer(cf)
	defer ts.Close()
	t.Setenv("CLOUDFLARE_API_BASE_URL", ts.URL)
	c, err = s.TestCredential(ctx, "cf-main")
	if err != nil || len(c.Info) < 2 || c.Info[0].Value != "测试账号" || c.CheckedAt == nil {
		t.Fatalf("验证结果：%+v %v", c, err)
	}
	bad := cfInput()
	bad.Values["api_token"] = "wrong"
	if _, err := s.TestCredentialInput(ctx, "", bad); kind(err) != KindFailed || !strings.Contains(err.Error(), "API Token") {
		t.Fatalf("Token 不对时应说明：%v", err)
	}
	// 修改表单里测试：Token 没填时用已保存的
	if info, err := s.TestCredentialInput(ctx, "cf-main", CredentialInput{Values: map[string]string{"account_id": testAccount}}); err != nil || len(info) == 0 {
		t.Fatalf("修改时测试应使用已保存的 Token：%v %v", info, err)
	}

	// 应用选择凭据：凭据要存在、种类对；被选择后不能删除
	repo := setup(t, s)
	if err := os.WriteFile(filepath.Join(repo, "wrangler.jsonc"), []byte(`{"name": "demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"name": "web", "title": "前端", "repo_path": repo, "branch": "main", "account": "missing"}
	if _, err := s.CreateApp(ctx, "shop", AppInput{Driver: "workers", Params: params}); !strings.Contains(fieldMessages(err)["account"], "不存在") {
		t.Fatalf("凭据不存在应报错：%v", err)
	}
	params["account"] = "cf-main"
	a, err := s.CreateApp(ctx, "shop", AppInput{Driver: "workers", Params: params})
	if err != nil || a.Server != "" {
		t.Fatalf("Workers 应用不需要服务器：%+v %v", a, err)
	}
	if err := s.DeleteCredential(ctx, "cf-main"); kind(err) != KindConflict {
		t.Fatal("还有应用在用时不能删除")
	}
	list, _ := s.ListCredentials(ctx)
	if len(list) != 1 || len(list[0].UsedBy) != 1 || list[0].UsedBy[0] != "shop/web" {
		t.Fatalf("列表应带上使用它的应用：%+v", list)
	}

	// 导出：不含敏感内容时没有 Token；导入后凭据先建出来，提醒补 Token，应用也能导入
	plain, _ := s.ExportAll(ctx, false)
	b, _ = json.Marshal(plain)
	if strings.Contains(string(b), testToken) || !strings.Contains(string(b), testAccount) {
		t.Fatalf("不含敏感内容的导出：%s", b)
	}
	s2, _, _ := newService(t)
	var in Export
	_ = json.Unmarshal(b, &in)
	res, err := s2.ImportAll(ctx, &in)
	if err != nil || len(res.Failed) != 0 || !strings.Contains(strings.Join(res.Warnings, ";"), "cf-main") {
		t.Fatalf("导入结果：%+v %v", res, err)
	}
	if _, err := s2.GetApp(ctx, "shop", "web"); err != nil {
		t.Fatalf("凭据先建出来，应用应能导入：%v", err)
	}
	full, _ := s.ExportAll(ctx, true)
	b, _ = json.Marshal(full)
	if !strings.Contains(string(b), testToken) {
		t.Fatal("包含敏感内容的导出应有 Token")
	}

	if err := s.DeleteApp(ctx, "shop", "web"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCredential(ctx, "cf-main"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkersCheckAndPreview(t *testing.T) {
	s, ssh, _ := newService(t)
	ctx := context.Background()
	cf := cftest.New(testAccount, testToken)
	ts := httptest.NewServer(cf)
	defer ts.Close()
	t.Setenv("CLOUDFLARE_API_BASE_URL", ts.URL)
	if _, err := s.CreateCredential(ctx, cfInput()); err != nil {
		t.Fatal(err)
	}
	repo := gitRepo(t)
	_ = os.WriteFile(filepath.Join(repo, "wrangler.jsonc"), []byte(`{"name": "demo-worker"}`), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"devDependencies": {"wrangler": "4.133.0"}}`), 0o644)
	if _, err := s.CreateProject(ctx, ProjectInput{Name: "proj", Title: "项目"}); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"name": "web", "title": "前端", "repo_path": repo, "branch": "main", "account": "cf-main"}
	if _, err := s.CreateApp(ctx, "proj", AppInput{Driver: "workers", Params: params}); err != nil {
		t.Fatal(err)
	}
	res, err := s.CheckApp(ctx, "proj", "web")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range res.Items {
		got[it.Group+"/"+it.Name] = it.Status + " " + it.Message
	}
	if !strings.HasPrefix(got["Cloudflare/账号"], "ok") || !strings.Contains(got["Cloudflare/Worker"], "还没有部署过") ||
		!strings.HasPrefix(got["本机/wrangler"], "ok") || !strings.HasPrefix(got["本机/wrangler 配置文件"], "ok") {
		t.Fatalf("检查结果：%v", got)
	}
	if _, ok := got["服务器/连接"]; ok || len(ssh.scripts) != 0 {
		t.Fatalf("Workers 不应连服务器：%v %v", got, ssh.scripts)
	}

	p, err := s.PreviewApp(ctx, "proj", "web", AppInput{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), "wrangler deploy --config wrangler.jsonc --tag") || strings.Contains(string(b), testToken) || !strings.Contains(string(b), "CLOUDFLARE_ACCOUNT_ID="+testAccount) {
		t.Fatalf("预览：%s", b)
	}
}
