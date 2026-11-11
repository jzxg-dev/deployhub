package driver

import (
	"fmt"
	"sort"
	"strings"
)

// Planner 是部署方式的计划部分：把参数换算成实际的值，生成部署时要执行的命令（预览）和检查项。
// M2 的部署执行也基于这里换算出的值。
type Planner interface {
	Plan(c *PlanContext) *Plan
	Checks(c *PlanContext) []Check
}

// PlanContext 是生成计划需要的信息。
type PlanContext struct {
	Project string
	App     string
	Params  map[string]any // 校验过的参数，敏感内容已解密；${名称} 还没替换
	Server  ServerInfo
	Repo    RepoInfo
	Version string // 版本号，预览时是示例
	// Creds 是参数里选择的凭据：参数键 → 凭据的全部字段（敏感字段是明文）。预览时为空。
	Creds map[string]map[string]string
}

// Cred 读取参数 key 选择的凭据里的字段。
func (c *PlanContext) Cred(key, field string) string { return c.Creds[key][field] }

// ServerInfo 是目标服务器的信息。
type ServerInfo struct {
	Name  string
	Dest  string // ssh 目标，如 deploy@1.2.3.4（不含端口）
	Label string // 显示用，如 deploy@1.2.3.4:5022
	User  string // 登录用户；没填时取检测到的用户
	Arch  string // 检测到的 Go 架构，没检测过为空
}

// RepoInfo 是本机仓库的信息。
type RepoInfo struct {
	Path    string
	Subject string            // 最新提交的说明
	Hints   map[string]string // 识别出的自动值，如 build_command、dist、dockerfile
}

// Plan 是部署计划。
type Plan struct {
	Resolved []Resolved `json:"resolved"` // 留空参数实际使用的值
	Steps    []Step     `json:"steps"`
	Warnings []string   `json:"warnings"`
}

// Resolved 是一个参数实际使用的值和来源。
type Resolved struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Value  string `json:"value"`
	Source string `json:"source"` // 填写 / 默认 / 识别 / 服务器检测
}

// Step 是部署的一个步骤。
type Step struct {
	Name     string    `json:"name"`
	Commands []Command `json:"commands"`
}

// Command 是一条命令。
type Command struct {
	Where   string `json:"where"` // local（本机）或 server（服务器）
	Command string `json:"command"`
	Note    string `json:"note,omitempty"`
}

// Check 是一项部署前检查。Local 和 Remote 二选一。
type Check struct {
	Group string // 本机 / 服务器
	Name  string
	// Local 是本机检查，返回是否通过和说明。
	Local func() (bool, string)
	// Remote 是在服务器上执行的 sh 片段，退出码 0 为通过；OK、Fail 是对应的说明。
	Remote string
	OK     string
	Fail   string
	// Warn 为 true 时不通过只是提醒，不影响部署。
	Warn bool
	// NoRepo 为 true 时本机仓库不可用也执行（如检查云平台账号）。
	NoRepo bool
	// NeedCreds 为 true 时需要参数里选择的凭据，凭据不可用时跳过。
	NeedCreds bool
}

// ---------- 读取参数 ----------

// Str 读取文本参数。
func (c *PlanContext) Str(key string) string {
	s, _ := c.Params[key].(string)
	return s
}

// Int 读取整数参数。
func (c *PlanContext) Int(key string, def int) int {
	switch v := c.Params[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

// Bool 读取开关参数。
func (c *PlanContext) Bool(key string) bool {
	b, _ := c.Params[key].(bool)
	return b
}

// List 读取列表参数。
func (c *PlanContext) List(key string) []string {
	switch v := c.Params[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, it := range v {
			if s, ok := it.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// KV 是一个键值对。
type KV struct{ Key, Value string }

// Map 读取键值对参数，按键排序。
func (c *PlanContext) Map(key string) []KV {
	var out []KV
	switch v := c.Params[key].(type) {
	case map[string]string:
		for k, val := range v {
			out = append(out, KV{k, val})
		}
	case map[string]any:
		for k, val := range v {
			if s, ok := val.(string); ok {
				out = append(out, KV{k, s})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Records 读取多条记录参数。
func (c *PlanContext) Records(key string) []map[string]any { return Records(c.Params, key) }

// Records 读取参数里的多条记录。
func Records(params map[string]any, key string) []map[string]any {
	switch v := params[key].(type) {
	case []map[string]any:
		return v
	case []any:
		var out []map[string]any
		for _, it := range v {
			if r, ok := it.(map[string]any); ok {
				out = append(out, r)
			}
		}
		return out
	}
	return nil
}

// Resolver 记录参数的实际值和来源，生成 Plan.Resolved。
type Resolver struct {
	c      *PlanContext
	labels map[string]string
	list   []Resolved
	warn   []string
}

// NewResolver 创建 Resolver；fields 用于取参数的显示名称。
func NewResolver(c *PlanContext, fields []Field) *Resolver {
	labels := map[string]string{}
	for _, f := range append(CommonFields(), fields...) {
		labels[f.Key] = f.Label
	}
	return &Resolver{c: c, labels: labels}
}

// Value 返回参数的值：填写了用填写的，否则依次用 auto 里第一个不为空的值。
// auto 成对给出：来源、值，如 "识别", hint, "默认", def。
func (r *Resolver) Value(key string, auto ...string) string {
	if v := r.c.Str(key); v != "" {
		return v
	}
	for i := 0; i+1 < len(auto); i += 2 {
		if auto[i+1] != "" {
			r.list = append(r.list, Resolved{Key: key, Label: r.labels[key], Value: auto[i+1], Source: auto[i]})
			return auto[i+1]
		}
	}
	return ""
}

// Warn 记录一条提醒。
func (r *Resolver) Warn(format string, a ...any) { r.warn = append(r.warn, fmt.Sprintf(format, a...)) }

// Finish 把记录的值和提醒写进计划。
func (r *Resolver) Finish(p *Plan) *Plan {
	p.Resolved = append(p.Resolved, r.list...)
	p.Warnings = append(p.Warnings, r.warn...)
	if p.Resolved == nil {
		p.Resolved = []Resolved{}
	}
	if p.Warnings == nil {
		p.Warnings = []string{}
	}
	return p
}

// ---------- 通用的命令片段 ----------

// Q 是 sh 单引号转义。
func Q(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@%+,", r)) {
			return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
		}
	}
	return s
}

// HealthCommand 返回健康检查命令：HTTP 地址轮询、自定义命令，或部署方式的默认检查。
func HealthCommand(c *PlanContext, def string) string {
	timeout := c.Int("health_timeout", 60)
	var check string
	switch c.Str("health_mode") {
	case "http":
		check = "curl -fsS -o /dev/null --max-time 5 " + Q(c.Str("health_url"))
	case "command":
		check = c.Str("health_command")
	default:
		check = def
	}
	return fmt.Sprintf("for i in $(seq 1 %d); do ( %s ) && exit 0; sleep 2; done; exit 1   # 每 2 秒一次，最多 %d 秒",
		(timeout+1)/2, check, timeout)
}

// ConfigFileCommands 返回上传配置文件和检查前置条件的命令，dir 是上传目录。
func ConfigFileCommands(c *PlanContext, dir string) []Command {
	var cmds []Command
	for _, f := range c.Records("config_files") {
		name, _ := f["name"].(string)
		if src, _ := f["source"].(string); src == "server" {
			cmds = append(cmds, Command{Where: "server", Command: "test -f " + Q(dir+"/"+name), Note: "以服务器上的 " + name + " 为准，只检查存在"})
			continue
		}
		cmds = append(cmds, Command{Where: "local", Command: fmt.Sprintf("scp <平台保存的 %s> %s:%s.uploading", name, c.Server.Dest, dir+"/"+name),
			Note: "内容不出现在命令行和日志里"})
		cmds = append(cmds, Command{Where: "server", Command: fmt.Sprintf("chmod 600 %s.uploading && mv -f %s.uploading %s", Q(dir+"/"+name), Q(dir+"/"+name), Q(dir+"/"+name))})
	}
	if files := c.List("required_files"); len(files) > 0 {
		quoted := make([]string, len(files))
		for i, f := range files {
			quoted[i] = Q(f)
		}
		cmds = append(cmds, Command{Where: "server", Command: fmt.Sprintf("cd %s && for f in %s; do test -e \"$f\" || exit 1; done", Q(dir), strings.Join(quoted, " ")),
			Note: "必需文件，缺少时在构建前停止"})
	}
	for _, rc := range c.Records("required_config") {
		file, _ := rc["file"].(string)
		key, _ := rc["key"].(string)
		want, _ := rc["value"].(string)
		cmds = append(cmds, Command{Where: "server", Command: TomlCheck(dir+"/"+file, key, want), Note: fmt.Sprintf("%s 里 %s 必须是 %s", file, key, want)})
	}
	return cmds
}

// ConfigFileChecks 返回配置文件和前置条件的服务器检查项。
func ConfigFileChecks(c *PlanContext, dir string) []Check {
	var checks []Check
	for _, f := range c.Records("config_files") {
		name, _ := f["name"].(string)
		if src, _ := f["source"].(string); src == "server" {
			checks = append(checks, Check{Group: "服务器", Name: "配置文件 " + name, Remote: "test -f " + Q(dir+"/"+name),
				OK: dir + "/" + name + " 存在", Fail: dir + "/" + name + " 不存在（来源设为以服务器为准，需要先在服务器上放好）"})
		}
	}
	uploaded := map[string]bool{}
	for _, f := range c.Records("config_files") {
		if src, _ := f["source"].(string); src != "server" {
			name, _ := f["name"].(string)
			uploaded[name] = true
		}
	}
	for _, f := range c.List("required_files") {
		if uploaded[f] {
			continue // 部署时会上传
		}
		checks = append(checks, Check{Group: "服务器", Name: "必需文件 " + f, Remote: "test -e " + Q(dir+"/"+f),
			OK: dir + "/" + f + " 存在", Fail: dir + "/" + f + " 不存在，部署会在构建前停止"})
	}
	for _, rc := range c.Records("required_config") {
		file, _ := rc["file"].(string)
		key, _ := rc["key"].(string)
		want, _ := rc["value"].(string)
		if uploaded[file] {
			continue // 内容由平台上传，部署时再检查
		}
		checks = append(checks, Check{Group: "服务器", Name: fmt.Sprintf("配置项 %s %s", file, key), Remote: TomlCheck(dir+"/"+file, key, want),
			OK: fmt.Sprintf("%s = %s", key, want), Fail: fmt.Sprintf("%s/%s 里 %s 不是 %s（或文件不存在）", dir, file, key, want)})
	}
	return checks
}

// tomlAwk 在服务器上用 awk 读取 toml：找到 [节名] 下的键（不区分大小写），去掉行尾注释和引号后与期望值比较。
// 只支持「键 = 单行值」的写法，足够检查开关和地址。
const tomlAwk = `/^[[:space:]]*\[/ { s = $0; sub(/#.*/, "", s); gsub(/[][[:space:]]/, "", s); cur = tolower(s); next }
cur == sec {
  line = $0; sub(/#.*/, "", line); n = index(line, "=")
  if (n == 0) next
  k = substr(line, 1, n - 1); gsub(/[[:space:]]/, "", k)
  if (tolower(k) != key) next
  v = substr(line, n + 1); gsub(/^[[:space:]]+|[[:space:]]+$/, "", v); gsub("^[\"" sq "]|[\"" sq "]$", "", v)
  if (v == want) found = 1
}
END { exit found ? 0 : 1 }`

// TomlCheck 返回检查 toml 取值的命令：文件中 [节名] 下的键等于期望值时退出码为 0。
func TomlCheck(path, key, want string) string {
	section, name := "", key
	if i := strings.LastIndex(key, "."); i >= 0 {
		section, name = key[:i], key[i+1:]
	}
	return fmt.Sprintf(`awk -v sq="'" -v sec=%s -v key=%s -v want=%s %s %s`,
		Q(strings.ToLower(section)), Q(strings.ToLower(name)), Q(want), Q(tomlAwk), Q(path))
}

// WritableCheck 返回检查目录可写（不存在时检查能否创建）的命令。
func WritableCheck(dir string) string {
	return fmt.Sprintf(`d=%s; while [ ! -e "$d" ]; do d=$(dirname "$d"); done; test -w "$d" || sudo -n true`, Q(dir))
}
