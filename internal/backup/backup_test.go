package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deployhub/internal/model"
	"deployhub/internal/store"
	"deployhub/internal/vault"
)

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(data, "deployhub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	vt, _ := vault.New(bytes.Repeat([]byte{9}, 32))
	enc, _ := vt.Encrypt("很重要的值")
	if err := st.CreateSecret(ctx, "API_KEY", enc, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateProject(ctx, &model.Project{Name: "shop", Title: "商城"}); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "elsewhere", "backups")
	o := Options{Store: st, Vault: vt, Dir: dir, Keep: 3}
	if _, ok := Latest(dir); ok {
		t.Fatal("还没备份过")
	}
	now := time.Date(2026, 11, 11, 3, 0, 0, 0, time.Local)
	path, err := Run(ctx, o, now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "deployhub-20261111-030000.db" {
		t.Fatalf("备份文件名 = %s", path)
	}
	for _, f := range []string{path, filepath.Join(dir, vault.KeyFile)} {
		info, err := os.Stat(f)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s 应存在且权限 600：%v %v", f, info, err)
		}
	}
	if last, ok := Latest(dir); !ok || !last.Equal(now) {
		t.Fatalf("Latest = %v %v", last, ok)
	}
	// 同一秒再备份一次：不覆盖已有的文件
	if _, err := Run(ctx, o, now); err == nil {
		t.Fatal("不应覆盖已有的备份")
	}

	// 备份之后的改动不在这份备份里
	if err := st.CreateProject(ctx, &model.Project{Name: "later", Title: "之后建的"}); err != nil {
		t.Fatal(err)
	}

	// 恢复到一个全新的数据目录：只靠备份目录里的两个文件
	restored := t.TempDir()
	copyFile(t, path, filepath.Join(restored, "deployhub.db"))
	copyFile(t, filepath.Join(dir, vault.KeyFile), filepath.Join(restored, vault.KeyFile))
	st2, err := store.Open(ctx, filepath.Join(restored, "deployhub.db"))
	if err != nil {
		t.Fatalf("打开恢复出来的数据库：%v", err)
	}
	defer st2.Close()
	projects, _ := st2.ListProjects(ctx)
	if len(projects) != 1 || projects[0].Name != "shop" {
		t.Fatalf("恢复出来的项目 = %+v", projects)
	}
	vt2, where, err := vault.Open(restored, "unused", nil)
	if err != nil || !strings.HasSuffix(where, vault.KeyFile) {
		t.Fatalf("用备份的主密钥打开：%v %s", err, where)
	}
	got, _ := st2.SecretValue(ctx, "API_KEY")
	if plain, err := vt2.Decrypt(got); err != nil || plain != "很重要的值" {
		t.Fatalf("恢复后应能解密原来的密钥：%q %v", plain, err)
	}
}

func TestPruneKeepsRecentAndUpgradeBackups(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "deployhub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	// 升级前的备份和别的文件不归自动清理管
	keepers := []string{"deployhub-before-upgrade-v4-20261101-000000.db", "notes.txt", "deployhub-手动.db"}
	for _, n := range keepers {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	o := Options{Store: st, Dir: dir, Keep: 2}
	base := time.Date(2026, 11, 1, 3, 0, 0, 0, time.Local)
	for i := 0; i < 4; i++ {
		if _, err := Run(ctx, o, base.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	got := snapshots(dir)
	if len(got) != 2 || got[0] != "deployhub-20261104-030000.db" || got[1] != "deployhub-20261103-030000.db" {
		t.Fatalf("应只留最近 2 份：%v", got)
	}
	for _, n := range keepers {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s 不应被清理", n)
		}
	}
	// 没有主密钥（Vault 为 nil）时不写 master.key
	if _, err := os.Stat(filepath.Join(dir, vault.KeyFile)); err == nil {
		t.Error("没有 Vault 时不应写主密钥")
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
