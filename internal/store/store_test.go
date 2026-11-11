package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"deployhub/internal/model"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deployhub.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, path := openTemp(t)
	if err := s.SetSetting(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 再次打开不应重复执行迁移，数据保留
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("第二次 Open: %v", err)
	}
	defer s2.Close()
	v, ok, err := s2.Setting(ctx, "k")
	if err != nil || !ok || v != "v" {
		t.Fatalf("Setting = %q %v %v，期望 v true nil", v, ok, err)
	}
	var n int
	if err := s2.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	list, _ := loadMigrations()
	if n != len(list) {
		t.Fatalf("迁移记录 %d 条，期望 %d 条", n, len(list))
	}
}

func TestNewerDatabaseIsRejected(t *testing.T) {
	ctx := context.Background()
	s, path := openTemp(t)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, '9999_future.sql', 0)`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(ctx, path); err == nil {
		t.Fatal("数据库版本比程序新时应该报错")
	}
}

func TestWALEnabled(t *testing.T) {
	s, _ := openTemp(t)
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %s，期望 wal", mode)
	}
}

func TestEnsureSecretIsStable(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	a, err := s.EnsureSecret(ctx, "session_secret", 32)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.EnsureSecret(ctx, "session_secret", 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 || !bytes.Equal(a, b) {
		t.Fatalf("两次读取的密钥不一致或长度不对：%x %x", a, b)
	}
}

func TestLoginTokenSingleUse(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	now := time.Unix(1_800_000_000, 0)
	if err := s.CreateLoginToken(ctx, "h1", now, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ConsumeLoginToken(ctx, "h1", now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("第一次使用 = %v %v，期望 true nil", ok, err)
	}
	if ok, _ := s.ConsumeLoginToken(ctx, "h1", now.Add(2*time.Minute)); ok {
		t.Fatal("令牌只能用一次")
	}
	if ok, _ := s.ConsumeLoginToken(ctx, "missing", now); ok {
		t.Fatal("不存在的令牌不能通过")
	}
}

func TestLoginTokenExpires(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	now := time.Unix(1_800_000_000, 0)
	if err := s.CreateLoginToken(ctx, "h1", now, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.ConsumeLoginToken(ctx, "h1", now.Add(10*time.Minute)); ok {
		t.Fatal("过期的令牌不能通过")
	}
	// 创建新令牌时清理过期的
	if err := s.CreateLoginToken(ctx, "h2", now.Add(time.Hour), now.Add(time.Hour+10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_tokens WHERE token_hash = 'h1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("过期的令牌应该被清理")
	}
}

// 库里保存的是加密后的令牌；加这一列之前创建的令牌（只有哈希）取出来是空的。
func TestTokenValue(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	a, b := s.For(1), s.For(2)
	tok := &model.AccessToken{Name: "ci", Level: model.LevelRead, Hint: "dh_abcd…"}
	if err := a.CreateToken(ctx, tok, "hash-new", "v1:enc"); err != nil || !tok.Revealable {
		t.Fatalf("CreateToken = %v，Revealable = %v", err, tok.Revealable)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO access_tokens (owner_id, name, level, token_hash, hint, created_at) VALUES (1, 'old', 'read', 'hash-old', 'dh_wxyz…', 1)`); err != nil {
		t.Fatal(err)
	}
	if v, err := a.TokenValue(ctx, "ci"); err != nil || v != "v1:enc" {
		t.Fatalf("TokenValue = %q, %v", v, err)
	}
	if v, err := a.TokenValue(ctx, "old"); err != nil || v != "" {
		t.Fatalf("旧令牌的 TokenValue = %q, %v", v, err)
	}
	if _, err := b.TokenValue(ctx, "ci"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("别的账号读令牌应 ErrNotFound，实际 %v", err)
	}
	list, err := a.ListTokens(ctx)
	if err != nil || len(list) != 2 || !list[0].Revealable || list[1].Revealable {
		t.Fatalf("ListTokens = %+v, %v", list, err)
	}
}
