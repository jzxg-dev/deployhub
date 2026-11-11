package shell

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunStreamsLines(t *testing.T) {
	var lines []string
	err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo a; echo b >&2; echo $X"}, Env: []string{"X=c"}}, func(l string) {
		lines = append(lines, l)
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, ",") != "a,b,c" {
		t.Fatalf("lines = %v", lines)
	}
}

func TestRunError(t *testing.T) {
	err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo 出错了; exit 3"}}, nil)
	var e *Error
	if !errors.As(err, &e) || e.ExitCode != 3 || len(e.Tail) != 1 || e.Tail[0] != "出错了" {
		t.Fatalf("err = %#v", err)
	}
}

func TestRunCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Run(ctx, Cmd{Name: "sh", Args: []string{"-c", "sleep 10 & sleep 10; wait"}}, nil)
	if err == nil || time.Since(start) > 4*time.Second {
		t.Fatalf("取消后应很快结束：%v %v", err, time.Since(start))
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应返回 ctx 的错误：%v", err)
	}
}

func TestDisplay(t *testing.T) {
	if got := (Cmd{Name: "go", Args: []string{"build", "-ldflags=-s -w"}}).String(); got != "go build '-ldflags=-s -w'" {
		t.Fatalf("String = %s", got)
	}
	if got := (Cmd{Name: "x", Display: "隐藏"}).String(); got != "隐藏" {
		t.Fatal(got)
	}
}
