package remote

import (
	"slices"
	"strings"
	"testing"
)

func TestArgs(t *testing.T) {
	s := &SSH{ControlDir: "/tmp"}
	args := s.Args(Target{Host: "1.2.3.4", Port: 5022, User: "deploy", KeyPath: "/k/id", Options: []string{"StrictHostKeyChecking=accept-new"}})
	joined := strings.Join(args, " ")
	for _, want := range []string{"-p 5022", "BatchMode=yes", "ControlPath=/tmp/deployhub-%C", "-i /k/id", "-- deploy@1.2.3.4"} {
		if !strings.Contains(joined, want) {
			t.Errorf("参数缺少 %q：%s", want, joined)
		}
	}
	// 自定义选项在默认选项前面（ssh 取第一次出现的值）
	if slices.Index(args, "StrictHostKeyChecking=accept-new") > slices.Index(args, "BatchMode=yes") {
		t.Error("自定义选项应在默认选项之前")
	}
	if args[len(args)-1] != "deploy@1.2.3.4" || args[len(args)-2] != "--" {
		t.Error("目标前应有 --，防止主机名被当成选项")
	}
}

func TestDefaultControlPathIsShort(t *testing.T) {
	args := strings.Join(NewSSH().Args(Target{Host: "h"}), " ")
	// %C 展开后是 40 个字符的哈希，整个路径要在 104 字节以内
	i := strings.Index(args, "ControlPath=")
	path := strings.Fields(args[i+len("ControlPath="):])[0]
	if len(path)-2+40 > 100 {
		t.Fatalf("ControlPath 太长：%s", path)
	}
}

func TestExplain(t *testing.T) {
	cases := map[string]string{
		"Host key verification failed.":                       "主机指纹",
		"deploy@1.2.3.4: Permission denied (publickey).":      "认证失败",
		"ssh: Could not resolve hostname x: nodename":         "域名解析",
		"ssh: connect to host x port 22: Connection refused":  "连接被拒绝",
		"ssh: connect to host x port 22: Operation timed out": "超时",
		"something else\nmore":                                "something else",
	}
	for in, want := range cases {
		if got := explain(in); !strings.Contains(got, want) {
			t.Errorf("explain(%q) = %q，期望含 %q", in, got, want)
		}
	}
}

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"":              "''",
		"/opt/app":      "/opt/app",
		"a b":           "'a b'",
		"it's":          `'it'"'"'s'`,
		"$(rm -rf /)":   "'$(rm -rf /)'",
		"KEY=value,x:1": "KEY=value,x:1",
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s，期望 %s", in, got, want)
		}
	}
}

func TestLabel(t *testing.T) {
	if got := (Target{Host: "h", User: "u", Port: 22}).Label(); got != "u@h" {
		t.Errorf("Label = %s", got)
	}
	if got := (Target{Host: "h", Port: 2222}).Label(); got != "h:2222" {
		t.Errorf("Label = %s", got)
	}
}
