// Package store 负责本地 SQLite 数据库：打开、表结构迁移和读写。
// 所有数据访问都集中在这里，SQL 尽量写成 SQLite 和 PostgreSQL 都能用的写法。
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动，不需要 CGO
)

// Store 是数据库的封装，可以被多个 goroutine 同时使用。
//
// 服务器、密钥、项目、应用、凭据、任务都属于某个账号。For 返回只读写那个账号数据的 Store，
// 业务层和部署引擎拿到的都是它，所以查询里统一带 owner_id 条件。
// Open 返回的 Store 归属是 0（不对应任何账号），只用来读写账号、会话、设置，以及在测试里当作单个账号用。
type Store struct {
	db    *sql.DB
	owner int64
}

// For 返回只读写账号 owner 数据的 Store，和原来的共用同一个数据库连接。
func (s *Store) For(owner int64) *Store {
	return &Store{db: s.db, owner: owner}
}

// Open 打开（不存在时创建）数据库文件，并把表结构升级到最新。
func Open(ctx context.Context, path string) (*Store, error) {
	// WAL：部署时写日志事件和页面读取可以同时进行；busy_timeout：写冲突时等待而不是立即报错。
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库 %s：%w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("打开数据库 %s：%w", path, err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

// Setting 读取一项设置，不存在时 ok 为 false。
func (s *Store) Setting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("读取设置 %s：%w", key, err)
	}
	return value, true, nil
}

// SetSetting 写入一项设置，已存在时覆盖。
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("写入设置 %s：%w", key, err)
	}
	return nil
}

// EnsureSecret 返回名为 key 的随机密钥；不存在时生成 size 字节的随机值并保存。
func (s *Store) EnsureSecret(ctx context.Context, key string, size int) ([]byte, error) {
	if v, ok, err := s.Setting(ctx, key); err != nil {
		return nil, err
	} else if ok {
		b, err := hex.DecodeString(v)
		if err != nil || len(b) != size {
			return nil, fmt.Errorf("设置 %s 的值无效，删除后会重新生成", key)
		}
		return b, nil
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("生成随机密钥：%w", err)
	}
	// 并发启动时以先写入的为准
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO NOTHING`,
		key, hex.EncodeToString(b), time.Now().Unix()); err != nil {
		return nil, fmt.Errorf("保存设置 %s：%w", key, err)
	}
	return s.EnsureSecret(ctx, key, size)
}

// CreateLoginToken 保存一个登录令牌的哈希，同时清理已经过期的令牌。
func (s *Store) CreateLoginToken(ctx context.Context, hash string, now, expires time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM login_tokens WHERE expires_at <= ?`, now.Unix()); err != nil {
		return fmt.Errorf("清理过期登录令牌：%w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO login_tokens (token_hash, created_at, expires_at) VALUES (?, ?, ?)`,
		hash, now.Unix(), expires.Unix()); err != nil {
		return fmt.Errorf("保存登录令牌：%w", err)
	}
	return nil
}

// ConsumeLoginToken 使用一个登录令牌：未过期且没用过才返回 true，用过之后立即失效。
func (s *Store) ConsumeLoginToken(ctx context.Context, hash string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE login_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now.Unix(), hash, now.Unix())
	if err != nil {
		return false, fmt.Errorf("使用登录令牌：%w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("使用登录令牌：%w", err)
	}
	return n == 1, nil
}
