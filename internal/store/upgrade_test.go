package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func timeAt(sec int64) time.Time { return time.Unix(sec, 0) }

// 从没有账号的旧版本（第 4 号迁移）升级：数据保留、编号不变，全部归编号 1 的账号，外键仍然有效。
func TestUpgradeToAccounts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "deployhub.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	list, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if m.version <= 4 {
			if err := applyMigration(ctx, db, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, q := range []string{
		`INSERT INTO servers (id, name, host, created_at, updated_at) VALUES (3, 'shop-main', '1.2.3.4', 1, 1)`,
		`INSERT INTO secrets (name, value_enc, created_at, updated_at) VALUES ('API_URL', 'enc', 1, 1)`,
		`INSERT INTO projects (id, name, title, created_at, updated_at) VALUES (5, 'shop', '示例商城', 1, 1)`,
		`INSERT INTO apps (id, project_id, name, title, driver, server_id, params, revision, created_at, updated_at) VALUES (9, 5, 'admin', '管理后台', 'image', 3, '{}', 1, 1, 1)`,
		`INSERT INTO app_revisions (app_id, revision, params, created_at) VALUES (9, 1, '{}', 1)`,
		`INSERT INTO credentials (name, kind, created_at, updated_at) VALUES ('cf', 'cloudflare', 1, 1)`,
		`INSERT INTO jobs (id, kind, status, initiator, created_at) VALUES (66, 'deploy', 'failed', 'page', 1)`,
		`INSERT INTO job_units (job_id, seq, app_id, project, app, title, driver, server, revision, status) VALUES (66, 0, 9, 'shop', 'admin', '管理后台', 'image', 'shop-main', 1, 'failed')`,
		`INSERT INTO job_events (job_id, time, type, message) VALUES (66, 1, 'info', '日志')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s：%v", q, err)
		}
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("升级：%v", err)
	}
	defer s.Close()

	// 不带归属的 Store 和别的账号都看不到，编号 1 的账号全都有
	for _, owner := range []int64{0, 2} {
		if list, _ := s.For(owner).ListProjects(ctx); len(list) != 0 {
			t.Fatalf("账号 %d 不应看到旧数据", owner)
		}
	}
	first := s.For(1)
	app, err := first.GetApp(ctx, "shop", "admin")
	if err != nil || app.ID != 9 || app.Server != "shop-main" || app.ProjectID != 5 {
		t.Fatalf("应用 = %+v %v", app, err)
	}
	if revs, err := first.ListRevisions(ctx, 9); err != nil || len(revs) != 1 {
		t.Fatalf("修改记录 = %v %v", revs, err)
	}
	if srv, err := first.GetServer(ctx, "shop-main"); err != nil || srv.ID != 3 {
		t.Fatalf("服务器 = %+v %v", srv, err)
	}
	if _, err := first.SecretValue(ctx, "API_URL"); err != nil {
		t.Fatalf("密钥：%v", err)
	}
	if _, err := first.GetCredential(ctx, "cf"); err != nil {
		t.Fatalf("凭据：%v", err)
	}
	job, err := first.GetJob(ctx, 66)
	if err != nil || len(job.Units) != 1 || job.Units[0].AppID == nil || *job.Units[0].AppID != 9 {
		t.Fatalf("任务 = %+v %v", job, err)
	}
	if evs, err := first.JobEvents(ctx, 66, 0, 10); err != nil || len(evs) != 1 {
		t.Fatalf("任务日志 = %v %v", evs, err)
	}

	// 升级后外键仍然有效：有应用的项目和服务器删不掉
	if err := first.DeleteProject(ctx, "shop"); err == nil {
		t.Fatal("还有应用的项目不应能删除")
	}
	if err := first.DeleteServer(ctx, "shop-main"); err == nil {
		t.Fatal("还有应用的服务器不应能删除")
	}
	// 名称只在账号内唯一
	other := s.For(2)
	if err := other.CreateSecret(ctx, "API_URL", "enc2", ""); err != nil {
		t.Fatalf("别的账号应能用同名密钥：%v", err)
	}
	if err := first.CreateSecret(ctx, "API_URL", "enc3", ""); err != ErrDuplicate {
		t.Fatalf("同一账号重名应报 ErrDuplicate，实际 %v", err)
	}
}

// 删除账号连同它的全部数据，别的账号不受影响。
func TestDeleteUserRemovesData(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	for _, name := range []string{"alice", "bob"} {
		if err := s.CreateUser(ctx, name, "hash", false); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]int64{}
	for _, name := range []string{"alice", "bob"} {
		u, err := s.GetUser(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = u.ID
		st := s.For(u.ID)
		if _, err := st.db.ExecContext(ctx, `INSERT INTO servers (owner_id, name, host, created_at, updated_at) VALUES (?, 'main', 'h', 1, 1)`, u.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.ExecContext(ctx, `INSERT INTO projects (owner_id, name, title, created_at, updated_at) VALUES (?, 'shop', '商城', 1, 1)`, u.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.ExecContext(ctx, `INSERT INTO apps (project_id, name, title, driver, server_id, params, revision, created_at, updated_at)
			SELECT p.id, 'web', '网站', 'static', v.id, '{}', 1, 1, 1 FROM projects p, servers v WHERE p.owner_id = ? AND v.owner_id = ?`, u.ID, u.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateSecret(ctx, "KEY", "enc", ""); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateSession(ctx, "session-"+name, u.ID, timeAt(1), timeAt(1<<40)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteUser(ctx, ids["alice"]); err != nil {
		t.Fatalf("删除账号：%v", err)
	}
	if _, err := s.GetUser(ctx, "alice"); err != ErrNotFound {
		t.Fatalf("账号应已删除：%v", err)
	}
	if _, err := s.SessionUser(ctx, "session-alice", timeAt(2)); err != ErrNotFound {
		t.Fatalf("会话应已删除：%v", err)
	}
	for _, table := range []string{"servers", "projects", "secrets"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE owner_id = ?`, ids["alice"]).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s 里还有 alice 的数据：%d %v", table, n, err)
		}
	}
	bob := s.For(ids["bob"])
	if apps, err := bob.ListApps(ctx, ""); err != nil || len(apps) != 1 {
		t.Fatalf("bob 的应用不应受影响：%v %v", apps, err)
	}
	if _, err := s.SessionUser(ctx, "session-bob", timeAt(2)); err != nil {
		t.Fatalf("bob 的会话不应受影响：%v", err)
	}
	// 新账号不会接手已删除账号的编号
	if err := s.CreateUser(ctx, "carol", "hash", false); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.GetUser(ctx, "carol"); u.ID == ids["alice"] {
		t.Fatal("账号编号不应重复使用")
	}
}
