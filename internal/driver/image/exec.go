package image

import (
	"fmt"
	"strings"

	"deployhub/internal/driver"
	"deployhub/internal/shell"
)

var _ driver.Executor = Driver{}

// ref 是镜像引用 <镜像名>:<版本>。
func (cfg *Config) ref(version string) string { return cfg.imageName() + ":" + version }

func (cfg *Config) anyComposeFile() string {
	conds := make([]string, len(ComposeFiles))
	for i, f := range ComposeFiles {
		conds[i] = "[ -e " + f + " ]"
	}
	return strings.Join(conds, " || ")
}

// Prepare 检查 Docker、准备 compose 目录和 compose 文件（没有才安装，已有的不覆盖）、上传配置文件、检查前置条件，
// 并确认 compose 文件里有要部署的服务。在构建镜像之前执行，服务器没准备好就不浪费构建时间。
func (d Driver) Prepare(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	q := driver.Q
	dir := cfg.ComposeDir
	if err := x.Remote(`docker info >/dev/null 2>&1 || { echo 'docker 不可用：需要安装 Docker，并把部署用户加入 docker 组' >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo 'docker compose 不可用：需要安装 compose 插件（docker-compose-plugin 或 docker-compose-v2）' >&2; exit 1; }`); err != nil {
		return err
	}
	if err := driver.EnsureDirs(x, dir); err != nil {
		return err
	}
	if cfg.ComposeContent != "" {
		if err := x.Remote(fmt.Sprintf(`cd %s
if %s; then
  echo 'compose 文件已存在，不覆盖'
else
  printf '%%s\n' %s > docker-compose.yml && echo '已安装 %s/docker-compose.yml'
fi`, q(dir), cfg.anyComposeFile(), q(strings.TrimRight(cfg.RenderCompose(), "\n")), dir)); err != nil {
			return err
		}
	} else if err := x.Remote(fmt.Sprintf("cd %s && { %s; } || { echo '%s 下没有 compose 文件（设置为以服务器上已有的为准）' >&2; exit 1; }", q(dir), cfg.anyComposeFile(), dir)); err != nil {
		return err
	}
	if err := driver.UploadConfigFiles(x, dir); err != nil {
		return err
	}
	if len(x.C.Records("config_files")) > 0 {
		// 配置文件权限 600、属主是部署用户；compose 里写 user: "${DEPLOY_UID}:${DEPLOY_GID}" 让容器以部署用户运行才能读到
		if err := x.Remote(fmt.Sprintf(`cd %s && touch .env && grep -v -e '^DEPLOY_UID=' -e '^DEPLOY_GID=' .env > .env.tmp; \
printf 'DEPLOY_UID=%%s\nDEPLOY_GID=%%s\n' "$(id -u)" "$(id -g)" >> .env.tmp && mv .env.tmp .env`, q(dir))); err != nil {
			return err
		}
	}
	if err := driver.CheckRequired(x, dir); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cd %s && services=$(docker compose config --services) || exit 1\n", q(dir))
	for _, s := range cfg.Services {
		fmt.Fprintf(&b, "echo \"$services\" | grep -qx %s || { echo 'compose 文件里没有服务 %s' >&2; exit 1; }\n", q(s), s)
	}
	return x.Remote(b.String())
}

// Build 本地 docker buildx build。用镜像仓库时直接推送，否则加载到本机 Docker 再传到服务器。
func (d Driver) Build(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	args := []string{"buildx", "build", "--platform", cfg.Platform, "-f", cfg.Dockerfile, "-t", cfg.ref(x.C.Version)}
	for _, kv := range cfg.BuildArgs {
		args = append(args, "--build-arg", kv.Key+"="+kv.Value)
	}
	if cfg.Transfer == "registry" {
		args = append(args, "--push")
	} else {
		args = append(args, "--load")
	}
	args = append(args, "--progress", "plain", cfg.Context)
	return x.Local(shell.Cmd{Name: "docker", Args: args, Dir: x.C.Repo.Path})
}

// Upload 用镜像仓库时在服务器上拉取；否则 docker save | gzip | ssh 'gunzip | docker load' 直传。
func (d Driver) Upload(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	ref := cfg.ref(x.C.Version)
	if cfg.Transfer == "registry" {
		return x.Remote("docker pull " + driver.Q(ref))
	}
	return x.Pipe(shell.Cmd{Name: "docker", Args: []string{"save", ref}}, "gunzip | docker load")
}

// Activate 只改 .env 里的镜像标签变量，然后 docker compose up -d 要部署的服务。
func (d Driver) Activate(x *driver.Exec, version string) error {
	cfg, _ := d.Resolve(x.C)
	q := driver.Q
	services := make([]string, len(cfg.Services))
	for i, s := range cfg.Services {
		services[i] = q(s)
	}
	return x.Remote(fmt.Sprintf(`cd %s
docker image inspect %s >/dev/null 2>&1 || { echo '服务器上没有镜像 %s' >&2; exit 1; }
touch .env && grep -v '^%s=' .env > .env.tmp; echo %s=%s >> .env.tmp && mv .env.tmp .env
docker compose up -d %s`, q(cfg.ComposeDir), q(cfg.ref(version)), cfg.ref(version), cfg.TagVar, cfg.TagVar, version, strings.Join(services, " ")))
}

// HealthScript 容器刚启动时可能还没退出，先等 3 秒再检查。
func (d Driver) HealthScript(x *driver.Exec) string {
	cfg, _ := d.Resolve(x.C)
	q := driver.Q
	services := make([]string, len(cfg.Services))
	for i, s := range cfg.Services {
		services[i] = q(s)
	}
	def := fmt.Sprintf("cd %s && test \"$(docker compose ps --status running -q %s | wc -l)\" -ge %d", q(cfg.ComposeDir), strings.Join(services, " "), len(services))
	return "sleep 3\n" + driver.HealthCommand(x.C, def)
}

// State 当前版本是 .env 里标签变量的值；保留的版本是服务器上这个镜像的标签（新的在前）。
func (d Driver) State(x *driver.Exec) (*driver.OnlineState, error) {
	cfg, _ := d.Resolve(x.C)
	q := driver.Q
	out, err := x.Query(fmt.Sprintf(`cd %s 2>/dev/null || exit 0
echo "current=$(grep '^%s=' .env 2>/dev/null | tail -n 1 | cut -d= -f2-)"
docker image ls --format '{{.Tag}}' %s 2>/dev/null | grep -v -e '^<none>$' -e '^latest$' | while read -r t; do echo "release=$t"; done
echo "last=$(grep -v '^$' deploy.log 2>/dev/null | tail -n 1)"`, q(cfg.ComposeDir), cfg.TagVar, q(cfg.imageName())))
	if err != nil {
		return nil, err
	}
	return driver.ParseState(out), nil
}

// Cleanup 每个镜像只保留最近 keep 个标签，当前版本永远不删。
func (d Driver) Cleanup(x *driver.Exec) error {
	cfg, _ := d.Resolve(x.C)
	q := driver.Q
	keep := cfg.KeepImages
	if keep < 1 {
		keep = 1
	}
	return x.Remote(fmt.Sprintf(`cd %s || exit 0
cur=$(grep '^%s=' .env 2>/dev/null | tail -n 1 | cut -d= -f2-)
docker image ls --format '{{.Tag}}' %s | grep -v -e '^<none>$' -e '^latest$' | tail -n +%d | while read -r t; do
  [ "$t" = "$cur" ] || { docker image rm %s:"$t" >/dev/null && echo "删除旧镜像 $t"; }
done`, q(cfg.ComposeDir), cfg.TagVar, q(cfg.imageName()), keep+1, q(cfg.imageName())))
}

// LogFile 是 <compose 目录>/deploy.log。
func (d Driver) LogFile(x *driver.Exec) string {
	cfg, _ := d.Resolve(x.C)
	return cfg.ComposeDir + "/deploy.log"
}
