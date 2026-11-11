package engine

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"deployhub/internal/model"
)

// LogQuery 是读取任务日志的条件。
type LogQuery struct {
	// Tail 只要最后几行；和 After 都没填时默认最后 200 行。
	Tail int
	// After 只要编号大于它的事件之后的日志，用上一页返回的 Next 接着读；-1 表示从头读。
	After int64
	// Limit 是最多返回几行，默认 500，最多 2000。
	Limit int
	// FailedOnly 只要失败步骤的日志和错误。
	FailedOnly bool
}

// LogPage 是一页任务日志：每行是「时间 [应用] 内容」。
type LogPage struct {
	JobID  int64    `json:"jobId"`
	Status string   `json:"status"`
	Lines  []string `json:"lines"`
	// Next 是这一页最后一条日志的编号，传给 after 接着读后面的。
	Next int64 `json:"next"`
	// Omitted 是因为行数限制没有返回的行数（tail 时是前面的，否则是后面的）。
	Omitted int `json:"omitted"`
}

const maxLogLine = 2000

// Logs 把任务事件整理成适合人和 AI 读的文本行，分页返回，避免一次返回几万行。
func (e *Engine) Logs(ctx context.Context, jobID int64, q LogQuery) (*LogPage, error) {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if q.Limit <= 0 {
		q.Limit = 500
	}
	if q.Limit > 2000 {
		q.Limit = 2000
	}
	if q.Tail <= 0 && q.After == 0 {
		q.Tail = 200
	}
	if q.Tail > q.Limit {
		q.Tail = q.Limit
	}
	evs, err := e.store.JobEvents(ctx, jobID, q.After, 100000)
	if err != nil {
		return nil, err
	}
	if q.FailedOnly {
		evs = failedEvents(evs)
	}
	page := &LogPage{JobID: jobID, Status: job.Status, Lines: []string{}, Next: q.After}
	switch {
	case q.Tail > 0 && len(evs) > q.Tail:
		page.Omitted = len(evs) - q.Tail
		evs = evs[len(evs)-q.Tail:]
	case q.Tail <= 0 && len(evs) > q.Limit:
		page.Omitted = len(evs) - q.Limit
		evs = evs[:q.Limit]
	}
	for _, ev := range evs {
		page.Lines = append(page.Lines, formatEvent(job, ev))
		page.Next = ev.ID
	}
	return page, nil
}

// failedEvents 只留失败步骤的事件和错误：每个应用里，从失败步骤开始的那一行到它失败的那一行。
func failedEvents(evs []*model.JobEvent) []*model.JobEvent {
	type key struct {
		unit int
		step string
	}
	unitOf := func(ev *model.JobEvent) int {
		if ev.UnitSeq == nil {
			return -1
		}
		return *ev.UnitSeq
	}
	failed := map[key]bool{}
	for _, ev := range evs {
		if ev.Type == "step_fail" {
			failed[key{unitOf(ev), ev.Step}] = true
		}
	}
	var out []*model.JobEvent
	for _, ev := range evs {
		if ev.Type == "error" || (ev.Step != "" && failed[key{unitOf(ev), ev.Step}]) {
			out = append(out, ev)
		}
	}
	return out
}

func formatEvent(job *model.Job, ev *model.JobEvent) string {
	msg := ev.Message
	if len(msg) > maxLogLine {
		msg = msg[:maxLogLine] + "…（已截断）"
	}
	switch ev.Type {
	case "step_start":
		msg = "▶ " + ev.Step
	case "step_done":
		msg = "✓ " + ev.Step + " 完成"
		if ms, err := strconv.ParseInt(ev.Message, 10, 64); err == nil {
			msg += fmt.Sprintf("（%.1f 秒）", float64(ms)/1000)
		}
	case "step_fail":
		msg = "✗ " + ev.Step + " 失败：" + msg
	case "warn":
		msg = "⚠ " + msg
	case "error":
		msg = "✗ " + msg
	}
	line := ev.Time.Format("15:04:05") + " "
	if ev.UnitSeq != nil && *ev.UnitSeq >= 0 && *ev.UnitSeq < len(job.Units) && len(job.Units) > 1 {
		line += "[" + job.Units[*ev.UnitSeq].App + "] "
	}
	return line + msg
}

// Finished 判断任务是不是不会再变了：执行结束、被拒绝或撤回。待审批不算结束，但也不在执行。
func Finished(status string) bool {
	return status != "queued" && status != "running" && status != "pending_approval"
}

// WaitJob 等任务执行结束，最多等 timeout；待审批的任务立刻返回（要等人在页面上批准）。返回任务当前的状态。
func (e *Engine) WaitJob(ctx context.Context, jobID int64, timeout time.Duration) (*model.Job, error) {
	deadline := time.Now().Add(timeout)
	for {
		job, err := e.store.GetJob(ctx, jobID)
		if err != nil {
			return nil, err
		}
		if (job.Status != "queued" && job.Status != "running") || !time.Now().Before(deadline) {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, nil
		case <-time.After(300 * time.Millisecond):
		}
	}
}
