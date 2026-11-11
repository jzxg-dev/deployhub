package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"deployhub/internal/model"
)

func TestServerCRUD(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	srv := &model.Server{Name: "shop-main", Host: "1.2.3.4", Port: 22, User: "deploy", KeyPath: "~/.ssh/id_ed25519"}
	if err := s.CreateServer(ctx, srv); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateServer(ctx, &model.Server{Name: "shop-main", Host: "x", Port: 22}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("重名应返回 ErrDuplicate，实际 %v", err)
	}
	if err := s.SetServerFacts(ctx, "shop-main", &model.ServerFacts{Arch: "amd64", Docker: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetServer(ctx, "shop-main")
	if err != nil || got.Facts == nil || got.Facts.Arch != "amd64" || got.CheckedAt == nil {
		t.Fatalf("GetServer = %+v %v", got, err)
	}
	if got.SSHOptions == nil {
		t.Fatal("SSH 选项应为空数组而不是 nil")
	}

	// 只改说明，检测结果保留
	got.Title = "主服务器"
	if err := s.UpdateServer(ctx, got); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GetServer(ctx, "shop-main"); g.Facts == nil || g.Title != "主服务器" {
		t.Fatal("只改说明时检测结果应保留")
	}
	// 改地址，检测结果作废
	got.Host = "5.6.7.8"
	if err := s.UpdateServer(ctx, got); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GetServer(ctx, "shop-main"); g.Facts != nil || g.CheckedAt != nil {
		t.Fatal("连接信息变了，检测结果应作废")
	}

	list, err := s.ListServers(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListServers = %d %v", len(list), err)
	}
	if err := s.DeleteServer(ctx, "shop-main"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetServer(ctx, "shop-main"); !errors.Is(err, ErrNotFound) {
		t.Fatal("删除后应查不到")
	}
	if err := s.DeleteServer(ctx, "shop-main"); !errors.Is(err, ErrNotFound) {
		t.Fatal("删除不存在的服务器应返回 ErrNotFound")
	}
}

func TestSecretCRUD(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	if err := s.CreateSecret(ctx, "API_URL", "v1:aaa", "接口地址"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSecret(ctx, "API_URL", "v1:bbb", ""); !errors.Is(err, ErrDuplicate) {
		t.Fatal("重名应返回 ErrDuplicate")
	}
	if err := s.UpdateSecret(ctx, "API_URL", nil, "新说明"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.SecretValue(ctx, "API_URL"); v != "v1:aaa" {
		t.Fatal("不传新值时值不变")
	}
	nv := "v1:ccc"
	if err := s.UpdateSecret(ctx, "API_URL", &nv, "新说明"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.SecretValue(ctx, "API_URL"); v != nv {
		t.Fatal("值应更新")
	}
	names, _ := s.SecretNames(ctx)
	if !names["API_URL"] {
		t.Fatal("SecretNames 应包含 API_URL")
	}
	list, _ := s.ListSecrets(ctx)
	if len(list) != 1 || list[0].Description != "新说明" {
		t.Fatalf("ListSecrets = %+v", list)
	}
	if err := s.DeleteSecret(ctx, "API_URL"); err != nil {
		t.Fatal(err)
	}
}

func TestAppRevisions(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	p := &model.Project{Name: "shop", Title: "示例商城"}
	if err := s.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	srv := &model.Server{Name: "shop-main", Host: "1.2.3.4", Port: 22}
	_ = s.CreateServer(ctx, srv)

	a := &model.App{ProjectID: p.ID, Name: "open-api", Title: "开放 API", Driver: "gobinary", ServerID: &srv.ID,
		Params: map[string]any{"name": "open-api", "main": "./cmd/open-api"}}
	if err := s.CreateApp(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateApp(ctx, &model.App{ProjectID: p.ID, Name: "open-api", Title: "x", Driver: "gobinary", Params: map[string]any{}}); !errors.Is(err, ErrDuplicate) {
		t.Fatal("同项目下重名应返回 ErrDuplicate")
	}

	got, err := s.GetApp(ctx, "shop", "open-api")
	if err != nil || got.Revision != 1 || got.Server != "shop-main" || got.Params["main"] != "./cmd/open-api" {
		t.Fatalf("GetApp = %+v %v", got, err)
	}

	got.Params["main"] = "./cmd/open-api2"
	if err := s.UpdateApp(ctx, got, 1); err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 {
		t.Fatalf("保存后版本 %d，期望 2", got.Revision)
	}
	// 基于旧版本保存 → 冲突
	if err := s.UpdateApp(ctx, got, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("基于旧版本保存应返回 ErrConflict，实际 %v", err)
	}
	revs, _ := s.ListRevisions(ctx, got.ID)
	if len(revs) != 2 || revs[0].Revision != 2 || revs[1].Params["main"] != "./cmd/open-api" {
		t.Fatalf("修改记录不对：%+v", revs)
	}

	if p2, _ := s.GetProject(ctx, "shop"); p2.AppCount != 1 {
		t.Fatal("项目的应用数应为 1")
	}
	if list, _ := s.ListServers(ctx); list[0].AppCount != 1 {
		t.Fatal("服务器的应用数应为 1")
	}
	// 有应用时不能删项目、服务器
	if err := s.DeleteProject(ctx, "shop"); err == nil {
		t.Fatal("还有应用时删除项目应失败")
	}
	if err := s.DeleteServer(ctx, "shop-main"); err == nil {
		t.Fatal("还有应用引用时删除服务器应失败")
	}
	if err := s.DeleteApp(ctx, got.ID); err != nil {
		t.Fatal(err)
	}
	if revs, _ := s.ListRevisions(ctx, got.ID); len(revs) != 0 {
		t.Fatal("删除应用后修改记录应一起删除")
	}
	if err := s.DeleteProject(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
}
