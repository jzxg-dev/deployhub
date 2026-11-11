// Package detect 识别本机仓库的内容和服务器的环境，用于给参数预填推荐值和检查。
package detect

import (
	"context"
	"strings"

	"deployhub/internal/model"
	"deployhub/internal/remote"
)

// serverScript 在服务器上收集环境信息，每行 key=value。只读，不改动服务器。
const serverScript = `echo "machine=$(uname -m)"
echo "user=$(id -un)"
if [ -r /etc/os-release ]; then (. /etc/os-release; echo "os=${PRETTY_NAME:-$NAME}"); else echo "os=$(uname -s)"; fi
if command -v docker >/dev/null 2>&1; then
  v=$(docker version --format '{{.Server.Version}}' 2>/dev/null) && echo "docker=$v"
  docker compose version >/dev/null 2>&1 && echo "compose=1"
fi
command -v rsync >/dev/null 2>&1 && echo "rsync=1"
command -v systemctl >/dev/null 2>&1 && echo "systemd=1"
sudo -n true >/dev/null 2>&1 && echo "sudo=1"
exit 0
`

// Server 连接服务器并检测环境。
func Server(ctx context.Context, r remote.Runner, t remote.Target) (*model.ServerFacts, error) {
	res, err := r.Run(ctx, t, serverScript)
	if err != nil {
		return nil, err
	}
	return ParseServerFacts(res.Stdout), nil
}

// ParseServerFacts 解析 serverScript 的输出。
func ParseServerFacts(out string) *model.ServerFacts {
	f := &model.ServerFacts{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "machine":
			f.Machine = v
			f.Arch = GoArch(v)
		case "user":
			f.User = v
		case "os":
			f.OS = v
		case "docker":
			f.Docker = v != ""
			f.DockerVersion = v
		case "compose":
			f.Compose = true
		case "rsync":
			f.Rsync = true
		case "systemd":
			f.Systemd = true
		case "sudo":
			f.SudoNoPassword = true
		}
	}
	return f
}

// GoArch 把 uname -m 的结果换成 Go 的架构名，认不出时返回空。
func GoArch(machine string) string {
	switch strings.ToLower(machine) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64", "armv8l":
		return "arm64"
	}
	return ""
}
