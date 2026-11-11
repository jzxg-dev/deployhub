package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"deployhub/internal/model"
)

var (
	// ErrNotFound 表示记录不存在。
	ErrNotFound = errors.New("记录不存在")
	// ErrDuplicate 表示名称重复。
	ErrDuplicate = errors.New("名称重复")
	// ErrConflict 表示记录已被修改（保存时基于的版本不是最新）。
	ErrConflict = errors.New("记录已被修改")
)

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func unixPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func timePtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}

// ---------- 服务器 ----------

const serverCols = `s.id, s.name, s.title, s.host, s.port, s.username, s.key_path, s.key_credential, s.ssh_options, s.facts, s.checked_at, s.created_at, s.updated_at`

func scanServer(row interface{ Scan(...any) error }, extra ...any) (*model.Server, error) {
	var s model.Server
	var opts string
	var facts sql.NullString
	var checked sql.NullInt64
	var created, updated int64
	dest := append([]any{&s.ID, &s.Name, &s.Title, &s.Host, &s.Port, &s.User, &s.KeyPath, &s.KeyCredential, &opts, &facts, &checked, &created, &updated}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(opts), &s.SSHOptions); err != nil {
		return nil, fmt.Errorf("服务器 %s 的 SSH 选项格式不对：%w", s.Name, err)
	}
	if facts.Valid {
		s.Facts = &model.ServerFacts{}
		if err := json.Unmarshal([]byte(facts.String), s.Facts); err != nil {
			return nil, fmt.Errorf("服务器 %s 的检测结果格式不对：%w", s.Name, err)
		}
	}
	s.CheckedAt = timePtr(checked)
	s.CreatedAt, s.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return &s, nil
}

// ListServers 返回所有服务器，按名称排序，带部署在上面的应用数。
func (s *Store) ListServers(ctx context.Context) ([]*model.Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverCols+`, (SELECT COUNT(*) FROM apps a WHERE a.server_id = s.id)
		FROM servers s WHERE s.owner_id = ? ORDER BY s.name`, s.owner)
	if err != nil {
		return nil, fmt.Errorf("读取服务器列表：%w", err)
	}
	defer rows.Close()
	list := []*model.Server{}
	for rows.Next() {
		var n int
		srv, err := scanServer(rows, &n)
		if err != nil {
			return nil, err
		}
		srv.AppCount = n
		list = append(list, srv)
	}
	return list, rows.Err()
}

// GetServer 按名称读取服务器。
func (s *Store) GetServer(ctx context.Context, name string) (*model.Server, error) {
	srv, err := scanServer(s.db.QueryRowContext(ctx, `SELECT `+serverCols+` FROM servers s WHERE s.owner_id = ? AND s.name = ?`, s.owner, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return srv, err
}

// CreateServer 新增服务器。
func (s *Store) CreateServer(ctx context.Context, srv *model.Server) error {
	opts, _ := json.Marshal(nonNil(srv.SSHOptions))
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO servers (owner_id, name, title, host, port, username, key_path, key_credential, ssh_options, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.owner, srv.Name, srv.Title, srv.Host, srv.Port, srv.User, srv.KeyPath, srv.KeyCredential, string(opts), now.Unix(), now.Unix())
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("保存服务器：%w", err)
	}
	srv.ID, _ = res.LastInsertId()
	srv.CreatedAt, srv.UpdatedAt = now, now
	return nil
}

// UpdateServer 修改服务器的连接信息（名称不能改）。连接信息变了，之前的检测结果作废。
func (s *Store) UpdateServer(ctx context.Context, srv *model.Server) error {
	opts, _ := json.Marshal(nonNil(srv.SSHOptions))
	res, err := s.db.ExecContext(ctx, `UPDATE servers SET title = ?, host = ?, port = ?, username = ?, key_path = ?, key_credential = ?, ssh_options = ?,
		facts = CASE WHEN host = ? AND port = ? AND username = ? AND key_path = ? AND key_credential = ? AND ssh_options = ? THEN facts ELSE NULL END,
		checked_at = CASE WHEN host = ? AND port = ? AND username = ? AND key_path = ? AND key_credential = ? AND ssh_options = ? THEN checked_at ELSE NULL END,
		updated_at = ? WHERE owner_id = ? AND name = ?`,
		srv.Title, srv.Host, srv.Port, srv.User, srv.KeyPath, srv.KeyCredential, string(opts),
		srv.Host, srv.Port, srv.User, srv.KeyPath, srv.KeyCredential, string(opts),
		srv.Host, srv.Port, srv.User, srv.KeyPath, srv.KeyCredential, string(opts),
		time.Now().Unix(), s.owner, srv.Name)
	if err != nil {
		return fmt.Errorf("保存服务器：%w", err)
	}
	return mustAffect(res)
}

// SetServerFacts 保存检测结果。
func (s *Store) SetServerFacts(ctx context.Context, name string, facts *model.ServerFacts, at time.Time) error {
	b, _ := json.Marshal(facts)
	res, err := s.db.ExecContext(ctx, `UPDATE servers SET facts = ?, checked_at = ? WHERE owner_id = ? AND name = ?`, string(b), at.Unix(), s.owner, name)
	if err != nil {
		return fmt.Errorf("保存检测结果：%w", err)
	}
	return mustAffect(res)
}

// DeleteServer 删除服务器。有应用引用时数据库会拒绝（调用方应先检查并给出提示）。
func (s *Store) DeleteServer(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE owner_id = ? AND name = ?`, s.owner, name)
	if err != nil {
		return fmt.Errorf("删除服务器：%w", err)
	}
	return mustAffect(res)
}

// ---------- 密钥与变量 ----------

// ListSecrets 返回所有密钥（不含值），按名称排序。
func (s *Store) ListSecrets(ctx context.Context) ([]*model.Secret, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, created_at, updated_at FROM secrets WHERE owner_id = ? ORDER BY name`, s.owner)
	if err != nil {
		return nil, fmt.Errorf("读取密钥列表：%w", err)
	}
	defer rows.Close()
	list := []*model.Secret{}
	for rows.Next() {
		var sec model.Secret
		var created, updated int64
		if err := rows.Scan(&sec.ID, &sec.Name, &sec.Description, &created, &updated); err != nil {
			return nil, err
		}
		sec.CreatedAt, sec.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
		sec.UsedBy = []string{}
		list = append(list, &sec)
	}
	return list, rows.Err()
}

// SecretValue 返回密钥加密后的值。
func (s *Store) SecretValue(ctx context.Context, name string) (string, error) {
	var enc string
	err := s.db.QueryRowContext(ctx, `SELECT value_enc FROM secrets WHERE owner_id = ? AND name = ?`, s.owner, name).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return enc, err
}

// SecretNames 返回所有密钥名称的集合。
func (s *Store) SecretNames(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM secrets WHERE owner_id = ?`, s.owner)
	if err != nil {
		return nil, fmt.Errorf("读取密钥列表：%w", err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names[n] = true
	}
	return names, rows.Err()
}

// CreateSecret 新增密钥，valueEnc 是加密后的值。
func (s *Store) CreateSecret(ctx context.Context, name, valueEnc, description string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO secrets (owner_id, name, value_enc, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		s.owner, name, valueEnc, description, now, now)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("保存密钥：%w", err)
	}
	return nil
}

// UpdateSecret 修改密钥；valueEnc 为 nil 时不改值。
func (s *Store) UpdateSecret(ctx context.Context, name string, valueEnc *string, description string) error {
	var res sql.Result
	var err error
	now := time.Now().Unix()
	if valueEnc != nil {
		res, err = s.db.ExecContext(ctx, `UPDATE secrets SET value_enc = ?, description = ?, updated_at = ? WHERE owner_id = ? AND name = ?`, *valueEnc, description, now, s.owner, name)
	} else {
		res, err = s.db.ExecContext(ctx, `UPDATE secrets SET description = ?, updated_at = ? WHERE owner_id = ? AND name = ?`, description, now, s.owner, name)
	}
	if err != nil {
		return fmt.Errorf("保存密钥：%w", err)
	}
	return mustAffect(res)
}

// DeleteSecret 删除密钥。
func (s *Store) DeleteSecret(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE owner_id = ? AND name = ?`, s.owner, name)
	if err != nil {
		return fmt.Errorf("删除密钥：%w", err)
	}
	return mustAffect(res)
}

// ---------- 项目 ----------

const projectCols = `p.id, p.name, p.title, p.description, p.created_at, p.updated_at, (SELECT COUNT(*) FROM apps a WHERE a.project_id = p.id)`

func scanProject(row interface{ Scan(...any) error }) (*model.Project, error) {
	var p model.Project
	var created, updated int64
	if err := row.Scan(&p.ID, &p.Name, &p.Title, &p.Description, &created, &updated, &p.AppCount); err != nil {
		return nil, err
	}
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return &p, nil
}

// ListProjects 返回所有项目，按名称排序。
func (s *Store) ListProjects(ctx context.Context) ([]*model.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectCols+` FROM projects p WHERE p.owner_id = ? ORDER BY p.name`, s.owner)
	if err != nil {
		return nil, fmt.Errorf("读取项目列表：%w", err)
	}
	defer rows.Close()
	list := []*model.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// GetProject 按名称读取项目。
func (s *Store) GetProject(ctx context.Context, name string) (*model.Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects p WHERE p.owner_id = ? AND p.name = ?`, s.owner, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// CreateProject 新增项目。
func (s *Store) CreateProject(ctx context.Context, p *model.Project) error {
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO projects (owner_id, name, title, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		s.owner, p.Name, p.Title, p.Description, now.Unix(), now.Unix())
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("保存项目：%w", err)
	}
	p.ID, _ = res.LastInsertId()
	p.CreatedAt, p.UpdatedAt = now, now
	return nil
}

// UpdateProject 修改项目的名称和说明（标识不能改）。
func (s *Store) UpdateProject(ctx context.Context, p *model.Project) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET title = ?, description = ?, updated_at = ? WHERE owner_id = ? AND name = ?`,
		p.Title, p.Description, time.Now().Unix(), s.owner, p.Name)
	if err != nil {
		return fmt.Errorf("保存项目：%w", err)
	}
	return mustAffect(res)
}

// DeleteProject 删除项目。还有应用时数据库会拒绝（调用方应先检查并给出提示）。
func (s *Store) DeleteProject(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE owner_id = ? AND name = ?`, s.owner, name)
	if err != nil {
		return fmt.Errorf("删除项目：%w", err)
	}
	return mustAffect(res)
}

// ---------- 应用 ----------

const appCols = `a.id, a.project_id, p.name, a.name, a.title, a.driver, a.server_id, COALESCE(s.name, ''), a.params, a.revision, a.created_at, a.updated_at`
const appFrom = ` FROM apps a JOIN projects p ON p.id = a.project_id LEFT JOIN servers s ON s.id = a.server_id`

func scanApp(row interface{ Scan(...any) error }) (*model.App, error) {
	var a model.App
	var serverID sql.NullInt64
	var params string
	var created, updated int64
	if err := row.Scan(&a.ID, &a.ProjectID, &a.Project, &a.Name, &a.Title, &a.Driver, &serverID, &a.Server, &params, &a.Revision, &created, &updated); err != nil {
		return nil, err
	}
	if serverID.Valid {
		a.ServerID = &serverID.Int64
	}
	if err := json.Unmarshal([]byte(params), &a.Params); err != nil {
		return nil, fmt.Errorf("应用 %s/%s 的参数格式不对：%w", a.Project, a.Name, err)
	}
	a.CreatedAt, a.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return &a, nil
}

func (s *Store) queryApps(ctx context.Context, where string, args ...any) ([]*model.App, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+appCols+appFrom+` `+where+` ORDER BY p.name, a.name`, args...)
	if err != nil {
		return nil, fmt.Errorf("读取应用列表：%w", err)
	}
	defer rows.Close()
	list := []*model.App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// ListApps 返回项目下的应用；project 为空时返回所有应用。
func (s *Store) ListApps(ctx context.Context, project string) ([]*model.App, error) {
	if project == "" {
		return s.queryApps(ctx, "WHERE p.owner_id = ?", s.owner)
	}
	return s.queryApps(ctx, "WHERE p.owner_id = ? AND p.name = ?", s.owner, project)
}

// GetApp 按项目和应用名称读取应用。
func (s *Store) GetApp(ctx context.Context, project, name string) (*model.App, error) {
	a, err := scanApp(s.db.QueryRowContext(ctx, `SELECT `+appCols+appFrom+` WHERE p.owner_id = ? AND p.name = ? AND a.name = ?`, s.owner, project, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// CreateApp 新增应用，同时保存第 1 个版本。a.Params 应已加密敏感字段。
func (s *Store) CreateApp(ctx context.Context, a *model.App) error {
	params, err := json.Marshal(a.Params)
	if err != nil {
		return err
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO apps (project_id, name, title, driver, server_id, params, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		a.ProjectID, a.Name, a.Title, a.Driver, a.ServerID, string(params), now.Unix(), now.Unix())
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("保存应用：%w", err)
	}
	a.ID, _ = res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_revisions (app_id, revision, params, created_at) VALUES (?, 1, ?, ?)`,
		a.ID, string(params), now.Unix()); err != nil {
		return fmt.Errorf("保存应用版本：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.Revision, a.CreatedAt, a.UpdatedAt = 1, now, now
	return nil
}

// UpdateApp 保存应用的新参数，生成新版本。baseRevision 不是当前版本时返回 ErrConflict（别处已经改过）。
func (s *Store) UpdateApp(ctx context.Context, a *model.App, baseRevision int) error {
	params, err := json.Marshal(a.Params)
	if err != nil {
		return err
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE apps SET title = ?, server_id = ?, params = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?`,
		a.Title, a.ServerID, string(params), now.Unix(), a.ID, baseRevision)
	if err != nil {
		return fmt.Errorf("保存应用：%w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_revisions (app_id, revision, params, created_at) VALUES (?, ?, ?, ?)`,
		a.ID, baseRevision+1, string(params), now.Unix()); err != nil {
		return fmt.Errorf("保存应用版本：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.Revision, a.UpdatedAt = baseRevision+1, now
	return nil
}

// DeleteApp 删除应用和它的所有版本。
func (s *Store) DeleteApp(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除应用：%w", err)
	}
	return mustAffect(res)
}

// ListRevisions 返回应用的所有版本，新的在前。
func (s *Store) ListRevisions(ctx context.Context, appID int64) ([]*model.AppRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT revision, params, created_at FROM app_revisions WHERE app_id = ? ORDER BY revision DESC`, appID)
	if err != nil {
		return nil, fmt.Errorf("读取修改记录：%w", err)
	}
	defer rows.Close()
	list := []*model.AppRevision{}
	for rows.Next() {
		var r model.AppRevision
		var params string
		var created int64
		if err := rows.Scan(&r.Revision, &params, &created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(params), &r.Params); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(created, 0)
		list = append(list, &r)
	}
	return list, rows.Err()
}

func mustAffect(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
