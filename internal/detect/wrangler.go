package detect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// WranglerFiles 是 wrangler 配置文件名，按 wrangler 自己的优先顺序。
var WranglerFiles = []string{"wrangler.json", "wrangler.jsonc", "wrangler.toml"}

// Wrangler 是 wrangler 配置文件里部署平台关心的内容。
type Wrangler struct {
	File         string   // 文件名，相对读取的目录
	Name         string   // Worker 名称
	BuildCommand string   // build.command，wrangler 发布前自己执行
	WorkersDev   bool     // 开着 workers.dev 地址（没写时 wrangler 默认开）
	Domains      []string // routes 里的自定义域名和路由
}

// FindWrangler 在目录里找 wrangler 配置文件，返回文件名；没有时为空。
func FindWrangler(dir string) string {
	for _, f := range WranglerFiles {
		if st, err := os.Stat(filepath.Join(dir, f)); err == nil && !st.IsDir() {
			return f
		}
	}
	return ""
}

// ReadWrangler 读取 wrangler 配置文件（json、jsonc、toml）。
func ReadWrangler(path string) (*Wrangler, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	w := &Wrangler{File: filepath.Base(path), WorkersDev: true}
	if strings.HasSuffix(path, ".toml") {
		readWranglerToml(string(b), w)
		return w, nil
	}
	var cfg struct {
		Name  string `json:"name"`
		Build struct {
			Command string `json:"command"`
		} `json:"build"`
		WorkersDev *bool             `json:"workers_dev"`
		Routes     []json.RawMessage `json:"routes"`
		Route      json.RawMessage   `json:"route"`
	}
	if err := json.Unmarshal(StripJSONC(b), &cfg); err != nil {
		return nil, fmt.Errorf("%s 格式不对：%w", filepath.Base(path), err)
	}
	w.Name, w.BuildCommand = cfg.Name, cfg.Build.Command
	if cfg.WorkersDev != nil {
		w.WorkersDev = *cfg.WorkersDev
	}
	routes := cfg.Routes
	if len(cfg.Route) > 0 {
		routes = append(routes, cfg.Route)
	}
	for _, r := range routes {
		var s string
		if json.Unmarshal(r, &s) == nil {
			w.Domains = append(w.Domains, s)
			continue
		}
		var obj struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal(r, &obj) == nil && obj.Pattern != "" {
			w.Domains = append(w.Domains, obj.Pattern)
		}
	}
	return w, nil
}

var (
	tomlSectionRe = regexp.MustCompile(`^\s*\[\[?\s*([A-Za-z0-9_.\-]+)\s*\]?\]\s*(#.*)?$`)
	tomlKVRe      = regexp.MustCompile(`^\s*([A-Za-z0-9_\-]+)\s*=\s*(.+?)\s*(#.*)?$`)
)

// readWranglerToml 只读最外层的 name、workers_dev 和 [build] 的 command，够用即可。
func readWranglerToml(src string, w *Wrangler) {
	section := ""
	for _, line := range strings.Split(src, "\n") {
		if m := tomlSectionRe.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		m := tomlKVRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, val := m[1], strings.Trim(m[2], `"'`)
		switch {
		case section == "" && key == "name":
			w.Name = val
		case section == "" && key == "workers_dev":
			w.WorkersDev = val != "false"
		case section == "build" && key == "command":
			w.BuildCommand = val
		}
	}
}

// StripJSONC 去掉 JSONC 里的注释和结尾多余的逗号，变成标准 JSON。
func StripJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		case c == '}' || c == ']':
			// 去掉前面紧挨着（只隔空白）的逗号
			j := len(out) - 1
			for j >= 0 && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j--
			}
			if j >= 0 && out[j] == ',' {
				out = append(out[:j], out[j+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// NodePackage 是 package.json 里部署关心的内容。
type NodePackage struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// ReadPackage 读取目录里的 package.json；没有时返回 nil。
func ReadPackage(dir string) (*NodePackage, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p NodePackage
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("package.json 格式不对：%w", err)
	}
	return &p, nil
}

// HasDep 表示依赖里有某个包。
func (p *NodePackage) HasDep(name string) bool {
	if p == nil {
		return false
	}
	_, a := p.Dependencies[name]
	_, b := p.DevDependencies[name]
	return a || b
}

// PackageManager 按锁文件判断包管理器，返回安装依赖和执行脚本的命令前缀。
func PackageManager(dir string) (install, run, note string) {
	switch {
	case exists(dir, "pnpm-lock.yaml"):
		return "pnpm install --frozen-lockfile", "pnpm run", ""
	case exists(dir, "yarn.lock"):
		return "yarn install --frozen-lockfile", "yarn run", ""
	case exists(dir, "bun.lock"), exists(dir, "bun.lockb"):
		return "bun install --frozen-lockfile", "bun run", ""
	case exists(dir, "package-lock.json"):
		return "npm ci", "npm run", ""
	}
	return "npm install", "npm run", "没有锁文件，依赖版本可能每次不同"
}

// HasMise 表示目录里有 mise 配置，命令要通过 mise exec 执行。
func HasMise(dir string) bool { return exists(dir, "mise.toml") || exists(dir, ".mise.toml") }

// WorkersBuildCommand 返回 Workers 应用默认的构建命令：有 package.json 时按锁文件安装依赖；
// wrangler 配置里没有 build.command、package.json 有 build 脚本时再执行 build（有 build.command 时 wrangler 发布前自己执行）。
// 没有 package.json 时为空（wrangler 自己打包入口文件）。
func WorkersBuildCommand(dir string, w *Wrangler) string {
	pkg, err := ReadPackage(dir)
	if err != nil || pkg == nil {
		return ""
	}
	install, run, _ := PackageManager(dir)
	parts := []string{install}
	if (w == nil || w.BuildCommand == "") && pkg.Scripts["build"] != "" {
		parts = append(parts, run+" build")
	}
	if HasMise(dir) {
		for i, p := range parts {
			parts[i] = "mise exec -- " + p
		}
	}
	return strings.Join(parts, " && ")
}

// detectWrangler 识别 Cloudflare Workers：有 wrangler 配置文件时推荐 Workers，并给出 Worker 名称、构建命令。
// 提示的键带 workers. 前缀，和静态站点的同名参数（如 build_command）区分。
func detectWrangler(root string, r *Repo) bool {
	file := FindWrangler(root)
	if file == "" {
		return false
	}
	w, err := ReadWrangler(filepath.Join(root, file))
	if err != nil {
		r.Notes = append(r.Notes, err.Error())
		return true
	}
	r.Hints["workers.config"] = file
	if w.Name != "" {
		r.Hints["workers.worker_name"] = w.Name
	}
	if cmd := WorkersBuildCommand(root, w); cmd != "" {
		r.Hints["workers.build_command"] = cmd
	}
	return true
}
