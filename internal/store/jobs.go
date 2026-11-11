package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"deployhub/internal/model"
)

// CreateJob 新建任务和它的应用，单元为 pending。任务状态用 j.Status（待审批的是 pending_approval），没填时是 queued。
// 幂等键和已有任务重复时返回 ErrDuplicate。
func (s *Store) CreateJob(ctx context.Context, j *model.Job) error {
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if j.Status == "" {
		j.Status = "queued"
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO jobs (owner_id, kind, status, initiator, idempotency_key, continue_on_error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.owner, j.Kind, j.Status, j.Initiator, j.IdempotencyKey, j.ContinueOnError, now.Unix())
	if isUnique(err) {
		return ErrDuplicate // 幂等键重复
	}
	if err != nil {
		return fmt.Errorf("保存任务：%w", err)
	}
	j.ID, _ = res.LastInsertId()
	j.CreatedAt = now
	for i, u := range j.Units {
		u.JobID, u.Seq, u.Status = j.ID, i, "pending"
		res, err := tx.ExecContext(ctx, `INSERT INTO job_units (job_id, seq, app_id, project, app, title, driver, server, revision, target, version, previous, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending')`,
			j.ID, i, u.AppID, u.Project, u.App, u.Title, u.Driver, u.Server, u.Revision, u.Target, u.Version, u.Previous)
		if err != nil {
			return fmt.Errorf("保存任务：%w", err)
		}
		u.ID, _ = res.LastInsertId()
	}
	return tx.Commit()
}

// SetJobStatus 修改任务状态；running 时记开始时间，结束状态记结束时间。
func (s *Store) SetJobStatus(ctx context.Context, id int64, status, errMsg string) error {
	now := time.Now().Unix()
	var err error
	switch status {
	case "running":
		_, err = s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, started_at = ? WHERE id = ?`, status, now, id)
	case "queued":
		_, err = s.db.ExecContext(ctx, `UPDATE jobs SET status = ? WHERE id = ?`, status, id)
	default:
		_, err = s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, error = ?, finished_at = ? WHERE id = ?`, status, errMsg, now, id)
	}
	if err != nil {
		return fmt.Errorf("更新任务状态：%w", err)
	}
	return nil
}

// UpdateUnit 保存任务单元的执行情况。
func (s *Store) UpdateUnit(ctx context.Context, u *model.JobUnit) error {
	_, err := s.db.ExecContext(ctx, `UPDATE job_units SET version = ?, previous = ?, commit_hash = ?, status = ?, step = ?, error = ?, started_at = ?, finished_at = ?
		WHERE id = ?`,
		u.Version, u.Previous, u.Commit, u.Status, u.Step, u.Error, unixPtr(u.StartedAt), unixPtr(u.FinishedAt), u.ID)
	if err != nil {
		return fmt.Errorf("更新任务单元：%w", err)
	}
	return nil
}

// AddJobEvent 追加一条任务事件，返回带编号的事件。
func (s *Store) AddJobEvent(ctx context.Context, e *model.JobEvent) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO job_events (job_id, unit_seq, time, type, step, message) VALUES (?, ?, ?, ?, ?, ?)`,
		e.JobID, e.UnitSeq, e.Time.UnixMilli(), e.Type, e.Step, e.Message)
	if err != nil {
		return fmt.Errorf("保存任务事件：%w", err)
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

// JobEvents 返回任务编号大于 after 的事件，最多 limit 条。
func (s *Store) JobEvents(ctx context.Context, jobID, after int64, limit int) ([]*model.JobEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, job_id, unit_seq, time, type, step, message FROM job_events
		WHERE job_id = ? AND id > ? AND EXISTS (SELECT 1 FROM jobs j WHERE j.id = job_events.job_id AND j.owner_id = ?)
		ORDER BY id LIMIT ?`, jobID, after, s.owner, limit)
	if err != nil {
		return nil, fmt.Errorf("读取任务事件：%w", err)
	}
	defer rows.Close()
	list := []*model.JobEvent{}
	for rows.Next() {
		var e model.JobEvent
		var seq sql.NullInt64
		var ms int64
		if err := rows.Scan(&e.ID, &e.JobID, &seq, &ms, &e.Type, &e.Step, &e.Message); err != nil {
			return nil, err
		}
		if seq.Valid {
			n := int(seq.Int64)
			e.UnitSeq = &n
		}
		e.Time = time.UnixMilli(ms)
		list = append(list, &e)
	}
	return list, rows.Err()
}

const unitCols = `id, job_id, seq, app_id, project, app, title, driver, server, revision, target, version, previous, commit_hash, status, step, error, started_at, finished_at`

func scanUnit(row interface{ Scan(...any) error }) (*model.JobUnit, error) {
	var u model.JobUnit
	var appID, started, finished sql.NullInt64
	if err := row.Scan(&u.ID, &u.JobID, &u.Seq, &appID, &u.Project, &u.App, &u.Title, &u.Driver, &u.Server, &u.Revision,
		&u.Target, &u.Version, &u.Previous, &u.Commit, &u.Status, &u.Step, &u.Error, &started, &finished); err != nil {
		return nil, err
	}
	if appID.Valid {
		u.AppID = &appID.Int64
	}
	u.StartedAt, u.FinishedAt = timePtr(started), timePtr(finished)
	return &u, nil
}

const jobCols = `id, kind, status, initiator, error, created_at, started_at, finished_at, idempotency_key, continue_on_error`

func scanJob(row interface{ Scan(...any) error }) (*model.Job, error) {
	var j model.Job
	var created int64
	var started, finished sql.NullInt64
	if err := row.Scan(&j.ID, &j.Kind, &j.Status, &j.Initiator, &j.Error, &created, &started, &finished, &j.IdempotencyKey, &j.ContinueOnError); err != nil {
		return nil, err
	}
	j.CreatedAt = time.Unix(created, 0)
	j.StartedAt, j.FinishedAt = timePtr(started), timePtr(finished)
	j.Units = []*model.JobUnit{}
	return &j, nil
}

// GetJob 读取任务和它的应用。
func (s *Store) GetJob(ctx context.Context, id int64) (*model.Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ? AND owner_id = ?`, id, s.owner))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.fillUnits(ctx, []*model.Job{j}); err != nil {
		return nil, err
	}
	return j, nil
}

// JobFilter 是任务列表的筛选条件。
type JobFilter struct {
	Project, App string // 只看某个应用（两个都要填）或某个项目的任务
	Status       string // 只看某个状态的任务，如 pending_approval
	Limit        int
	Before       int64 // 只返回编号小于它的，用于翻页
}

// ListJobs 返回任务列表，新的在前。
func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]*model.Job, error) {
	where, args := []string{"owner_id = ?"}, []any{s.owner}
	if f.Project != "" {
		q := `EXISTS (SELECT 1 FROM job_units u WHERE u.job_id = jobs.id AND u.project = ?`
		args = append(args, f.Project)
		if f.App != "" {
			q += ` AND u.app = ?`
			args = append(args, f.App)
		}
		where = append(where, q+`)`)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Before > 0 {
		where = append(where, "id < ?")
		args = append(args, f.Before)
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs
		WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("读取任务列表：%w", err)
	}
	defer rows.Close()
	list := []*model.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, s.fillUnits(ctx, list)
}

func (s *Store) fillUnits(ctx context.Context, jobs []*model.Job) error {
	if len(jobs) == 0 {
		return nil
	}
	byID := map[int64]*model.Job{}
	ids := make([]string, len(jobs))
	for i, j := range jobs {
		byID[j.ID] = j
		ids[i] = fmt.Sprint(j.ID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+unitCols+` FROM job_units WHERE job_id IN (`+strings.Join(ids, ",")+`) ORDER BY job_id, seq`)
	if err != nil {
		return fmt.Errorf("读取任务单元：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUnit(rows)
		if err != nil {
			return err
		}
		byID[u.JobID].Units = append(byID[u.JobID].Units, u)
	}
	return rows.Err()
}

// JobByIdempotencyKey 按幂等键读取任务，没有时返回 ErrNotFound。
func (s *Store) JobByIdempotencyKey(ctx context.Context, key string) (*model.Job, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM jobs WHERE owner_id = ? AND idempotency_key = ?`, s.owner, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

// PendingJobForApp 返回应用待审批的任务编号，没有时返回 0。
func (s *Store) PendingJobForApp(ctx context.Context, appID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT j.id FROM jobs j JOIN job_units u ON u.job_id = j.id
		WHERE u.app_id = ? AND j.status = 'pending_approval' ORDER BY j.id DESC LIMIT 1`, appID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// ResolvePendingJob 把待审批的任务改成 status：批准后是 queued，拒绝后是 rejected，发起方自己撤回是 canceled。
// 任务不是待审批状态时返回 ErrConflict，两个人同时点只有一个生效。不执行的（拒绝、撤回）把各应用标记为跳过。
func (s *Store) ResolvePendingJob(ctx context.Context, id int64, status, note string) error {
	now := time.Now().Unix()
	var res sql.Result
	var err error
	ended := status != "queued"
	if ended {
		res, err = s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, error = ?, finished_at = ? WHERE id = ? AND owner_id = ? AND status = 'pending_approval'`,
			status, note, now, id, s.owner)
	} else {
		res, err = s.db.ExecContext(ctx, `UPDATE jobs SET status = ? WHERE id = ? AND owner_id = ? AND status = 'pending_approval'`, status, id, s.owner)
	}
	if err != nil {
		return fmt.Errorf("更新任务状态：%w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	if ended {
		if _, err := s.db.ExecContext(ctx, `UPDATE job_units SET status = 'skipped', finished_at = ? WHERE job_id = ?`, now, id); err != nil {
			return fmt.Errorf("更新任务单元：%w", err)
		}
	}
	return nil
}

// ActiveJobForApp 返回应用正在进行（排队或执行中）的任务编号，没有时返回 0。
func (s *Store) ActiveJobForApp(ctx context.Context, appID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT j.id FROM jobs j JOIN job_units u ON u.job_id = j.id
		WHERE u.app_id = ? AND j.status IN ('queued', 'running') ORDER BY j.id DESC LIMIT 1`, appID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// InterruptJobs 把还在进行中的任务标记为失败，用于服务启动时（上次退出时任务被中断）。返回处理的任务数。
func (s *Store) InterruptJobs(ctx context.Context, reason string) (int, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'failed', error = ?, finished_at = ? WHERE owner_id = ? AND status IN ('queued', 'running')`, reason, now, s.owner)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := s.db.ExecContext(ctx, `UPDATE job_units SET status = CASE status WHEN 'running' THEN 'failed' ELSE 'skipped' END,
		error = CASE status WHEN 'running' THEN ? ELSE error END, finished_at = ?
		WHERE status IN ('pending', 'running') AND job_id IN (SELECT id FROM jobs WHERE owner_id = ?)`, reason, now, s.owner); err != nil {
		return 0, err
	}
	return int(n), nil
}

// ImportJob 保存一条从别处导入的已结束任务（如旧工具的部署历史），时间用记录里的时间。
func (s *Store) ImportJob(ctx context.Context, j *model.Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := j.CreatedAt.Unix()
	res, err := tx.ExecContext(ctx, `INSERT INTO jobs (owner_id, kind, status, initiator, error, created_at, started_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.owner, j.Kind, j.Status, j.Initiator, j.Error, at, at, at)
	if err != nil {
		return fmt.Errorf("保存任务：%w", err)
	}
	j.ID, _ = res.LastInsertId()
	for i, u := range j.Units {
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_units (job_id, seq, app_id, project, app, title, driver, server, revision, version, previous, status, error, started_at, finished_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)`,
			j.ID, i, u.AppID, u.Project, u.App, u.Title, u.Driver, u.Server, u.Version, u.Previous, u.Status, u.Error, at, at); err != nil {
			return fmt.Errorf("保存任务：%w", err)
		}
	}
	return tx.Commit()
}

// ImportedExists 判断某条导入记录是否已经导入过（按发起方、时间、应用、版本、动作判断）。
func (s *Store) ImportedExists(ctx context.Context, initiator string, at time.Time, project, app, version, kind string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs j JOIN job_units u ON u.job_id = j.id
		WHERE j.owner_id = ? AND j.initiator = ? AND j.created_at = ? AND j.kind = ? AND u.project = ? AND u.app = ? AND u.version = ?`,
		s.owner, initiator, at.Unix(), kind, project, app, version).Scan(&n)
	return n > 0, err
}
