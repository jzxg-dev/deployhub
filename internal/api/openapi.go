package api

import (
	"net/http"
	"regexp"
)

// 请求体的结构。应用的参数随部署方式不同，具体有哪些见 GET /api/drivers。
var schemas = map[string]any{
	"Error": obj(map[string]any{
		"error": obj(map[string]any{
			"code":    str("错误类别，如 invalid、not_found、conflict、busy、forbidden、page_only"),
			"message": str("中文说明"),
			"fields": map[string]any{"type": "array", "description": "校验错误按字段列出", "items": obj(map[string]any{
				"field": str("参数名"), "message": str("这个参数哪里不对"),
			})},
		}),
	}),
	"DeployRequest": obj(map[string]any{
		"apps":            map[string]any{"type": "array", "items": str("项目/应用，如 shop/shop-api"), "description": "要部署的应用"},
		"continueOnError": map[string]any{"type": "boolean", "description": "某个应用失败后是否继续部署剩下的，默认停止"},
	}, "apps"),
	"RollbackRequest": obj(map[string]any{
		"app":     str("项目/应用"),
		"version": str("目标版本；不填时回到当前版本的上一个版本"),
	}, "app"),
	"AppInput": obj(map[string]any{
		"driver":   str("部署方式，新建时必填：gobinary、static、image、workers"),
		"params":   map[string]any{"type": "object", "additionalProperties": true, "description": "参数，键见 GET /api/drivers 里的通用参数和这种部署方式的参数"},
		"revision": map[string]any{"type": "integer", "description": "修改时必填：基于的版本号，不是最新版本时拒绝保存"},
	}, "params"),
	"ProjectInput": obj(map[string]any{
		"name": str("标识，小写字母、数字和 -，创建后不能改"), "title": str("名称"), "description": str("说明"),
	}, "title"),
	"ServerInput": obj(map[string]any{
		"name": str("名称，创建后不能改"), "title": str("说明"), "host": str("地址"),
		"port":          map[string]any{"type": "integer", "description": "端口，默认 22"},
		"user":          str("登录用户"),
		"keyCredential": str("平台保存的 SSH 私钥（凭据）的名称"),
		"keyPath":       str("运行 deployhub 的机器上的密钥文件路径，和 keyCredential 只能填一个"),
		"sshOptions":    map[string]any{"type": "array", "items": str("名称=值"), "description": "额外的 ssh -o 选项"},
	}, "host"),
	"TokenInput": obj(map[string]any{
		"name":  str("名称，如 Claude Code"),
		"level": map[string]any{"type": "string", "enum": []string{"read", "deploy", "admin"}, "description": "权限：只读 / 可部署 / 管理"},
	}, "name", "level"),
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

var pathParamRe = regexp.MustCompile(`\{([a-z]+)\}`)

var accessNote = map[access]string{
	accessPublic:    "不需要登录。",
	accessPage:      "只能登录页面后调用，访问令牌不能调用。",
	accessPageAdmin: "只能登录页面后调用，而且要是管理员账号。",
	accessRead:      "访问令牌需要「只读」或更高的权限。",
	accessDeploy:    "访问令牌需要「可部署」或更高的权限。",
	accessAdmin:     "访问令牌需要「管理」权限。",
}

// openapi 返回接口文档（OpenAPI 3.1），从路由表生成。
func (s *server) openapi(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.openapiDoc())
}

func (s *server) openapiDoc() map[string]any {
	errResp := map[string]any{"description": "出错", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}}}}
	paths := map[string]any{}
	for _, rt := range s.routes() {
		op := map[string]any{
			"tags":        []string{rt.tag},
			"summary":     rt.summary,
			"description": accessNote[rt.access],
			"responses":   map[string]any{"200": map[string]any{"description": "成功"}, "default": errResp},
		}
		// x-access：访问令牌至少需要的权限；page 表示只能在页面上，public 表示不需要登录
		switch {
		case rt.access == accessPublic:
			op["x-access"] = "public"
			op["security"] = []any{}
		case rt.access.level() == "":
			op["x-access"] = "page"
			op["security"] = []any{map[string]any{"session": []string{}}}
		default:
			op["x-access"] = rt.access.level()
		}
		var params []any
		for _, m := range pathParamRe.FindAllStringSubmatch(rt.path, -1) {
			schema := map[string]any{"type": "string"}
			if m[1] == "id" || m[1] == "rev" {
				schema = map[string]any{"type": "integer"}
			}
			params = append(params, map[string]any{"name": m[1], "in": "path", "required": true, "schema": schema})
		}
		for name, desc := range rt.query {
			params = append(params, map[string]any{"name": name, "in": "query", "description": desc, "schema": map[string]any{"type": "string"}})
		}
		if rt.path == "/api/deploys" || rt.path == "/api/rollbacks" {
			params = append(params, map[string]any{"name": "Idempotency-Key", "in": "header", "schema": map[string]any{"type": "string"},
				"description": "幂等键：同一个键重复提交时返回原来的任务，网络重试不会重复部署"})
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if rt.body != "" {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + rt.body}}}}
		}
		item, _ := paths[rt.path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[rt.path] = item
		}
		item[map[string]string{"GET": "get", "POST": "post", "PUT": "put", "DELETE": "delete"}[rt.method]] = op
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "deployhub",
			"version": s.Version,
			"description": "deployhub 的 HTTP 接口。脚本和 AI 用访问令牌调用：请求头 Authorization: Bearer <令牌>，令牌在页面「设置 → 访问令牌」里创建，" +
				"权限分只读（read）、可部署（deploy）、管理（admin）三级，每个接口的 x-access 是它至少需要的级别；x-access 为 page 的接口只能登录页面后调用。" +
				"所有数据按账号隔离，令牌只能访问创建它的账号的数据。出错时返回 {\"error\": {code, message, fields}}。" +
				"AI 客户端也可以直接用 MCP：地址是 /mcp（Streamable HTTP），同样用访问令牌认证。",
		},
		"paths": paths,
		"components": map[string]any{
			"schemas": schemas,
			"securitySchemes": map[string]any{
				"token":   map[string]any{"type": "http", "scheme": "bearer", "description": "访问令牌"},
				"session": map[string]any{"type": "apiKey", "in": "cookie", "name": "deployhub_session", "description": "页面登录后的会话"},
			},
		},
		"security": []any{map[string]any{"token": []string{}}, map[string]any{"session": []string{}}},
	}
}
