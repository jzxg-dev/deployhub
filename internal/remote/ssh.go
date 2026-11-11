// Package remote 通过系统的 ssh 在服务器上执行命令。
//
// 直接调用 ssh 命令而不是 Go 的 SSH 库：复用 ~/.ssh/config、known_hosts 和 ssh-agent，
// 行为和手动 ssh 一致；同一台服务器通过 ControlMaster 复用连接。
package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Target 是一台服务器的连接信息。
type Target struct {
	Host    string
	Port    int
	User    string
	KeyPath string   // 私钥路径，可以用 ~；为空时用 ssh 默认的密钥和 ssh-agent
	Options []string // 额外的 ssh -o 选项，如 StrictHostKeyChecking=accept-new
}

// Label 返回 user@host:port，用于提示。
func (t Target) Label() string {
	s := t.Host
	if t.User != "" {
		s = t.User + "@" + s
	}
	if t.Port != 0 && t.Port != 22 {
		s += ":" + strconv.Itoa(t.Port)
	}
	return s
}

// Result 是一次远程执行的结果。
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Runner 在服务器上执行脚本。测试时可以替换成假的实现。
type Runner interface {
	// Run 把 script 交给服务器上的 sh 执行。连接失败返回 *ConnectError；脚本退出码非 0 不算错误，看 Result.ExitCode。
	Run(ctx context.Context, t Target, script string) (*Result, error)
}

// ConnectError 表示连不上服务器（ssh 退出码 255）。
type ConnectError struct {
	Target Target
	Reason string // 中文原因
	Detail string // ssh 原始输出
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("连接 %s 失败：%s", e.Target.Label(), e.Reason)
}

// SSH 用系统 ssh 执行命令。
type SSH struct {
	// ControlDir 存放连接复用的 socket。unix socket 路径不能超过 104 字节，
	// macOS 的 os.TempDir()（/var/folders/...）太长，所以默认用 /tmp。
	ControlDir string
}

// NewSSH 创建 SSH 执行器。
func NewSSH() *SSH { return &SSH{ControlDir: defaultControlDir()} }

func defaultControlDir() string {
	if st, err := os.Stat("/tmp"); err == nil && st.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}

// Args 返回 ssh 的参数（不含命令本身）。
func (s *SSH) Args(t Target) []string {
	return append(s.Options(t), "--", Dest(t))
}

// Dest 返回 ssh 目标，如 deploy@1.2.3.4。
func Dest(t Target) string {
	if t.User != "" {
		return t.User + "@" + t.Host
	}
	return t.Host
}

// Options 返回 ssh 的选项（不含目标），scp、rsync 也用它们，共享同一个复用连接。
// 服务器自定义选项放在前面：ssh 对同一选项取第一次出现的值。
func (s *SSH) Options(t Target) []string {
	args := []string{}
	if t.Port != 0 {
		args = append(args, "-p", strconv.Itoa(t.Port))
	}
	for _, o := range t.Options {
		args = append(args, "-o", o)
	}
	controlDir := s.ControlDir
	if controlDir == "" {
		controlDir = defaultControlDir()
	}
	args = append(args,
		"-o", "BatchMode=yes", // 不交互：不问密码、不问是否信任主机指纹
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath="+filepath.Join(controlDir, "deployhub-%C"),
		"-o", "ControlPersist=120",
	)
	if t.KeyPath != "" {
		args = append(args, "-i", ExpandHome(t.KeyPath), "-o", "IdentitiesOnly=yes")
	}
	return args
}

// Run 通过标准输入把脚本交给服务器上的 sh -s 执行，不受登录 shell 和参数转义的影响。
func (s *SSH) Run(ctx context.Context, t Target, script string) (*Result, error) {
	cmd := exec.CommandContext(ctx, "ssh", append(s.Args(t), "sh -s")...)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	res := &Result{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			if errors.Is(err, exec.ErrNotFound) {
				return nil, errors.New("本机找不到 ssh 命令")
			}
			if ctx.Err() != nil {
				return nil, &ConnectError{Target: t, Reason: "超时或已取消"}
			}
			return nil, err
		}
		res.ExitCode = ee.ExitCode()
		if res.ExitCode == 255 {
			return nil, &ConnectError{Target: t, Reason: explain(res.Stderr), Detail: strings.TrimSpace(res.Stderr)}
		}
	}
	return res, nil
}

// explain 把常见的 ssh 错误翻译成中文。
func explain(stderr string) string {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "host key verification failed"):
		return "主机指纹未确认：先在终端手动 ssh 登录一次确认指纹，或在 SSH 选项里加 StrictHostKeyChecking=accept-new"
	case strings.Contains(s, "remote host identification has changed"):
		return "主机指纹和之前记录的不一致，可能是服务器重装或有中间人攻击，请核实后更新 ~/.ssh/known_hosts"
	case strings.Contains(s, "permission denied"):
		return "认证失败：检查用户名和 SSH 密钥，服务器上是否已加入对应的公钥"
	case strings.Contains(s, "could not resolve hostname"):
		return "域名解析失败，检查服务器地址"
	case strings.Contains(s, "connection refused"):
		return "连接被拒绝，检查端口和服务器上的 sshd"
	case strings.Contains(s, "timed out"):
		return "连接超时，检查地址、端口和网络（防火墙、安全组）"
	case strings.Contains(s, "no such identity"), strings.Contains(s, "identity file") && strings.Contains(s, "not accessible"):
		return "找不到 SSH 密钥文件"
	case strings.Contains(s, "bad permissions"), strings.Contains(s, "unprotected private key"):
		return "SSH 密钥文件权限太宽，请执行 chmod 600 <密钥文件>"
	}
	if line := firstLine(stderr); line != "" {
		return line
	}
	return "ssh 返回 255"
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// ExpandHome 把开头的 ~ 换成用户主目录。
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Quote 把字符串转义成 sh 的单引号字符串。
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@%+,", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
