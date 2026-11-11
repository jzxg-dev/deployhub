package detect

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"deployhub/internal/driver"
	"deployhub/internal/remote"
)

// Repo 是本机仓库的识别结果。
type Repo struct {
	Path  string `json:"path"` // 展开 ~ 后的绝对路径
	Error string `json:"error,omitempty"`

	Branch      string     `json:"branch,omitempty"`
	Commit      string     `json:"commit,omitempty"` // 短 commit
	Subject     string     `json:"subject,omitempty"`
	CommittedAt *time.Time `json:"committedAt,omitempty"`
	Dirty       int        `json:"dirty"` // 未提交改动的文件数
	Remote      string     `json:"remote,omitempty"`

	// SuggestDriver 是推荐的部署方式。
	SuggestDriver string `json:"suggestDriver,omitempty"`
	// Options 是给下拉框的候选项，键对应参数声明里的 detect:<名称>。
	Options map[string][]driver.Option `json:"options"`
	// Hints 是「留空时自动使用」的值，键是参数名，如 build_command、dist、dockerfile；
	// 只对某种部署方式有效的写成 <部署方式>.<参数名>，如 workers.build_command。
	Hints map[string]string `json:"hints"`
	// Notes 是需要注意的地方，如 Go 版本不一致。
	Notes []string `json:"notes"`
}

// RepoDetector 识别仓库。LocalGoVersion 为空时调用本机 go env GOVERSION。
type RepoDetector struct {
	LocalGoVersion func(ctx context.Context) string
}

// Detect 识别仓库：git 状态和可部署的内容。路径不存在或不是 git 仓库时 Error 不为空。
func (d RepoDetector) Detect(ctx context.Context, path string) *Repo {
	r := &Repo{Options: map[string][]driver.Option{}, Hints: map[string]string{}, Notes: []string{}}
	abs, err := filepath.Abs(remote.ExpandHome(strings.TrimSpace(path)))
	if err != nil || strings.TrimSpace(path) == "" {
		r.Error = "仓库目录无效"
		return r
	}
	r.Path = abs
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		r.Error = "目录不存在：" + abs
		return r
	}
	top, err := git(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		r.Error = "不是 git 仓库：" + abs
		return r
	}
	if real, _ := filepath.EvalSymlinks(abs); top != abs && top != real {
		r.Notes = append(r.Notes, "这是仓库 "+top+" 的子目录，构建时以这个子目录为准")
	}

	r.Branch, _ = git(ctx, abs, "rev-parse", "--abbrev-ref", "HEAD")
	if out, err := git(ctx, abs, "log", "-1", "--format=%h%x00%s%x00%ct"); err == nil {
		parts := strings.SplitN(out, "\x00", 3)
		if len(parts) == 3 {
			r.Commit, r.Subject = parts[0], parts[1]
			if sec, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
				t := time.Unix(sec, 0)
				r.CommittedAt = &t
			}
		}
	} else {
		r.Notes = append(r.Notes, "仓库还没有提交")
	}
	if out, err := git(ctx, abs, "status", "--porcelain"); err == nil && out != "" {
		r.Dirty = len(strings.Split(out, "\n"))
	}
	r.Remote, _ = git(ctx, abs, "remote", "get-url", "origin")
	if r.Branch != "" && r.Branch != "HEAD" {
		r.Hints["branch"] = r.Branch
	}

	d.detectGo(ctx, abs, r)
	detectDocker(abs, r)
	detectNode(abs, r)
	workers := detectWrangler(abs, r)

	switch {
	case workers:
		r.SuggestDriver = "workers"
	case len(r.Options["go_mains"]) > 0:
		r.SuggestDriver = "gobinary"
	case r.Hints["build_command"] != "":
		r.SuggestDriver = "static"
	case len(r.Options["dockerfiles"]) > 0:
		r.SuggestDriver = "image"
	}
	return r
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// 遍历时跳过的目录
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "testdata": true, "dist": true, "build": true,
	".next": true, ".nuxt": true, ".output": true, "tmp": true, ".idea": true, ".vscode": true,
}

// walk 遍历仓库，最多 maxDepth 层，跳过常见的依赖和产物目录。
func walk(root string, maxDepth int, fn func(rel string, d fs.DirEntry)) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if rel != "." && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if rel != "." && strings.Count(rel, string(filepath.Separator))+1 > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		fn(rel, d)
		return nil
	})
}

var goVersionRe = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+(?:\.\d+)?)\s*$`)

func (d RepoDetector) detectGo(ctx context.Context, root string, r *Repo) {
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return
	}
	mains := map[string]bool{}
	walk(root, 4, func(rel string, e fs.DirEntry) {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			return
		}
		dir := filepath.Dir(rel)
		if mains[dir] {
			return
		}
		if isPackageMain(filepath.Join(root, rel)) {
			mains[dir] = true
		}
	})
	var dirs []string
	for dir := range mains {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		v := "./" + filepath.ToSlash(dir)
		if dir == "." {
			v = "."
		}
		r.Options["go_mains"] = append(r.Options["go_mains"], driver.Option{Value: v, Label: v})
	}
	if len(dirs) == 0 {
		r.Notes = append(r.Notes, "有 go.mod，但没找到 package main")
	}

	if m := goVersionRe.FindSubmatch(mod); m != nil {
		want := string(m[1])
		local := ""
		if d.LocalGoVersion != nil {
			local = d.LocalGoVersion(ctx)
		} else if out, err := exec.CommandContext(ctx, "go", "env", "GOVERSION").Output(); err == nil {
			local = strings.TrimSpace(string(out))
		}
		local = strings.TrimPrefix(local, "go")
		switch {
		case local == "":
			r.Notes = append(r.Notes, "本机找不到 go 命令，Go 服务需要在本机编译")
		case compareVersion(local, want) < 0:
			r.Notes = append(r.Notes, fmt.Sprintf("go.mod 要求 Go %s，本机是 %s，编译时会自动下载对应的工具链或失败", want, local))
		}
	}
}

// isPackageMain 读文件开头，判断是不是 package main。
func isPackageMain(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; sc.Scan() && i < 200; i++ {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "package ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "package ")) == "main"
		}
	}
	return false
}

// compareVersion 比较 1.25.3 这样的版本号。
func compareVersion(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x = leadingInt(pa[i])
		}
		if i < len(pb) {
			y = leadingInt(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// leadingInt 取开头的数字，如 26rc1 → 26。
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

func detectDocker(root string, r *Repo) {
	var files []string
	walk(root, 2, func(rel string, e fs.DirEntry) {
		n := e.Name()
		if n == "Dockerfile" || strings.HasPrefix(n, "Dockerfile.") || strings.HasSuffix(n, ".Dockerfile") {
			files = append(files, filepath.ToSlash(rel))
		}
	})
	sort.Slice(files, func(i, j int) bool {
		// 根目录的 Dockerfile 排第一
		if (files[i] == "Dockerfile") != (files[j] == "Dockerfile") {
			return files[i] == "Dockerfile"
		}
		return files[i] < files[j]
	})
	for _, f := range files {
		r.Options["dockerfiles"] = append(r.Options["dockerfiles"], driver.Option{Value: f, Label: f})
	}
	if len(files) > 0 {
		r.Hints["dockerfile"] = files[0]
	}
}

var (
	outDirRe = regexp.MustCompile(`outDir\s*:\s*['"]([^'"]+)['"]`)
	envKeyRe = regexp.MustCompile(`(?m)^\s*(VITE_[A-Z0-9_]+)\s*=`)
)

func detectNode(root string, r *Repo) {
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil || pkg.Scripts["build"] == "" {
		return
	}

	var install, build string
	switch {
	case exists(root, "pnpm-lock.yaml"):
		install, build = "pnpm install --frozen-lockfile", "pnpm build"
	case exists(root, "yarn.lock"):
		install, build = "yarn install --frozen-lockfile", "yarn build"
	case exists(root, "bun.lock"), exists(root, "bun.lockb"):
		install, build = "bun install --frozen-lockfile", "bun run build"
	case exists(root, "package-lock.json"):
		install, build = "npm ci", "npm run build"
	default:
		install, build = "npm install", "npm run build"
		r.Notes = append(r.Notes, "没有锁文件，依赖版本可能每次不同")
	}
	if exists(root, "mise.toml") || exists(root, ".mise.toml") {
		install, build = "mise exec -- "+install, "mise exec -- "+build
	}
	r.Hints["build_command"] = install + " && " + build

	dist := "dist"
	for _, name := range []string{"vite.config.ts", "vite.config.mts", "vite.config.js", "vite.config.mjs"} {
		if src, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			if m := outDirRe.FindSubmatch(src); m != nil {
				dist = string(m[1])
			}
			break
		}
	}
	r.Hints["dist"] = dist

	if env, err := os.ReadFile(filepath.Join(root, ".env.production")); err == nil {
		for _, m := range envKeyRe.FindAllSubmatch(env, -1) {
			k := string(m[1])
			r.Options["build_env_keys"] = append(r.Options["build_env_keys"], driver.Option{Value: k, Label: k})
		}
	}
}

func exists(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}
