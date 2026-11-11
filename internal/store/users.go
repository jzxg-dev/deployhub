package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deployhub/internal/model"
)

// UserRow 是账号的一行，带密码哈希。
type UserRow struct {
	model.User
	PasswordHash string
}

const userCols = `u.id, u.username, u.password_hash, u.is_admin, u.disabled, u.created_at, u.last_login_at`

func scanUser(row interface{ Scan(...any) error }, extra ...any) (*UserRow, error) {
	var u UserRow
	var created int64
	var lastLogin sql.NullInt64
	dest := append([]any{&u.ID, &u.Username, &u.PasswordHash, &u.Admin, &u.Disabled, &created, &lastLogin}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	u.CreatedAt, u.LastLoginAt = time.Unix(created, 0), timePtr(lastLogin)
	return &u, nil
}

// CountUsers 返回账号数；为 0 表示还没有创建管理员。
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("读取账号数：%w", err)
	}
	return n, nil
}

// ListUsers 返回所有账号，按用户名排序，带各自的项目数。
func (s *Store) ListUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+`, (SELECT COUNT(*) FROM projects p WHERE p.owner_id = u.id)
		FROM users u ORDER BY u.username`)
	if err != nil {
		return nil, fmt.Errorf("读取账号列表：%w", err)
	}
	defer rows.Close()
	list := []*model.User{}
	for rows.Next() {
		var n int
		u, err := scanUser(rows, &n)
		if err != nil {
			return nil, err
		}
		u.ProjectCount = n
		list = append(list, &u.User)
	}
	return list, rows.Err()
}

// GetUser 按用户名读取账号。
func (s *Store) GetUser(ctx context.Context, username string) (*UserRow, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users u WHERE u.username = ?`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// CreateUser 新增账号。
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, admin bool) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		username, passwordHash, admin, now, now)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("保存账号：%w", err)
	}
	return nil
}

// CreateFirstUser 在还没有任何账号时新增一个管理员；已经有账号时返回 ErrConflict。
// 判断和写入在一条语句里完成，两个人同时打开创建链接也只会成功一个。
func (s *Store) CreateFirstUser(ctx context.Context, username, passwordHash string) error {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, is_admin, created_at, updated_at)
		SELECT ?, ?, 1, ?, ? WHERE NOT EXISTS (SELECT 1 FROM users)`, username, passwordHash, now, now)
	if err != nil {
		return fmt.Errorf("保存账号：%w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

// SetUserPassword 修改账号的密码哈希。
func (s *Store) SetUserPassword(ctx context.Context, id int64, passwordHash string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("保存密码：%w", err)
	}
	return mustAffect(res)
}

// SetUserFlags 修改账号是不是管理员、是不是停用。
func (s *Store) SetUserFlags(ctx context.Context, id int64, admin, disabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET is_admin = ?, disabled = ?, updated_at = ? WHERE id = ?`, admin, disabled, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("保存账号：%w", err)
	}
	return mustAffect(res)
}

// TouchUserLogin 记录最近一次登录时间。
func (s *Store) TouchUserLogin(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, at.Unix(), id)
	return err
}

// DeleteUser 删除账号和它的全部数据：项目、应用、服务器、密钥、凭据、访问令牌、部署记录、会话。
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 顺序：先删引用别人的（任务、应用），再删被引用的（项目、服务器）
	for _, q := range []string{
		`DELETE FROM jobs WHERE owner_id = ?`,
		`DELETE FROM apps WHERE project_id IN (SELECT id FROM projects WHERE owner_id = ?)`,
		`DELETE FROM projects WHERE owner_id = ?`,
		`DELETE FROM servers WHERE owner_id = ?`,
		`DELETE FROM secrets WHERE owner_id = ?`,
		`DELETE FROM credentials WHERE owner_id = ?`,
		`DELETE FROM access_tokens WHERE owner_id = ?`,
		`DELETE FROM sessions WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("删除账号的数据：%w", err)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除账号：%w", err)
	}
	if err := mustAffect(res); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- 会话 ----------

// CreateSession 保存一个会话令牌的哈希，同时清理已经过期的会话。
func (s *Store) CreateSession(ctx context.Context, hash string, userID int64, now, expires time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return fmt.Errorf("清理过期会话：%w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hash, userID, now.Unix(), expires.Unix()); err != nil {
		return fmt.Errorf("保存会话：%w", err)
	}
	return nil
}

// SessionUser 返回会话对应的账号；会话不存在、已过期或账号已停用时返回 ErrNotFound。
func (s *Store) SessionUser(ctx context.Context, hash string, now time.Time) (*model.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM sessions x JOIN users u ON u.id = x.user_id
		WHERE x.token_hash = ? AND x.expires_at > ? AND u.disabled = 0`, hash, now.Unix()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取会话：%w", err)
	}
	return &u.User, nil
}

// DeleteSession 删除一个会话（退出登录）。
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash)
	return err
}

// DeleteUserSessions 删除账号的所有会话，keepHash 不为空时保留那一个（改自己的密码时保留当前会话）。
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, keepHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`, userID, keepHash)
	return err
}
