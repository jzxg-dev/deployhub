package engine

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// repoState 是一个仓库在本次任务里的同步结果。同一个仓库只同步一次。
type repoState struct {
	branch string
	commit string // 完整哈希
	err    error
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("git %s：%s", strings.Join(args, " "), s)
	}
	return s, nil
}

// syncRepo 按同步方式更新代码。以本地为准：先 fetch，只有本地单纯落后远程时才快进；
// 领先或分叉时直接用本地代码；fetch 失败也继续用本地代码。
func syncRepo(ctx context.Context, dir, mode string, info, warn func(string, ...any)) (string, error) {
	if mode != "offline" {
		if _, err := git(ctx, dir, "fetch", "--quiet"); err != nil {
			warn("git fetch 失败，使用本地代码：%v", err)
		} else if _, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "@{u}"); err != nil {
			info("当前分支没有上游分支，使用本地代码")
		} else {
			counts, err := git(ctx, dir, "rev-list", "--left-right", "--count", "HEAD...@{u}")
			if err == nil {
				f := strings.Fields(counts)
				ahead, _ := strconv.Atoi(f[0])
				behind, _ := strconv.Atoi(f[1])
				switch {
				case ahead > 0 && behind > 0:
					warn("本地和远程分叉（本地领先 %d、落后 %d 个提交），使用本地代码", ahead, behind)
				case ahead > 0:
					info("本地领先远程 %d 个提交，使用本地代码", ahead)
				case behind > 0:
					if _, err := git(ctx, dir, "merge", "--ff-only", "--quiet", "@{u}"); err != nil {
						return "", fmt.Errorf("本地落后远程 %d 个提交，快进失败：%w", behind, err)
					}
					info("本地落后远程 %d 个提交，已快进到远程最新", behind)
				default:
					info("本地和远程一致")
				}
			}
		}
	} else {
		info("同步方式为不联系远程，直接用本地代码")
	}
	return git(ctx, dir, "rev-parse", "HEAD")
}
