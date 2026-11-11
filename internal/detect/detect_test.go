package detect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "初始提交"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

var detector = RepoDetector{LocalGoVersion: func(context.Context) string { return "go1.20" }}

func TestDetectGoRepo(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n\ngo 1.25.3\n")
	write(t, root, "cmd/shop-api/main.go", "// 注释\npackage main\nfunc main(){}\n")
	write(t, root, "cmd/open-api/main.go", "package main\nfunc main(){}\n")
	write(t, root, "internal/lib/lib.go", "package lib\n")
	write(t, root, "node_modules/x/main.go", "package main\n") // 应跳过
	write(t, root, "Dockerfile", "FROM scratch\n")
	gitInit(t, root)
	write(t, root, "dirty.txt", "x")

	r := detector.Detect(context.Background(), root)
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	if r.Branch != "main" || r.Commit == "" || r.Subject != "初始提交" || r.Dirty != 1 {
		t.Fatalf("git 信息不对：%+v", r)
	}
	var mains []string
	for _, o := range r.Options["go_mains"] {
		mains = append(mains, o.Value)
	}
	if strings.Join(mains, ",") != "./cmd/open-api,./cmd/shop-api" {
		t.Fatalf("go_mains = %v", mains)
	}
	if r.SuggestDriver != "gobinary" || r.Hints["branch"] != "main" || r.Hints["dockerfile"] != "Dockerfile" {
		t.Fatalf("推荐不对：%s %v", r.SuggestDriver, r.Hints)
	}
	if !strings.Contains(strings.Join(r.Notes, ";"), "go.mod 要求 Go 1.25.3，本机是 1.20") {
		t.Fatalf("应提示 Go 版本：%v", r.Notes)
	}
}

func TestDetectNodeRepo(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"scripts":{"build":"vite build"}}`)
	write(t, root, "pnpm-lock.yaml", "")
	write(t, root, "mise.toml", "")
	write(t, root, "vite.config.ts", "export default { build: { outDir: 'www' } }")
	write(t, root, ".env.production", "VITE_APP_URL=x\nVITE_API_URL = y\nOTHER=1\n")
	gitInit(t, root)

	r := detector.Detect(context.Background(), root)
	if r.SuggestDriver != "static" {
		t.Fatalf("推荐 %s，期望 static", r.SuggestDriver)
	}
	if r.Hints["build_command"] != "mise exec -- pnpm install --frozen-lockfile && mise exec -- pnpm build" {
		t.Fatalf("build_command = %s", r.Hints["build_command"])
	}
	if r.Hints["dist"] != "www" {
		t.Fatalf("dist = %s", r.Hints["dist"])
	}
	if len(r.Options["build_env_keys"]) != 2 {
		t.Fatalf("build_env_keys = %v", r.Options["build_env_keys"])
	}
}

func TestDetectErrors(t *testing.T) {
	if r := detector.Detect(context.Background(), "/no/such/dir"); !strings.Contains(r.Error, "不存在") {
		t.Fatalf("Error = %s", r.Error)
	}
	if r := detector.Detect(context.Background(), t.TempDir()); !strings.Contains(r.Error, "不是 git 仓库") {
		t.Fatalf("Error = %s", r.Error)
	}
	if r := detector.Detect(context.Background(), " "); r.Error == "" {
		t.Fatal("空路径应报错")
	}
}

func TestParseServerFacts(t *testing.T) {
	f := ParseServerFacts("machine=x86_64\nuser=deploy\nos=Ubuntu 24.04 LTS\ndocker=27.1.1\ncompose=1\nrsync=1\nsystemd=1\n")
	if f.Arch != "amd64" || f.User != "deploy" || !f.Docker || f.DockerVersion != "27.1.1" || !f.Compose || !f.Rsync || !f.Systemd || f.SudoNoPassword {
		t.Fatalf("%+v", f)
	}
	if GoArch("aarch64") != "arm64" || GoArch("riscv64") != "" {
		t.Fatal("GoArch 不对")
	}
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"1.27.3", "1.25", 1}, {"1.25", "1.25.0", 0}, {"1.24.9", "1.25", -1}, {"1.26rc1", "1.26", 0}}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%s, %s) = %d", c.a, c.b, got)
		}
	}
}
