package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deployhub/internal/model"
)

const tokenCols = `t.id, t.name, t.level, t.hint, t.created_at, t.last_used_at, t.token_enc <> ''`

func scanToken(row interface{ Scan(...any) error }, extra ...any) (*model.AccessToken, error) {
	var t model.AccessToken
	var created int64
	var used sql.NullInt64
	dest := append([]any{&t.ID, &t.Name, &t.Level, &t.Hint, &created, &used, &t.Revealable}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	t.CreatedAt, t.LastUsedAt = time.Unix(created, 0), timePtr(used)
	return &t, nil
}

// ListTokens 返回账号的访问令牌，按名称排序。
func (s *Store) ListTokens(ctx context.Context) ([]*model.AccessToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tokenCols+` FROM access_tokens t WHERE t.owner_id = ? ORDER BY t.name`, s.owner)
	if err != nil {
		return nil, fmt.Errorf("读取访问令牌：%w", err)
	}
	defer rows.Close()
	list := []*model.AccessToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// CreateToken 新增访问令牌，hash 是令牌的 SHA-256（认证时用），enc 是加密后的令牌（页面上再次查看时用）。
func (s *Store) CreateToken(ctx context.Context, t *model.AccessToken, hash, enc string) error {
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO access_tokens (owner_id, name, level, token_hash, token_enc, hint, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.owner, t.Name, t.Level, hash, enc, t.Hint, now.Unix())
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("保存访问令牌：%w", err)
	}
	t.ID, _ = res.LastInsertId()
	t.CreatedAt, t.Revealable = now, enc != ""
	return nil
}

// TokenValue 返回加密后的令牌；这一列加上之前创建的令牌返回空字符串。
func (s *Store) TokenValue(ctx context.Context, name string) (string, error) {
	var enc string
	err := s.db.QueryRowContext(ctx, `SELECT token_enc FROM access_tokens WHERE owner_id = ? AND name = ?`, s.owner, name).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return enc, err
}

// DeleteToken 吊销（删除）访问令牌，之后它立即不能再用。
func (s *Store) DeleteToken(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM access_tokens WHERE owner_id = ? AND name = ?`, s.owner, name)
	if err != nil {
		return fmt.Errorf("吊销访问令牌：%w", err)
	}
	return mustAffect(res)
}

// TokenByHash 按令牌的哈希找到令牌和它所属的账号，并记录使用时间（一分钟内最多更新一次）。
// 令牌不存在或账号已停用时返回 ErrNotFound。认证时用，不看 Store 的归属。
func (s *Store) TokenByHash(ctx context.Context, hash string, now time.Time) (*model.AccessToken, *model.User, error) {
	var u UserRow
	var created int64
	var lastLogin sql.NullInt64
	t, err := scanToken(s.db.QueryRowContext(ctx, `SELECT `+tokenCols+`, `+userCols+`
		FROM access_tokens t JOIN users u ON u.id = t.owner_id WHERE t.token_hash = ? AND u.disabled = 0`, hash),
		&u.ID, &u.Username, &u.PasswordHash, &u.Admin, &u.Disabled, &created, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("读取访问令牌：%w", err)
	}
	u.CreatedAt, u.LastLoginAt = time.Unix(created, 0), timePtr(lastLogin)
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= time.Minute {
		_, _ = s.db.ExecContext(ctx, `UPDATE access_tokens SET last_used_at = ? WHERE id = ?`, now.Unix(), t.ID)
	}
	return t, &u.User, nil
}
