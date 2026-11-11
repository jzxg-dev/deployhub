package gobinary

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"deployhub/internal/driver"
	"deployhub/internal/shell"
)

var _ driver.Executor = Driver{}

// Prepare 准备服务器：目录、配置文件、前置条件、systemd 服务（不存在才安装，已有的不覆盖）。
func (d Driver) Prepare(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	if err := driver.EnsureDirs(x, cfg.RemoteDir, cfg.RemoteDir+"/releases", cfg.WorkingDir); err != nil {
		return err
	}
	if err := driver.UploadConfigFiles(x, cfg.WorkingDir); err != nil {
		return err
	}
	if err := driver.CheckRequired(x, cfg.WorkingDir); err != nil {
		return err
	}
	if cfg.Template == "" {
		return x.Remote(fmt.Sprintf("systemctl cat %s >/dev/null 2>&1 || { echo '服务 %s 不存在，且设置为不自动安装：请先在服务器上手动安装' >&2; exit 1; }",
			driver.Q(cfg.Service), cfg.Service))
	}
	path := "/etc/systemd/system/" + cfg.Service + ".service"
	return x.Remote(fmt.Sprintf(`if systemctl cat %s >/dev/null 2>&1; then
  echo '服务 %s 已存在，不覆盖'
else
  printf '%%s\n' %s | sudo -n tee %s >/dev/null && sudo -n systemctl daemon-reload && sudo -n systemctl enable %s \
    && echo '已安装并启用服务 %s' \
    || { echo '安装服务 %s 失败：需要免密 sudo，或在服务器上手动安装 %s' >&2; exit 1; }
fi`, driver.Q(cfg.Service), cfg.Service, driver.Q(cfg.RenderService()), driver.Q(path), driver.Q(cfg.Service), cfg.Service, cfg.Service, path))
}

// Build 本地交叉编译（或执行自定义构建命令），产物和 SHA256SUMS 放在 WorkDir。
func (d Driver) Build(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	out := filepath.Join(x.WorkDir, cfg.BinaryName)
	short := x.Commit
	if len(short) > 7 {
		short = short[:7]
	}
	placeholders := strings.NewReplacer("{version}", x.C.Version, "{commit}", short, "{app}", x.C.App)

	if cfg.BuildCommand != "" {
		if err := x.Local(shell.Cmd{Name: "sh", Args: []string{"-c", cfg.BuildCommand}, Dir: x.C.Repo.Path, Display: cfg.BuildCommand}); err != nil {
			return err
		}
		if err := copyFile(filepath.Join(x.C.Repo.Path, cfg.ArtifactPath), out); err != nil {
			return fmt.Errorf("复制产物 %s：%w", cfg.ArtifactPath, err)
		}
	} else if cfg.Builder == BuilderDocker {
		// 缓存目录要先建好，否则 docker 会用 root 创建，容器里的普通用户写不进去
		for _, d := range []string{"gomod", "gobuild"} {
			if err := os.MkdirAll(filepath.Join(x.CacheDir, d), 0o755); err != nil {
				return fmt.Errorf("创建编译缓存目录：%w", err)
			}
		}
		args := cfg.dockerBuild(x.C.Repo.Path, x.WorkDir, x.CacheDir, placeholders.Replace)
		if err := x.Local(shell.Cmd{Name: "docker", Args: args, Display: "docker " + strings.Join(quoteAll(args), " ")}); err != nil {
			return err
		}
	} else {
		env, args := cfg.goBuild(out, placeholders.Replace)
		if err := x.Local(shell.Cmd{Name: "go", Args: args, Dir: x.C.Repo.Path, Env: env,
			Display: strings.Join(env, " ") + " go " + strings.Join(quoteAll(args), " ")}); err != nil {
			return err
		}
	}

	sum, err := sha256File(out)
	if err != nil {
		return fmt.Errorf("计算产物校验值：%w", err)
	}
	st, _ := os.Stat(out)
	x.Info("产物 %s，%.1f MB，sha256 %s", cfg.BinaryName, float64(st.Size())/(1<<20), sum[:12])
	return os.WriteFile(filepath.Join(x.WorkDir, "SHA256SUMS"), []byte(sum+"  "+cfg.BinaryName+"\n"), 0o644)
}

// Upload 上传到 releases/<版本>，在服务器上校验 sha256。
func (d Driver) Upload(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	rel := cfg.RemoteDir + "/releases/" + x.C.Version
	if err := x.Remote("mkdir -p " + driver.Q(rel)); err != nil {
		return err
	}
	if err := x.UploadDir(x.WorkDir, rel); err != nil {
		return err
	}
	return x.Remote(fmt.Sprintf("cd %s && sha256sum -c SHA256SUMS && chmod +x %s", driver.Q(rel), driver.Q(cfg.BinaryName)))
}

// Activate 切换 current 并重启服务。
func (d Driver) Activate(x *driver.Exec, version string) error {
	cfg, _ := d.Resolve(x.C)
	if err := driver.SwitchCurrent(x, cfg.RemoteDir, version, cfg.BinaryName); err != nil {
		return err
	}
	return x.Remote(cfg.Restart)
}

// HealthScript 刚重启时进程可能还没崩溃，先等 3 秒再检查。
func (d Driver) HealthScript(x *driver.Exec) string {
	cfg, _ := d.Resolve(x.C)
	return "sleep 3\n" + driver.HealthCommand(x.C, "systemctl is-active --quiet "+driver.Q(cfg.Service))
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

// LogFile 是 <部署目录>/deploy.log，和旧工具一致。
func (d Driver) LogFile(x *driver.Exec) string {
	cfg, _ := d.Resolve(x.C)
	return cfg.RemoteDir + "/deploy.log"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = driver.Q(a)
	}
	return out
}
