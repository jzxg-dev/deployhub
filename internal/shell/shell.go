// Package shell 执行本地命令，按行实时转发输出，支持取消。
package shell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Cmd 是要执行的本地命令。
type Cmd struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string // 追加到当前环境变量后面，如 GOOS=linux
	Stdin io.Reader
	// Display 是日志里显示的命令，为空时由 Name 和 Args 拼出。用于隐藏不该出现在日志里的部分。
	Display string
}

// String 返回显示用的命令行。
func (c Cmd) String() string {
	if c.Display != "" {
		return c.Display
	}
	parts := []string{c.Name}
	for _, a := range c.Args {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

// Error 是命令执行失败，带退出码和最后几行输出。
type Error struct {
	Cmd      string
	ExitCode int
	Tail     []string
	Err      error
}

func (e *Error) Error() string {
	if e.ExitCode > 0 {
		return fmt.Sprintf("命令退出码 %d：%s", e.ExitCode, e.Cmd)
	}
	return fmt.Sprintf("命令执行失败：%s：%v", e.Cmd, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Run 执行命令，每行输出（stdout 和 stderr 合并）调用一次 onLine。ctx 取消时先发 SIGTERM，5 秒后强制结束。
func Run(ctx context.Context, c Cmd, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = c.Stdin
	// 独立进程组：取消时连同子进程（如 go build 起的编译器）一起结束
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second

	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw

	tail := newTail(20)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			tail.add(line)
			if onLine != nil {
				onLine(line)
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()

	err := cmd.Start()
	if err == nil {
		err = cmd.Wait()
	}
	pw.Close()
	wg.Wait()
	if err == nil {
		return nil
	}
	e := &Error{Cmd: c.String(), Tail: tail.lines(), Err: err}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		e.ExitCode = ee.ExitCode()
	}
	if ctx.Err() != nil {
		e.Err = ctx.Err()
		e.ExitCode = 0
	}
	return e
}

type tailBuf struct {
	mu  sync.Mutex
	n   int
	buf []string
}

func newTail(n int) *tailBuf { return &tailBuf{n: n} }

func (t *tailBuf) add(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, s)
	if len(t.buf) > t.n {
		t.buf = t.buf[len(t.buf)-t.n:]
	}
}

func (t *tailBuf) lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.buf...)
}

func quote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@%+,", r)) {
			return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
		}
	}
	return s
}
