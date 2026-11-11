package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"deployhub/internal/detect"
	"deployhub/internal/driver"
	"deployhub/internal/remote"
)

// 应用的代码有两种来源（参数 repo_source）：
//   - 本机目录：运行 deployhub 的机器上已有的仓库，平台只读取和按同步方式快进。
//   - git 地址：平台自己克隆到账号数据目录的 repos/<项目>/<应用>，每个应用一份；
//     同步时拉取部署分支并把工作区切到它的最新提交（工作区是平台的，不保留本地改动）。

// RepoSync 说明读取应用仓库时要不要同步平台克隆的仓库。对本机目录没有影响。
type RepoSync int

const (
	// RepoNoSync 不联网，只看已经克隆下来的内容（还没克隆过时识别结果带错误）。
	RepoNoSync RepoSync = iota
	// RepoSyncIdle 拉取最新提交；应用正在部署时不动工作区，只看当前内容。用于检查、部署确认、总览状态。
	RepoSyncIdle
	// RepoSyncJob 拉取最新提交，由部署任务自己调用。
	RepoSyncJob
)

// ManagedRepo 判断应用的代码来源是不是 git 地址（平台克隆）。
func ManagedRepo(params map[string]any) bool {
	return params["repo_source"] == driver.RepoSourceGit
}

// RepoDir 返回应用代码在本机的目录：本机目录来源是参数里的路径（展开 ~ 后的绝对路径），git 地址来源是平台克隆的位置。
func (s *Service) RepoDir(project, app string, params map[string]any) string {
	if ManagedRepo(params) {
		return filepath.Join(s.dataDir, "repos", project, app)
	}
	p, _ := params["repo_path"].(string)
	if p = strings.TrimSpace(p); p == "" {
		return ""
	}
	if abs, err := filepath.Abs(remote.ExpandHome(p)); err == nil {
		return abs
	}
	return p
}

// AppRepo 返回应用仓库的识别结果。git 地址来源的按 mode 先同步；同步失败时结果的 Error 不为空。
// logf 不为 nil 时收到同步过程的说明（warn 为 true 是提醒）。
func (s *Service) AppRepo(ctx context.Context, project, app string, params map[string]any, mode RepoSync, logf func(warn bool, msg string)) *detect.Repo {
	dir := s.RepoDir(project, app, params)
	if !ManagedRepo(params) {
		return s.repos.Detect(ctx, dir)
	}
	if logf == nil {
		logf = func(bool, string) {}
	}
	if err := s.syncManagedRepo(ctx, project, app, dir, params, mode, logf); err != nil {
		return &detect.Repo{Path: dir, Error: err.Error(), Options: map[string][]driver.Option{}, Hints: map[string]string{}, Notes: []string{}}
	}
	repo := s.repos.Detect(ctx, dir)
	// 工作区是平台的：构建留下的文件不算「未提交的改动」
	repo.Dirty = 0
	return repo
}

// AppRepoInfo 返回已保存应用的仓库识别结果，给应用页显示用：不联网，git 地址来源的只看已经拉取下来的内容。
func (s *Service) AppRepoInfo(ctx context.Context, project, app string) (*detect.Repo, error) {
	a, err := s.getApp(ctx, project, app)
	if err != nil {
		return nil, err
	}
	return s.AppRepo(ctx, project, app, a.Params, RepoNoSync, nil), nil
}

func (s *Service) lockRepo(dir string) *sync.Mutex {
	s.repoMu.Lock()
	defer s.repoMu.Unlock()
	mu := s.repoLock[dir]
	if mu == nil {
		mu = &sync.Mutex{}
		s.repoLock[dir] = mu
	}
	return mu
}

func hasGitDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && st.IsDir()
}

// syncManagedRepo 克隆或更新平台克隆的仓库：拉取部署分支，把工作区切到它的最新提交。
func (s *Service) syncManagedRepo(ctx context.Context, project, app, dir string, params map[string]any, mode RepoSync, logf func(bool, string)) error {
	cloned := hasGitDir(dir)
	if mode == RepoNoSync {
		if !cloned {
			return errors.New("代码还没有拉取过，第一次检查或部署时会从 git 地址克隆")
		}
		return nil
	}
	// 同一个仓库目录同时只有一处在操作
	mu := s.lockRepo(dir)
	mu.Lock()
	defer mu.Unlock()
	cloned = hasGitDir(dir)
	if mode == RepoSyncIdle && cloned && s.repoBusy != nil && s.repoBusy(project, app) {
		return nil // 正在部署：构建可能正在用工作区，不动它
	}

	branch, _ := params["branch"].(string)
	urlTemplate, _ := params["repo_url"].(string)
	if urlTemplate == "" {
		return errors.New("还没有填 git 地址")
	}
	if branch == "" || strings.HasPrefix(branch, "-") {
		return errors.New("部署分支无效")
	}
	url, secrets, err := s.expandRepoURL(ctx, urlTemplate)
	if err != nil {
		return err
	}
	// git 的报错里可能带着地址（含令牌）：把令牌换成 ***，把展开后的地址换回填写的样子
	mask := func(text string) string {
		text = strings.ReplaceAll(text, url, urlTemplate)
		for _, v := range secrets {
			if len(v) >= 4 {
				text = strings.ReplaceAll(text, v, "***")
			}
		}
		return text
	}

	sshCmd := "ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=accept-new"
	if name, _ := params["repo_credential"].(string); name != "" {
		keyFile, err := s.credentialKeyFile(ctx, name)
		if err != nil {
			return err
		}
		sshCmd += " -o IdentitiesOnly=yes -i " + remote.Quote(keyFile)
	}
	// 地址通过环境变量传给 git（当作 remote.origin.url），不写进 .git/config，也不出现在命令行里：地址里可能有令牌
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND="+sshCmd,
		"GIT_ALLOW_PROTOCOL="+s.gitProtocols,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=remote.origin.url",
		"GIT_CONFIG_VALUE_0="+url,
	)
	run := func(timeout time.Duration, args ...string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(cctx, "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		text := mask(strings.TrimSpace(string(out)))
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return "", errors.New("本机找不到 git 命令")
			}
			if cctx.Err() != nil && ctx.Err() == nil {
				return text, fmt.Errorf("超过 %d 分钟没有完成", int(timeout.Minutes()))
			}
			return text, errors.New(explainGit(text))
		}
		return text, nil
	}

	if !cloned {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建仓库目录：%w", err)
		}
		if _, err := run(time.Minute, "init", "--quiet"); err != nil {
			return fmt.Errorf("初始化仓库目录：%w", err)
		}
		logf(false, fmt.Sprintf("第一次从 %s 克隆 %s 分支", urlTemplate, branch))
	}
	ref := "refs/remotes/origin/" + branch
	if _, err := run(15*time.Minute, "fetch", "--quiet", "--force", "--prune", "origin", "+refs/heads/"+branch+":"+ref); err != nil {
		if !cloned {
			// 第一次就失败：不留下空仓库，下次重新开始
			_ = os.RemoveAll(dir)
		}
		return fmt.Errorf("从 %s 拉取 %s 分支失败：%w", urlTemplate, branch, err)
	}
	before, _ := run(time.Minute, "rev-parse", "--quiet", "--verify", "HEAD")
	if _, err := run(5*time.Minute, "checkout", "--quiet", "--force", "-B", branch, ref); err != nil {
		return fmt.Errorf("切换到 %s 分支的最新提交失败：%w", branch, err)
	}
	after, _ := run(time.Minute, "log", "-1", "--format=%h %s")
	head, _ := run(time.Minute, "rev-parse", "HEAD")
	switch {
	case before == "":
		logf(false, "已克隆，最新提交 "+after)
	case before != head:
		logf(false, "已拉取到最新提交 "+after)
	default:
		logf(false, "远程没有新提交，当前 "+after)
	}
	return nil
}

// expandRepoURL 把 git 地址里的 ${名称} 换成密钥的值，返回展开后的地址和用到的密钥值。
func (s *Service) expandRepoURL(ctx context.Context, url string) (string, []string, error) {
	fields := []driver.Field{{Key: "repo_url", Type: driver.TypeText}}
	values := map[string]any{"repo_url": url}
	names := driver.RefNames(driver.Refs(fields, values))
	if len(names) == 0 {
		return url, nil, nil
	}
	secrets, err := s.SecretValues(ctx, names)
	if err != nil {
		return "", nil, err
	}
	out, err := driver.Expand(fields, values, func(n string) (string, bool) { v, ok := secrets[n]; return v, ok })
	if err != nil {
		return "", nil, err
	}
	var used []string
	for _, v := range secrets {
		used = append(used, v)
	}
	expanded, _ := out["repo_url"].(string)
	return expanded, used, nil
}

// explainGit 把常见的 git 错误换成中文说明，认不出的返回原文的最后一行。
func explainGit(output string) string {
	s := strings.ToLower(output)
	switch {
	case strings.Contains(s, "permission denied (publickey") || strings.Contains(s, "permission denied, please try again"):
		return "没有权限：检查选的 SSH 私钥，对应的公钥是否已加到仓库的部署密钥里"
	case strings.Contains(s, "authentication failed") || strings.Contains(s, "could not read username") || strings.Contains(s, "terminal prompts disabled"):
		return "认证失败：私有仓库用 HTTPS 地址时要在地址里带上令牌，或改用 SSH 地址和 SSH 私钥"
	case strings.Contains(s, "repository not found") || strings.Contains(s, "does not appear to be a git repository"):
		return "找不到仓库：检查 git 地址，私有仓库还要确认有访问权限"
	case strings.Contains(s, "couldn't find remote ref") || strings.Contains(s, "could not find remote ref"):
		return "远程仓库没有这个分支"
	case strings.Contains(s, "could not resolve host"):
		return "域名解析失败，检查 git 地址和网络"
	case strings.Contains(s, "host key verification failed"):
		return "git 服务器的主机指纹和之前记录的不一致，请在运行 deployhub 的机器上核实 ~/.ssh/known_hosts"
	case strings.Contains(s, "connection timed out") || strings.Contains(s, "operation timed out") || strings.Contains(s, "connection refused"):
		return "连不上 git 服务器，检查地址和网络"
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return "git 命令失败"
}

// removeManagedRepo 删除应用的平台克隆（应用删除时）。
func (s *Service) removeManagedRepo(project, app string) {
	dir := filepath.Join(s.dataDir, "repos", project, app)
	mu := s.lockRepo(dir)
	mu.Lock()
	defer mu.Unlock()
	_ = os.RemoveAll(dir)
}
