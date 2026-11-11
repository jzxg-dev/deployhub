package api

import (
	"net/http"

	"deployhub/internal/core"
	"deployhub/internal/model"
)

// access 是接口的访问要求。
type access int

const (
	accessPublic    access = iota // 不需要登录
	accessPage                    // 只能登录页面后调用，访问令牌不行（密钥、凭据、令牌管理、删除、审批等）
	accessPageAdmin               // 只能登录页面后调用，而且要是管理员账号
	accessRead                    // 页面，或至少「只读」的访问令牌
	accessDeploy                  // 页面，或至少「可部署」的访问令牌
	accessAdmin                   // 页面，或「管理」的访问令牌
)

// level 返回访问令牌至少需要的权限级别；访问令牌不能调用的接口返回空。
func (a access) level() string {
	switch a {
	case accessRead:
		return model.LevelRead
	case accessDeploy:
		return model.LevelDeploy
	case accessAdmin:
		return model.LevelAdmin
	}
	return ""
}

func levelTitle(level string) string {
	return map[string]string{model.LevelRead: "只读", model.LevelDeploy: "可部署", model.LevelAdmin: "管理"}[level]
}

// route 是一个接口：路径、访问要求、说明。mux 的注册和 OpenAPI 文档都从这张表生成，两边不会不一致。
type route struct {
	method, path string
	access       access
	tag, summary string
	fn           http.HandlerFunc
	// body 是请求体在 OpenAPI 文档里的结构名（见 openapi.go 的 schemas），没有请求体或结构不固定时为空。
	body string
	// query 是查询参数：名称 → 说明。
	query map[string]string
}

func (s *server) routes() []route {
	const (
		pub, page, pageAdmin = accessPublic, accessPage, accessPageAdmin
		read, deploy, admin  = accessRead, accessDeploy, accessAdmin
	)
	return []route{
		// 登录和账号
		{"GET", "/api/health", pub, "账号", "服务是否在运行、版本", s.health, "", nil},
		{"POST", "/api/login", pub, "账号", "用用户名和密码登录页面", s.login, "", nil},
		{"POST", "/api/logout", pub, "账号", "退出登录", s.logout, "", nil},
		{"POST", "/api/setup", pub, "账号", "用一次性链接创建第一个管理员", s.setup, "", nil},
		{"GET", "/api/session", read, "账号", "当前账号；用访问令牌调用时还返回令牌的名称和权限", s.session, "", nil},
		{"POST", "/api/account/password", page, "账号", "修改自己的密码", s.changePassword, "", nil},
		{"GET", "/api/users", pageAdmin, "账号", "所有账号", s.listUsers, "", nil},
		{"POST", "/api/users", pageAdmin, "账号", "新建账号", s.createUser, "", nil},
		{"PUT", "/api/users/{username}", pageAdmin, "账号", "重置密码、设为管理员、停用或启用", s.updateUser, "", nil},
		{"DELETE", "/api/users/{username}", pageAdmin, "账号", "删除账号和它的全部数据", s.deleteUser, "", nil},

		// 访问令牌：只能在页面上管理
		{"GET", "/api/tokens", page, "访问令牌", "当前账号的访问令牌（不含令牌本身）", s.listTokens, "", nil},
		{"POST", "/api/tokens", page, "访问令牌", "创建访问令牌，令牌明文只在这次响应里返回", s.createToken, "TokenInput", nil},
		{"DELETE", "/api/tokens/{name}", page, "访问令牌", "吊销访问令牌", s.revokeToken, "", nil},

		// 部署方式、实时事件、接口文档
		{"GET", "/api/drivers", read, "部署方式", "部署方式和参数声明，新建应用时据此知道要填什么", s.drivers, "", nil},
		{"GET", "/api/events", read, "任务", "实时事件（SSE）：任务状态变化 job、任务日志 job_event", s.events, "", nil},
		{"GET", "/api/openapi.json", read, "部署方式", "这份接口文档", s.openapi, "", nil},

		// 服务器
		{"GET", "/api/servers", read, "服务器", "服务器列表", s.listServers, "", nil},
		{"POST", "/api/servers", admin, "服务器", "添加服务器", s.createServer, "ServerInput", nil},
		{"POST", "/api/servers/test", admin, "服务器", "用还没保存的连接信息测试连接（只读）", s.testServerInput, "ServerInput", nil},
		{"GET", "/api/servers/{name}", read, "服务器", "服务器详情", s.getServer, "", nil},
		{"PUT", "/api/servers/{name}", admin, "服务器", "修改服务器", s.updateServer, "ServerInput", nil},
		{"DELETE", "/api/servers/{name}", page, "服务器", "删除服务器", s.deleteServer, "", nil},
		{"POST", "/api/servers/{name}/test", read, "服务器", "测试连接并检测环境（只执行只读命令）", s.testServer, "", nil},

		// 密钥与变量：访问令牌只能看名称
		{"GET", "/api/secrets", read, "密钥与变量", "密钥的名称和说明，不含值", s.listSecrets, "", nil},
		{"POST", "/api/secrets", page, "密钥与变量", "添加密钥", s.createSecret, "", nil},
		{"PUT", "/api/secrets/{name}", page, "密钥与变量", "修改密钥", s.updateSecret, "", nil},
		{"DELETE", "/api/secrets/{name}", page, "密钥与变量", "删除密钥", s.deleteSecret, "", nil},
		// 读取明文用 POST：修改类请求才校验 Origin，防止其他网站诱导读取
		{"POST", "/api/secrets/{name}/reveal", page, "密钥与变量", "读取密钥的明文", s.revealSecret, "", nil},

		// 凭据：访问令牌只能看不敏感的字段
		{"GET", "/api/credentials", read, "凭据", "凭据列表，不含敏感字段的值", s.listCredentials, "", nil},
		{"POST", "/api/credentials", page, "凭据", "添加凭据", s.createCredential, "", nil},
		{"POST", "/api/credentials/test", page, "凭据", "验证还没保存的凭据", s.testCredentialInput, "", nil},
		{"GET", "/api/credentials/{name}", read, "凭据", "凭据详情，不含敏感字段的值", s.getCredential, "", nil},
		{"PUT", "/api/credentials/{name}", page, "凭据", "修改凭据", s.updateCredential, "", nil},
		{"DELETE", "/api/credentials/{name}", page, "凭据", "删除凭据", s.deleteCredential, "", nil},
		{"POST", "/api/credentials/{name}/test", page, "凭据", "验证已保存的凭据", s.testCredential, "", nil},

		// 项目和应用
		{"GET", "/api/projects", read, "项目和应用", "项目列表", s.listProjects, "", nil},
		{"POST", "/api/projects", admin, "项目和应用", "新建项目", s.createProject, "ProjectInput", nil},
		{"GET", "/api/projects/{project}", read, "项目和应用", "项目和它的应用", s.getProject, "", nil},
		{"PUT", "/api/projects/{project}", admin, "项目和应用", "修改项目", s.updateProject, "ProjectInput", nil},
		{"DELETE", "/api/projects/{project}", page, "项目和应用", "删除项目", s.deleteProject, "", nil},
		{"GET", "/api/apps", read, "项目和应用", "所有应用（敏感参数不返回内容）", s.listAllApps, "", nil},
		{"POST", "/api/projects/{project}/apps", admin, "项目和应用", "新建应用；参数见 /api/drivers", s.createApp, "AppInput", nil},
		{"POST", "/api/projects/{project}/apps/validate", read, "项目和应用", "校验新应用的参数，不保存", s.validateNewApp, "AppInput", nil},
		{"POST", "/api/projects/{project}/apps/preview", page, "项目和应用", "预览新应用部署时会执行的命令", s.previewApp, "AppInput", nil},
		{"GET", "/api/projects/{project}/apps/{app}", read, "项目和应用", "应用的参数；用访问令牌调用时敏感参数（如配置文件内容）不返回内容", s.getApp, "", nil},
		{"PUT", "/api/projects/{project}/apps/{app}", admin, "项目和应用", "修改应用，要带上基于的版本号 revision；用访问令牌调用时，没传或是占位值的敏感参数保留原来的内容", s.updateApp, "AppInput", nil},
		{"DELETE", "/api/projects/{project}/apps/{app}", page, "项目和应用", "删除应用", s.deleteApp, "", nil},
		{"POST", "/api/projects/{project}/apps/{app}/validate", read, "项目和应用", "校验对已有应用的修改，不保存", s.validateApp, "AppInput", nil},
		{"GET", "/api/projects/{project}/apps/{app}/revisions", page, "项目和应用", "应用的修改记录（含敏感参数）", s.listRevisions, "", nil},
		{"POST", "/api/projects/{project}/apps/{app}/revisions/{rev}/restore", page, "项目和应用", "恢复到某个旧版本的参数", s.restoreRevision, "", nil},
		{"POST", "/api/projects/{project}/apps/{app}/check", read, "项目和应用", "部署前检查：仓库、服务器连接、目录、必需文件（只读）", s.checkApp, "", nil},
		{"GET", "/api/projects/{project}/apps/{app}/repo", read, "项目和应用", "应用仓库当前的分支和提交（不联网）", s.appRepo, "", nil},
		{"POST", "/api/projects/{project}/apps/{app}/preview", page, "项目和应用", "预览部署时会执行的命令", s.previewApp, "AppInput", nil},
		{"POST", "/api/detect/repo", read, "项目和应用", "识别运行 deployhub 的机器上的一个仓库目录", s.detectRepo, "", nil},

		// 状态
		{"GET", "/api/projects/{project}/apps/{app}/state", read, "状态", "线上版本、服务器上保留的版本（要连服务器）", s.appState, "", nil},
		{"GET", "/api/status", read, "状态", "所有应用的部署状态：线上版本和最新提交是否一致（要连每台服务器，可能要几秒）", s.statusAll, "",
			map[string]string{"project": "只看这个项目"}},

		// 部署和任务
		{"POST", "/api/deploys/plan", read, "部署", "部署计划：新旧版本、提醒，不执行", s.deployPlan, "DeployRequest", nil},
		{"POST", "/api/deploys", deploy, "部署", "发起部署，立刻返回任务。用访问令牌发起、应用打开了「AI 部署需要审批」时，任务状态是 pending_approval，等页面上批准。请求头 Idempotency-Key 相同的重复请求返回原来的任务", s.startDeploy, "DeployRequest", nil},
		{"POST", "/api/rollbacks", deploy, "部署", "发起回滚，立刻返回任务；审批和 Idempotency-Key 同部署", s.startRollback, "RollbackRequest", nil},
		{"GET", "/api/jobs", read, "任务", "任务列表，新的在前", s.listJobs, "",
			map[string]string{"project": "只看这个项目", "app": "只看这个应用（要同时给 project）", "status": "只看这个状态，如 pending_approval", "limit": "最多几条，默认 50", "before": "只要编号小于它的，用于翻页"}},
		{"GET", "/api/jobs/{id}", read, "任务", "任务详情和各应用的进度", s.getJob, "", nil},
		{"GET", "/api/jobs/{id}/events", read, "任务", "任务事件（步骤、输出），给页面用", s.jobEvents, "",
			map[string]string{"after": "只要编号大于它的", "limit": "最多几条"}},
		{"GET", "/api/jobs/{id}/logs", read, "任务", "任务日志的文本，分页；默认最后 200 行", s.jobLogs, "",
			map[string]string{"tail": "只要最后几行", "after": "接着上一页的 next 往后读；-1 表示从头读", "limit": "最多几行，默认 500，最多 2000", "failed": "为 1 时只要失败步骤的日志"}},
		{"POST", "/api/jobs/{id}/cancel", deploy, "任务", "取消任务；待审批的任务是撤回", s.cancelJob, "", nil},
		{"POST", "/api/jobs/{id}/approve", page, "任务", "批准待审批的任务，开始执行", s.approveJob, "", nil},
		{"POST", "/api/jobs/{id}/reject", page, "任务", "拒绝待审批的任务", s.rejectJob, "", nil},

		// 导入导出：可能包含密钥明文
		{"POST", "/api/import/history", page, "导入导出", "导入旧工具的部署历史", s.importHistory, "", nil},
		{"POST", "/api/export", page, "导入导出", "导出当前账号的配置", s.exportAll, "", nil},
		{"POST", "/api/import", page, "导入导出", "导入配置", s.importAll, "", nil},
	}
}

// ---------- 访问令牌 ----------

func (s *server) listTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.core(r).ListTokens(r.Context())
	s.respond(w, r, http.StatusOK, list, err)
}

// createToken 创建访问令牌。响应里的 token 是令牌明文，只有这一次。
func (s *server) createToken(w http.ResponseWriter, r *http.Request) {
	var in core.TokenInput
	if !decode(w, r, &in) {
		return
	}
	t, token, err := s.core(r).CreateToken(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"name": t.Name, "level": t.Level, "hint": t.Hint, "createdAt": t.CreatedAt, "token": token})
}

func (s *server) revokeToken(w http.ResponseWriter, r *http.Request) {
	s.ok(w, r, s.core(r).RevokeToken(r.Context(), r.PathValue("name")))
}
