package static

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"deployhub/internal/driver"
	"deployhub/internal/shell"
)

var _ driver.Executor = Driver{}

// Prepare 创建站点目录（需要部署用户可写），上传配置文件，检查前置条件。
func (d Driver) Prepare(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	if err := driver.EnsureDirs(x, cfg.RemoteDir, cfg.RemoteDir+"/releases"); err != nil {
		return err
	}
	if err := driver.UploadConfigFiles(x, cfg.RemoteDir); err != nil {
		return err
	}
	return driver.CheckRequired(x, cfg.RemoteDir)
}

// Build 在仓库工作区的副本里构建（包含未提交的改动，不动仓库自己的 node_modules），
// 再把产物（去掉不上传的文件）复制到 WorkDir。副本放在 CacheDir，保留到下次，依赖不用每次重装。
func (d Driver) Build(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	if cfg.BuildCommand == "" {
		return errors.New("没有构建命令：仓库里没识别出来，请在应用配置里填写")
	}
	work := filepath.Join(x.CacheDir, "src")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	dist := strings.Trim(cfg.Dist, "/")
	if err := x.Local(shell.Cmd{Name: "rsync", Args: []string{
		"-a", "--delete", "--exclude=/.git", "--exclude=/node_modules", "--exclude=/" + dist,
		strings.TrimRight(x.C.Repo.Path, "/") + "/", work + "/",
	}, Display: fmt.Sprintf("rsync 仓库工作区 → 构建副本 %s（不含 .git、node_modules、%s）", work, dist)}); err != nil {
		return err
	}
	// 构建副本是新路径，mise 要求先信任其中的 mise.toml，否则 mise exec 会报错
	for _, name := range []string{"mise.toml", ".mise.toml"} {
		if _, err := os.Stat(filepath.Join(work, name)); err == nil {
			if err := x.Local(shell.Cmd{Name: "mise", Args: []string{"trust", "--quiet", filepath.Join(work, name)}}); err != nil {
				return err
			}
		}
	}
	// CI=true：pnpm 等工具不再等终端确认；值为空的环境变量不设置，用仓库里的 .env.production
	env := []string{"CI=true"}
	for _, kv := range cfg.BuildEnv {
		if kv.Value != "" {
			env = append(env, kv.Key+"="+kv.Value)
		}
	}
	display := strings.Join(env, " ") + " " + cfg.BuildCommand
	if err := x.Local(shell.Cmd{Name: "sh", Args: []string{"-c", cfg.BuildCommand}, Dir: work, Env: env, Display: display}); err != nil {
		return err
	}
	src := filepath.Join(work, dist)
	if _, err := os.Stat(filepath.Join(src, "index.html")); err != nil {
		return fmt.Errorf("构建产物里没有 index.html：%s（产物目录是不是 %s？）", src, dist)
	}
	n, size, err := copyTree(src, x.WorkDir, cfg.Exclude)
	if err != nil {
		return fmt.Errorf("复制产物：%w", err)
	}
	x.Info("产物 %d 个文件，%.1f MB（跳过 %s）", n, float64(size)/(1<<20), strings.Join(orNone(cfg.Exclude), "、"))
	return nil
}

func orNone(list []string) []string {
	if len(list) == 0 {
		return []string{"无"}
	}
	return list
}

// copyTree 复制目录，跳过文件名匹配 exclude 的文件，返回文件数和总大小。
// 目录固定 755、文件固定 644，不随进程的 umask 变：deployhub 作为服务运行时 umask 常是 077，
// 产物会变成 700/600，上传后 Nginx 读不到。
func copyTree(src, dst string, exclude []string) (int, int64, error) {
	var n int
	var size int64
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			return os.Chmod(target, 0o755)
		}
		for _, pat := range exclude {
			if ok, _ := filepath.Match(pat, d.Name()); ok {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Chmod(0o644); err != nil {
			out.Close()
			return err
		}
		n++
		size += info.Size()
		return out.Close()
	})
	return n, size, err
}

// Upload 上传到 releases/<版本>。
//
// 有 rsync 时用 --link-dest 以当前版本为参照：内容和属性都相同的文件在服务器上建硬链接（不占空间、不重传），
// 其余的新建文件。不能先 cp -al 复制当前版本再 rsync：内容相同的文件 rsync 只改属性，
// 而硬链接共用 inode，会把当前和更早版本的同名文件一起改掉（产物权限不对时，线上版本也跟着坏）。
// --no-times 不同步修改时间，否则每次构建时间不同，没有文件能链接上。
// 没有 rsync 时整个目录用 scp 上传。
//
// 上传后把新版本目录统一设为所有人可读（Nginx 等通常不是部署用户）。不用 rsync --chmod：
// macOS 自带的 openrsync 接受这个参数但不生效。链接上的文件和当前版本属性相同，chmod 不会改到它。
func (d Driver) Upload(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	q := driver.Q
	rel := cfg.RemoteDir + "/releases/" + x.C.Version
	if err := x.Remote("mkdir -p " + q(rel)); err != nil {
		return err
	}
	var extra []string
	if x.Rsync {
		extra = []string{"--checksum", "--no-times", "--delete"} // --delete：同一版本重传时清掉多余文件
		// 首次部署没有 current，rsync 会提示参照目录不存在，这里先判断
		if out, err := x.Query(fmt.Sprintf("[ -d %s ] && echo yes || true", q(cfg.RemoteDir+"/current/"))); err == nil && strings.TrimSpace(out) == "yes" {
			extra = append(extra, "--link-dest="+cfg.RemoteDir+"/current/")
		}
	}
	if err := x.UploadDir(x.WorkDir, rel, extra...); err != nil {
		return err
	}
	return x.Remote(fmt.Sprintf("chmod -R u+rwX,go+rX %s && { test -f %s || { echo '上传后 %s 里没有 index.html' >&2; exit 1; }; }",
		q(rel), q(rel+"/index.html"), rel))
}

// Activate 切换 current，Nginx 下一个请求就读新版本，不用重启。
func (d Driver) Activate(x *driver.Exec, version string) error {
	cfg, _ := d.Resolve(x.C)
	return driver.SwitchCurrent(x, cfg.RemoteDir, version, "index.html")
}

// HealthScript 静态文件没有进程，不用等。
func (d Driver) HealthScript(x *driver.Exec) string {
	cfg, _ := d.Resolve(x.C)
	return driver.HealthCommand(x.C, cfg.DefaultCheck())
}

// State 读取线上版本。
func (d Driver) State(x *driver.Exec) (*driver.OnlineState, error) {
	cfg, _ := d.Resolve(x.C)
	return driver.ReleaseState(x, cfg.RemoteDir)
}

// Cleanup 清理旧版本。
func (d Driver) Cleanup(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	return driver.CleanupReleases(x, cfg.RemoteDir, cfg.KeepReleases)
}

// LogFile 是 <站点目录>/deploy.log。
func (d Driver) LogFile(x *driver.Exec) string {
	cfg, _ := d.Resolve(x.C)
	return cfg.RemoteDir + "/deploy.log"
}
