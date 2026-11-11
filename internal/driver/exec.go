package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"deployhub/internal/shell"
)

// Executor 是部署方式的执行部分。引擎按 Prepare → Build → Upload → Activate → 健康检查 → Cleanup 调用，
// 健康检查失败时用 Activate 切回上一个版本。
type Executor interface {
	Planner
	// Prepare 准备服务器：目录、配置文件、前置条件、服务。在构建前执行，服务器没准备好就不浪费构建时间。
	Prepare(x *Exec) error
	// Build 本地构建，产物放在 x.WorkDir。
	Build(x *Exec) error
	// Upload 把产物传到服务器。
	Upload(x *Exec) error
	// Activate 切换到 version 并让它生效，部署和回滚共用。
	Activate(x *Exec, version string) error
	// HealthScript 返回在服务器上执行的健康检查脚本，退出码 0 为通过。
	HealthScript(x *Exec) string
	// State 读取线上状态（只读）。
	State(x *Exec) (*OnlineState, error)
	// Cleanup 清理旧版本，当前版本永远不删。
	Cleanup(x *Exec) error
	// LogFile 是服务器上的部署记录文件；为空表示不记录（没有服务器，部署记录只在本地）。
	LogFile(x *Exec) string
}

// HealthChecker 是可选接口：健康检查不在服务器上执行时（如 Workers 在本机请求），由部署方式自己做，
// 引擎不再调用 HealthScript。返回的错误直接作为失败原因。
type HealthChecker interface {
	Health(x *Exec) error
}

// OnlineState 是线上状态。
type OnlineState struct {
	Current  string   `json:"current"`  // 当前版本
	Releases []string `json:"releases"` // 服务器上保留的版本，新的在前
	Last     string   `json:"last"`     // 最后一条部署记录
}

// Exec 是执行时的上下文：参数（${名称} 已替换）和执行命令的方法。由引擎提供。
type Exec struct {
	Ctx      context.Context
	C        *PlanContext
	WorkDir  string // 本地构建目录，这个版本独占，开始时是空的
	TmpDir   string // 本地临时目录，放待上传的配置文件，用完即删（权限 700）
	CacheDir string // 本地缓存目录，这个应用的各次部署共用（如静态站点的构建副本和依赖）
	Commit   string // 完整的 commit 哈希
	Rsync    bool   // 本机和服务器都有 rsync
	// Activated 是最近一次切换到的版本，由部署方式在 Activate 里设置，健康检查据此确认线上版本（自动回滚后是上一个版本）。
	Activated string

	Local     func(cmd shell.Cmd) error                    // 执行本地命令，输出进日志
	Remote    func(script string) error                    // 在服务器上执行，输出进日志
	Query     func(script string) (string, error)          // 在服务器上执行，只取输出，不进日志
	CopyFiles func(local []string, remoteDir string) error // scp 上传
	RsyncDir  func(localDir, remoteDir string, extra []string) error
	// Pipe 把本地命令的标准输出压缩（gzip）后，作为服务器上 remoteCmd 的标准输入，
	// 如 docker save | gzip | ssh 'gunzip | docker load'。
	Pipe func(local shell.Cmd, remoteCmd string) error
	Info func(format string, a ...any)
	Warn func(format string, a ...any)
}

// UploadDir 把本地目录的内容传到服务器目录（需已存在）：有 rsync 用 rsync，否则用 scp。
func (x *Exec) UploadDir(localDir, remoteDir string, rsyncExtra ...string) error {
	if x.Rsync {
		return x.RsyncDir(localDir, remoteDir, rsyncExtra)
	}
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = filepath.Join(localDir, e.Name())
	}
	return x.CopyFiles(paths, remoteDir)
}

// ---------- releases/ + current 目录结构的通用操作（Go 服务、静态站点共用）----------

// EnsureDirs 创建服务器目录；部署用户没有权限时用免密 sudo 创建，并把目录交给部署用户。
func EnsureDirs(x *Exec, dirs ...string) error {
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = Q(d)
	}
	list := strings.Join(quoted, " ")
	return x.Remote(fmt.Sprintf(`set -e
for d in %s; do
  if mkdir -p "$d" 2>/dev/null && [ -w "$d" ]; then continue; fi
  if sudo -n true 2>/dev/null; then
    sudo -n mkdir -p "$d" && sudo -n chown "$(id -un):" "$d"
  else
    echo "目录 $d 不可写，也没有免密 sudo：请在服务器上执行 sudo mkdir -p $d && sudo chown $(id -un) $d" >&2
    exit 1
  fi
done`, list))
}

// UploadConfigFiles 上传「在平台里填写」的配置文件到 dir：先传到临时文件，权限 600 后再改名，进程不会读到写了一半的文件。
// 「以服务器为准」的只检查存在。内容不出现在命令行和日志里。
func UploadConfigFiles(x *Exec, dir string) error {
	for _, f := range x.C.Records("config_files") {
		name, _ := f["name"].(string)
		if src, _ := f["source"].(string); src == "server" {
			if err := x.Remote(fmt.Sprintf("test -f %s || { echo '缺少 %s/%s（来源设为以服务器为准，需要先在服务器上放好）' >&2; exit 1; }", Q(dir+"/"+name), dir, name)); err != nil {
				return err
			}
			continue
		}
		content, _ := f["content"].(string)
		local := filepath.Join(x.TmpDir, name+".uploading")
		if err := os.WriteFile(local, []byte(content), 0o600); err != nil {
			return err
		}
		x.Info("上传配置文件 %s → %s/%s", name, dir, name)
		if err := x.CopyFiles([]string{local}, dir); err != nil {
			return err
		}
		os.Remove(local)
		target := Q(dir + "/" + name)
		if err := x.Remote(fmt.Sprintf("chmod 600 %s.uploading && mv -f %s.uploading %s", target, target, target)); err != nil {
			return err
		}
	}
	return nil
}

// CheckRequired 检查必需文件和必需配置项，不满足时报错（在构建前停止）。
func CheckRequired(x *Exec, dir string) error {
	var b strings.Builder
	b.WriteString("fail=0\n")
	for _, f := range x.C.List("required_files") {
		fmt.Fprintf(&b, "[ -e %s ] || { echo '缺少必需文件 %s/%s' >&2; fail=1; }\n", Q(dir+"/"+f), dir, f)
	}
	for _, rc := range x.C.Records("required_config") {
		file, _ := rc["file"].(string)
		key, _ := rc["key"].(string)
		want, _ := rc["value"].(string)
		fmt.Fprintf(&b, "%s || { echo %s >&2; fail=1; }\n", TomlCheck(dir+"/"+file, key, want), Q(fmt.Sprintf("%s/%s 里需要配置 %s = %s", dir, file, key, want)))
	}
	if b.Len() == len("fail=0\n") {
		return nil
	}
	b.WriteString("exit $fail\n")
	return x.Remote(b.String())
}

// ReleaseState 读取 releases/ + current 结构的线上状态。
func ReleaseState(x *Exec, dir string) (*OnlineState, error) {
	out, err := x.Query(fmt.Sprintf(`cd %s 2>/dev/null || exit 0
echo "current=$(basename "$(readlink current 2>/dev/null)" 2>/dev/null)"
ls -1t releases 2>/dev/null | while read -r r; do echo "release=$r"; done
echo "last=$(grep -v '^$' deploy.log 2>/dev/null | tail -n 1)"`, Q(dir)))
	if err != nil {
		return nil, err
	}
	return ParseState(out), nil
}

// ParseState 解析 current= / release= / last= 格式的输出。
func ParseState(out string) *OnlineState {
	st := &OnlineState{Releases: []string{}}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "current":
			if v != "." && v != "current" {
				st.Current = v
			}
		case "release":
			if v != "" {
				st.Releases = append(st.Releases, v)
			}
		case "last":
			st.Last = v
		}
	}
	return st
}

// SwitchCurrent 原子切换 current 软链接到 releases/<version>（先建临时链接再 mv -T 覆盖）。check 是版本目录里必须存在的文件。
func SwitchCurrent(x *Exec, dir, version, check string) error {
	return x.Remote(fmt.Sprintf(`cd %s && test -e %s || { echo '服务器上没有版本 %s' >&2; exit 1; }
ln -sfn %s current.tmp && mv -T current.tmp current`,
		Q(dir), Q("releases/"+version+"/"+check), version, Q("releases/"+version)))
}

// CleanupReleases 只保留最近 keep 个版本，当前版本永远不删。
func CleanupReleases(x *Exec, dir string, keep int) error {
	if keep < 1 {
		keep = 1
	}
	return x.Remote(fmt.Sprintf(`cd %s/releases || exit 0
cur=$(basename "$(readlink ../current)")
ls -1t | tail -n +%d | while read -r r; do
  [ "$r" = "$cur" ] || { rm -rf -- "$r" && echo "删除旧版本 $r"; }
done`, Q(dir), keep+1))
}
