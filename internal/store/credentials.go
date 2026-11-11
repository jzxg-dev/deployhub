package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"deployhub/internal/model"
)

// CredentialRow 是凭据的一行：敏感字段是加密后的 JSON。
type CredentialRow struct {
	model.Credential
	SecretsEnc string
}

const credentialCols = `id, name, kind, title, fields, secrets_enc, info, checked_at, created_at, updated_at`

func scanCredential(row interface{ Scan(...any) error }) (*CredentialRow, error) {
	var c CredentialRow
	var fields string
	var info sql.NullString
	var checked sql.NullInt64
	var created, updated int64
	if err := row.Scan(&c.ID, &c.Name, &c.Kind, &c.Title, &fields, &c.SecretsEnc, &info, &checked, &created, &updated); err != nil {
		return nil, err
	}
	c.Values = map[string]string{}
	if err := json.Unmarshal([]byte(fields), &c.Values); err != nil {
		return nil, fmt.Errorf("凭据 %s 的字段格式不对：%w", c.Name, err)
	}
	c.Info = []model.InfoItem{}
	if info.Valid {
		_ = json.Unmarshal([]byte(info.String), &c.Info)
	}
	c.CheckedAt = timePtr(checked)
	c.CreatedAt, c.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	c.Secrets, c.UsedBy = []string{}, []string{}
	return &c, nil
}

// ListCredentials 返回所有凭据，按名称排序。
func (s *Store) ListCredentials(ctx context.Context) ([]*CredentialRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+credentialCols+` FROM credentials WHERE owner_id = ? ORDER BY name`, s.owner)
	if err != nil {
		return nil, fmt.Errorf("读取凭据列表：%w", err)
	}
	defer rows.Close()
	list := []*CredentialRow{}
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// GetCredential 按名称读取凭据。
func (s *Store) GetCredential(ctx context.Context, name string) (*CredentialRow, error) {
	c, err := scanCredential(s.db.QueryRowContext(ctx, `SELECT `+credentialCols+` FROM credentials WHERE owner_id = ? AND name = ?`, s.owner, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// CreateCredential 新增凭据。
func (s *Store) CreateCredential(ctx context.Context, c *CredentialRow) error {
	fields, err := json.Marshal(c.Values)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `INSERT INTO credentials (owner_id, name, kind, title, fields, secrets_enc, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.owner, c.Name, c.Kind, c.Title, string(fields), c.SecretsEnc, now, now)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("保存凭据：%w", err)
	}
	return nil
}

// UpdateCredential 修改凭据的名称以外的内容，清掉上次的验证结果。
func (s *Store) UpdateCredential(ctx context.Context, c *CredentialRow) error {
	fields, err := json.Marshal(c.Values)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE credentials SET title = ?, fields = ?, secrets_enc = ?, info = NULL, checked_at = NULL, updated_at = ? WHERE owner_id = ? AND name = ?`,
		c.Title, string(fields), c.SecretsEnc, time.Now().Unix(), s.owner, c.Name)
	if err != nil {
		return fmt.Errorf("保存凭据：%w", err)
	}
	return mustAffect(res)
}

// SetCredentialInfo 保存验证结果。
func (s *Store) SetCredentialInfo(ctx context.Context, name string, info []model.InfoItem, at time.Time) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE credentials SET info = ?, checked_at = ? WHERE owner_id = ? AND name = ?`, string(b), at.Unix(), s.owner, name)
	return err
}

// DeleteCredential 删除凭据。
func (s *Store) DeleteCredential(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM credentials WHERE owner_id = ? AND name = ?`, s.owner, name)
	if err != nil {
		return fmt.Errorf("删除凭据：%w", err)
	}
	return mustAffect(res)
}
