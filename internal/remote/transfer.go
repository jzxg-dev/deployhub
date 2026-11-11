package remote

import (
	"context"
	"errors"
	"io"
	"strings"

	"deployhub/internal/shell"
)

// Transport 是部署执行需要的远程操作：执行脚本（实时输出）、上传文件。
type Transport interface {
	Runner
	// Stream 在服务器上执行脚本，每行输出调用一次 onLine。退出码非 0 返回 *shell.Error，连不上返回 *ConnectError。
	Stream(ctx context.Context, t Target, script string, onLine func(string)) error
	// CopyFiles 用 scp 把本地文件或目录（递归）上传到服务器目录 remoteDir（需已存在）。
	CopyFiles(ctx context.Context, t Target, local []string, remoteDir string, onLine func(string)) error
	// Rsync 用 rsync 把本地目录的内容同步到服务器目录 remoteDir，服务器上也要有 rsync。
	Rsync(ctx context.Context, t Target, localDir, remoteDir string, extra []string, onLine func(string)) error
	// RunStdin 在服务器上执行 command，stdin 作为它的标准输入（用于传输镜像这类大数据流）。
	RunStdin(ctx context.Context, t Target, command string, stdin io.Reader, onLine func(string)) error
}

var _ Transport = (*SSH)(nil)

// Stream 通过标准输入把脚本交给服务器上的 sh -s 执行，按行转发输出。
func (s *SSH) Stream(ctx context.Context, t Target, script string, onLine func(string)) error {
	err := shell.Run(ctx, shell.Cmd{
		Name: "ssh", Args: append(s.Args(t), "sh -s"), Stdin: strings.NewReader(script),
		Display: "ssh " + t.Label(),
	}, onLine)
	return s.wrap(t, err)
}

// CopyFiles 用 scp 上传。scp 的端口参数是 -P，其余选项和 ssh 相同。
func (s *SSH) CopyFiles(ctx context.Context, t Target, local []string, remoteDir string, onLine func(string)) error {
	opts := s.Options(t)
	for i, o := range opts {
		if o == "-p" {
			opts[i] = "-P"
		}
	}
	args := append([]string{"-q", "-r"}, opts...)
	args = append(args, "--")
	args = append(args, local...)
	args = append(args, Dest(t)+":"+remoteDir+"/")
	return s.wrap(t, shell.Run(ctx, shell.Cmd{Name: "scp", Args: args, Display: "scp → " + t.Label() + ":" + remoteDir}, onLine))
}

// Rsync 用 rsync 同步，-e 里用同样的 ssh 选项（共享复用连接）。
func (s *SSH) Rsync(ctx context.Context, t Target, localDir, remoteDir string, extra []string, onLine func(string)) error {
	sshCmd := "ssh"
	for _, o := range s.Options(t) {
		sshCmd += " " + Quote(o)
	}
	args := append([]string{"-az", "--partial"}, extra...)
	args = append(args, "-e", sshCmd, strings.TrimRight(localDir, "/")+"/", Dest(t)+":"+remoteDir+"/")
	return s.wrap(t, shell.Run(ctx, shell.Cmd{Name: "rsync", Args: args, Display: "rsync → " + t.Label() + ":" + remoteDir}, onLine))
}

// RunStdin 执行 command，标准输入来自 stdin。
func (s *SSH) RunStdin(ctx context.Context, t Target, command string, stdin io.Reader, onLine func(string)) error {
	return s.wrap(t, shell.Run(ctx, shell.Cmd{Name: "ssh", Args: append(s.Args(t), command), Stdin: stdin, Display: "ssh " + t.Label() + " " + command}, onLine))
}

// wrap 把 ssh 退出码 255（连接失败）换成 ConnectError。
func (s *SSH) wrap(t Target, err error) error {
	var se *shell.Error
	if errors.As(err, &se) && se.ExitCode == 255 {
		detail := strings.Join(se.Tail, "\n")
		return &ConnectError{Target: t, Reason: explain(detail), Detail: detail}
	}
	return err
}
