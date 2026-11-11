package detect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadWranglerJSONC(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"wrangler.jsonc": `{
  "$schema": "node_modules/wrangler/config-schema.json", // 地址里的 // 不是注释
  "name": "demo-official",
  /* 块注释 */
  "workers_dev": false,
  "routes": [
    { "pattern": "example.net", "custom_domain": true },
    "example.com/*",
  ],
  "build": { "command": "pnpm run build:workers", },
}`})
	if f := FindWrangler(dir); f != "wrangler.jsonc" {
		t.Fatalf("应找到 wrangler.jsonc，得到 %q", f)
	}
	w, err := ReadWrangler(filepath.Join(dir, "wrangler.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "demo-official" || w.BuildCommand != "pnpm run build:workers" || w.WorkersDev || len(w.Domains) != 2 || w.Domains[0] != "example.net" {
		t.Fatalf("读取结果：%+v", w)
	}
}

func TestReadWranglerToml(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"wrangler.toml": "name = \"proxy\" # 注释\nmain = \"src/index.js\"\n\n[build]\ncommand = \"npm run build\"\n\n[vars]\nname = \"不是 Worker 名称\"\n"})
	w, err := ReadWrangler(filepath.Join(dir, "wrangler.toml"))
	if err != nil || w.Name != "proxy" || w.BuildCommand != "npm run build" || !w.WorkersDev {
		t.Fatalf("读取结果：%+v %v", w, err)
	}
}

func TestWorkersBuildCommand(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		w     *Wrangler
		want  string
	}{
		{"没有 package.json", map[string]string{"worker.js": ""}, &Wrangler{}, ""},
		{"配置里有 build.command 只装依赖", map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`, "pnpm-lock.yaml": ""},
			&Wrangler{BuildCommand: "pnpm run build:workers"}, "pnpm install --frozen-lockfile"},
		{"没有 build.command 再执行 build", map[string]string{"package.json": `{"scripts":{"build":"astro build"}}`, "pnpm-lock.yaml": "", "mise.toml": ""},
			&Wrangler{}, "mise exec -- pnpm install --frozen-lockfile && mise exec -- pnpm run build"},
		{"npm", map[string]string{"package.json": `{}`, "package-lock.json": ""}, &Wrangler{}, "npm ci"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, c.files)
			if got := WorkersBuildCommand(dir, c.w); got != c.want {
				t.Fatalf("得到 %q，期望 %q", got, c.want)
			}
		})
	}
}

func TestDetectSuggestsWorkers(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"wrangler.jsonc": `{"name": "demo-merchant", "build": {"command": "pnpm run build:workers"}}`,
		"package.json":   `{"scripts": {"build": "vite build"}, "devDependencies": {"wrangler": "^4.133.0"}}`,
		"pnpm-lock.yaml": "",
	})
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@e", "commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	r := RepoDetector{LocalGoVersion: func(context.Context) string { return "go1.27.3" }}.Detect(context.Background(), dir)
	if r.SuggestDriver != "workers" || r.Hints["workers.worker_name"] != "demo-merchant" ||
		r.Hints["workers.build_command"] != "pnpm install --frozen-lockfile" || r.Hints["build_command"] == "" {
		t.Fatalf("识别结果：%s %+v", r.SuggestDriver, r.Hints)
	}
}
