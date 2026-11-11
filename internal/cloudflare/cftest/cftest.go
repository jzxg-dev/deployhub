// Package cftest 是模拟的 Cloudflare 接口，测试和本地验证 Workers 部署用（不连真实的 Cloudflare）。
//
// 支持验证 Token、读取账号和 workers.dev 子域名、Worker 的版本和部署（读取、把某个版本设为 100% 流量）。
// 发布新版本用 FakeWrangler 写出的假 wrangler：它调用 POST /__fake/deploy 新建版本并设为 100% 流量。
// 模拟的 Worker 在 GET /__worker/<名称>：返回当前版本名，版本标记为坏的时返回 500，用来测试健康检查失败和自动回滚。
package cftest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Server 是模拟的 Cloudflare 接口。
type Server struct {
	AccountID   string
	AccountName string
	Token       string // 有效的 API Token
	Subdomain   string

	mu      sync.Mutex
	scripts map[string]*script
	seq     int
	now     time.Time
	// Requests 记录收到的请求（方法和路径），测试用来确认调用了哪些接口。
	Requests []string
}

type version struct {
	ID          string            `json:"id"`
	Number      int               `json:"number"`
	Metadata    map[string]string `json:"metadata"`
	Annotations map[string]string `json:"annotations"`
	broken      bool
}

type deployment struct {
	ID          string            `json:"id"`
	CreatedOn   string            `json:"created_on"`
	Source      string            `json:"source"`
	AuthorEmail string            `json:"author_email"`
	Strategy    string            `json:"strategy"`
	Versions    []map[string]any  `json:"versions"`
	Annotations map[string]string `json:"annotations"`
}

type script struct {
	versions    []*version // 旧的在前
	deployments []*deployment
}

// New 创建模拟接口。
func New(accountID, token string) *Server {
	return &Server{AccountID: accountID, AccountName: "测试账号", Token: token, Subdomain: "demo",
		scripts: map[string]*script{}, now: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)}
}

func (s *Server) tick() string {
	s.now = s.now.Add(time.Minute)
	return s.now.Format(time.RFC3339Nano)
}

func ok(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
}

func fail(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": code, "message": msg}}, "result": nil})
}

// ServeHTTP 处理请求。路径可以带 /client/v4 前缀。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, r.Method+" "+path)

	if name, found := strings.CutPrefix(path, "/__worker/"); found {
		s.serveWorker(w, name)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.Token {
		fail(w, http.StatusUnauthorized, 10000, "Authentication error")
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case path == "/user/tokens/verify":
		ok(w, map[string]string{"id": "tok", "status": "active"})
	case path == "/__fake/deploy" && r.Method == http.MethodPost:
		s.fakeDeploy(w, r)
	case len(parts) < 2 || parts[0] != "accounts" || parts[1] != s.AccountID:
		fail(w, http.StatusForbidden, 9109, "Unauthorized to access requested resource")
	case len(parts) == 2:
		ok(w, map[string]string{"id": s.AccountID, "name": s.AccountName})
	case len(parts) == 4 && parts[2] == "workers" && parts[3] == "subdomain":
		ok(w, map[string]string{"subdomain": s.Subdomain})
	case len(parts) == 6 && parts[2] == "workers" && parts[3] == "scripts":
		s.serveScript(w, r, parts[4], parts[5])
	default:
		fail(w, http.StatusNotFound, 7003, "No route for that URI")
	}
}

func (s *Server) serveScript(w http.ResponseWriter, r *http.Request, name, what string) {
	sc := s.scripts[name]
	if sc == nil {
		fail(w, http.StatusNotFound, 10007, "This Worker does not exist on your account.")
		return
	}
	switch {
	case what == "versions" && r.Method == http.MethodGet:
		items := make([]*version, 0, len(sc.versions))
		for i := len(sc.versions) - 1; i >= 0; i-- {
			items = append(items, sc.versions[i])
		}
		ok(w, map[string]any{"items": items})
	case what == "deployments" && r.Method == http.MethodGet:
		list := make([]*deployment, 0, len(sc.deployments))
		for i := len(sc.deployments) - 1; i >= 0; i-- {
			list = append(list, sc.deployments[i])
		}
		ok(w, map[string]any{"deployments": list})
	case what == "deployments" && r.Method == http.MethodPost:
		var body struct {
			Versions []struct {
				VersionID  string  `json:"version_id"`
				Percentage float64 `json:"percentage"`
			} `json:"versions"`
			Annotations map[string]string `json:"annotations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Versions) != 1 {
			fail(w, http.StatusBadRequest, 10021, "bad request")
			return
		}
		found := false
		for _, v := range sc.versions {
			found = found || v.ID == body.Versions[0].VersionID
		}
		if !found {
			fail(w, http.StatusNotFound, 10220, "version not found")
			return
		}
		if len([]rune(body.Annotations["workers/message"])) > 100 {
			fail(w, http.StatusBadRequest, 10021, "message too long")
			return
		}
		d := s.addDeployment(sc, body.Versions[0].VersionID, body.Annotations["workers/message"])
		d.Annotations["workers/triggered_by"] = "deployment"
		ok(w, d)
	default:
		fail(w, http.StatusNotFound, 7003, "No route for that URI")
	}
}

func (s *Server) addDeployment(sc *script, versionID, message string) *deployment {
	s.seq++
	d := &deployment{ID: fmt.Sprintf("dep-%04d", s.seq), CreatedOn: s.tick(), Source: "api", AuthorEmail: "test@example.com", Strategy: "percentage",
		Versions:    []map[string]any{{"version_id": versionID, "percentage": 100}},
		Annotations: map[string]string{"workers/message": message}}
	sc.deployments = append(sc.deployments, d)
	return d
}

// fakeDeploy 是假 wrangler 调用的：新建版本并设为 100% 流量。
func (s *Server) fakeDeploy(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.Form.Get("account") != s.AccountID {
		fail(w, http.StatusForbidden, 9109, "wrong account")
		return
	}
	name, tag, msg := r.Form.Get("name"), r.Form.Get("tag"), r.Form.Get("message")
	if name == "" || len(tag) > 25 || len([]rune(msg)) > 100 {
		fail(w, http.StatusBadRequest, 10021, "bad tag or message")
		return
	}
	sc := s.scripts[name]
	if sc == nil {
		sc = &script{}
		s.scripts[name] = sc
	}
	s.seq++
	v := &version{ID: fmt.Sprintf("%08x-0000-4000-8000-%012x", s.seq, s.seq), Number: len(sc.versions) + 1,
		Metadata:    map[string]string{"created_on": s.tick(), "author_email": "test@example.com", "source": "wrangler"},
		Annotations: map[string]string{"workers/tag": tag, "workers/message": msg, "workers/triggered_by": "upload"},
		broken:      r.Form.Get("broken") == "1"}
	sc.versions = append(sc.versions, v)
	s.addDeployment(sc, v.ID, msg)
	ok(w, map[string]string{"id": v.ID})
}

// AddVersion 直接加一个版本并部署（模拟控制台或旧工具发布的版本），返回版本 ID。
func (s *Server) AddVersion(name, tag, message string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.scripts[name]
	if sc == nil {
		sc = &script{}
		s.scripts[name] = sc
	}
	s.seq++
	v := &version{ID: fmt.Sprintf("%08x-0000-4000-8000-%012x", s.seq, s.seq), Number: len(sc.versions) + 1,
		Metadata:    map[string]string{"created_on": s.tick(), "source": "dash"},
		Annotations: map[string]string{"workers/tag": tag, "workers/message": message}}
	sc.versions = append(sc.versions, v)
	s.addDeployment(sc, v.ID, message)
	return v.ID
}

// Current 返回 Worker 当前 100% 流量的版本 ID 和它的版本说明。
func (s *Server) Current(name string) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current(name)
}

func (s *Server) current(name string) (string, string) {
	sc := s.scripts[name]
	if sc == nil || len(sc.deployments) == 0 {
		return "", ""
	}
	id := sc.deployments[len(sc.deployments)-1].Versions[0]["version_id"].(string)
	for _, v := range sc.versions {
		if v.ID == id {
			return id, v.Annotations["workers/message"]
		}
	}
	return id, ""
}

func (s *Server) serveWorker(w http.ResponseWriter, name string) {
	id, msg := s.current(name)
	if id == "" {
		http.Error(w, "no such worker", http.StatusNotFound)
		return
	}
	for _, v := range s.scripts[name].versions {
		if v.ID == id && v.broken {
			http.Error(w, "worker threw exception", http.StatusInternalServerError)
			return
		}
	}
	fmt.Fprintln(w, msg)
}

// fakeWrangler 是假的 wrangler：解析 deploy 的参数，调用模拟接口发布。仓库副本里有 BROKEN 文件时发布一个坏版本。
// 会打印一行带 Token 的输出，测试用来确认日志里的 Token 被遮盖。
const fakeWrangler = `#!/bin/sh
set -e
[ "$1" = "deploy" ] || { echo "假 wrangler 只支持 deploy" >&2; exit 2; }
shift
config=wrangler.jsonc name="" tag="" message=""
while [ $# -gt 0 ]; do
  case "$1" in
    --config) config=$2; shift 2 ;;
    --name) name=$2; shift 2 ;;
    --tag) tag=$2; shift 2 ;;
    --message) message=$2; shift 2 ;;
    *) shift ;;
  esac
done
if [ -z "$name" ]; then
  name=$(sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$config" | head -n 1)
fi
broken=0
[ -f BROKEN ] && broken=1
echo "假 wrangler：发布 ${name}（标签 ${tag}），token=${CLOUDFLARE_API_TOKEN}"
curl -fsS -X POST -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
  --data-urlencode "account=$CLOUDFLARE_ACCOUNT_ID" --data-urlencode "name=$name" \
  --data-urlencode "tag=$tag" --data-urlencode "message=$message" --data-urlencode "broken=$broken" \
  "$CLOUDFLARE_API_BASE_URL/__fake/deploy" >/dev/null
echo "Deployed ${name} triggers"
`

// WriteFakeWrangler 把假 wrangler 写到仓库目录的 fake-wrangler.sh。构建副本不复制 node_modules，
// 构建命令用 InstallCommand 把它装成 node_modules/.bin/wrangler（相当于安装依赖），部署时从项目依赖里找到它。
func WriteFakeWrangler(dir string) error {
	return os.WriteFile(filepath.Join(dir, "fake-wrangler.sh"), []byte(fakeWrangler), 0o755)
}

// InstallCommand 是测试应用的构建命令：把假 wrangler 装进 node_modules/.bin。
const InstallCommand = "mkdir -p node_modules/.bin && cp fake-wrangler.sh node_modules/.bin/wrangler && chmod +x node_modules/.bin/wrangler"
