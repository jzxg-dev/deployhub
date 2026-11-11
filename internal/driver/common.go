package driver

// Sections 是表单分组，按这个顺序显示。通用参数和各部署方式的参数都放进这些分组。
var Sections = []Section{
	{Key: "basic", Label: "基本"},
	{Key: "code", Label: "代码"},
	{Key: "server", Label: "服务器"},
	{Key: "account", Label: "账号", Help: "部署到云平台用的账号，在「凭据」里添加"},
	{Key: "build", Label: "构建"},
	{Key: "transfer", Label: "传输"},
	{Key: "runtime", Label: "运行"},
	{Key: "health", Label: "健康检查", Help: "切换版本后检查服务是否正常，超时视为失败并自动回滚到上一个版本"},
	{Key: "release", Label: "发布"},
	{Key: "files", Label: "配置文件", Help: "随部署上传到服务器的项目配置，如 config.toml"},
	{Key: "precondition", Label: "前置条件", Help: "不满足时在构建前停止"},
	{Key: "advanced", Label: "高级"},
}

// CommonFields 是所有部署方式共有的参数。
func CommonFields() []Field {
	return []Field{
		// 基本
		{
			Key: "name", Label: "应用标识", Section: "basic", Type: TypeText, Required: true, Immutable: true,
			Placeholder: "open-api",
			Pattern:     `^[a-z][a-z0-9-]{0,38}[a-z0-9]$`,
			PatternHint: "小写字母开头，只能包含小写字母、数字和 -，2～40 个字符",
			Help:        "项目内唯一；用于目录名、服务名等默认值，创建后不能修改",
		},
		{
			Key: "title", Label: "名称", Section: "basic", Type: TypeText, Required: true,
			Placeholder: "开放 API",
			Help:        "页面上显示的名称",
		},
		{
			Key: "groups", Label: "分组", Section: "basic", Type: TypeList,
			Placeholder: "api",
			Help:        "用于批量部署，如把几个后端服务都放进 api 分组",
		},
		{
			Key: "order", Label: "部署顺序", Section: "basic", Type: TypeNumber, Default: 100, Min: Num(0), Max: Num(10000),
			Help: "批量部署时从小到大执行",
		},

		// 代码
		{
			Key: "repo_source", Label: "代码来源", Section: "code", Type: TypeSelect, Default: RepoSourceLocal,
			Options: []Option{
				{Value: RepoSourceLocal, Label: "本机目录", Help: "运行 deployhub 的机器上已有的 git 仓库，部署时用里面的代码"},
				{Value: RepoSourceGit, Label: "git 地址", Help: "平台自己克隆，每次部署前拉取分支的最新提交"},
			},
		},
		{
			Key: "repo_path", Label: "仓库目录", Section: "code", Type: TypeText, Required: true,
			Placeholder: "~/code/shop/shop-backend",
			Help:        "运行 deployhub 的机器上 git 仓库的路径；选中后显示当前分支和最新提交，并识别仓库内容",
			ShowIf:      &Condition{Key: "repo_source", In: []string{RepoSourceLocal}},
		},
		{
			Key: "repo_url", Label: "git 地址", Section: "code", Type: TypeText, Required: true,
			Placeholder: "git@github.com:example/shop-api.git",
			Pattern:     `^((https?|ssh)://\S+|[A-Za-z0-9_.-]+@[A-Za-z0-9.-]+:\S+)$`,
			PatternHint: "写成 git@主机:路径.git、ssh://… 或 https://…",
			Help:        "SSH 地址配合下面的 SSH 私钥；私有仓库用 HTTPS 时把令牌放进「密钥与变量」，写成 https://x-access-token:${GIT_TOKEN}@github.com/example/shop-api.git",
			ShowIf:      &Condition{Key: "repo_source", In: []string{RepoSourceGit}},
		},
		{
			Key: "repo_credential", Label: "拉取代码用的 SSH 私钥", Section: "code", Type: TypeSelect,
			OptionsFrom: "credentials:sshkey",
			Help:        "从「凭据」里选一把 SSH 私钥，对应的公钥加到 git 仓库的部署密钥里；公开仓库和 HTTPS 地址不用选",
			ShowIf:      &Condition{Key: "repo_source", In: []string{RepoSourceGit}},
		},
		{
			Key: "branch", Label: "部署分支", Section: "code", Type: TypeText, Required: true,
			DefaultHint: "仓库当前分支",
			Placeholder: "main",
			Pattern:     `^[A-Za-z0-9_][A-Za-z0-9_./-]*$`,
			PatternHint: "分支名只能包含字母、数字和 _ . / -",
			Help:        "本机目录：部署时仓库必须在这个分支上，否则预检不通过。git 地址：部署这个分支的最新提交",
		},
		{
			Key: "sync", Label: "同步方式", Section: "code", Type: TypeSelect, Default: "local_first",
			ShowIf: &Condition{Key: "repo_source", In: []string{RepoSourceLocal}},
			Options: []Option{
				{Value: "local_first", Label: "以本地为准", Help: "先 git fetch；本地领先或分叉就用本地代码，只有落后时才快进；fetch 失败也继续"},
				{Value: "offline", Label: "不联系远程", Help: "直接用本地代码"},
			},
		},
		{
			Key: "dirty", Label: "有未提交的改动时", Section: "code", Type: TypeSelect, Default: "warn",
			ShowIf: &Condition{Key: "repo_source", In: []string{RepoSourceLocal}},
			Options: []Option{
				{Value: "warn", Label: "提醒后可以继续"},
				{Value: "block", Label: "禁止部署"},
			},
		},

		// 服务器
		{
			Key: "server", Label: "目标服务器", Section: "server", Type: TypeSelect, Required: true,
			OptionsFrom: "servers",
			Help:        "从已添加的服务器中选择",
		},

		// 健康检查
		{
			Key: "health_mode", Label: "检查方式", Section: "health", Type: TypeSelect, Default: "default",
			Options: []Option{
				{Value: "default", Label: "默认检查"},
				{Value: "http", Label: "HTTP 地址", Help: "轮询这个地址，返回 2xx 即通过；部署到服务器的在服务器上用 curl 请求，Workers 在本机请求"},
				{Value: "command", Label: "自定义命令", Help: "退出码为 0 即通过；部署到服务器的在服务器上执行，Workers 在本机执行"},
			},
		},
		{
			Key: "health_url", Label: "HTTP 地址", Section: "health", Type: TypeText, Required: true,
			Placeholder: "http://127.0.0.1:8889/ping",
			Pattern:     `^https?://\S+$`,
			PatternHint: "以 http:// 或 https:// 开头",
			ShowIf:      &Condition{Key: "health_mode", In: []string{"http"}},
		},
		{
			Key: "health_command", Label: "自定义命令", Section: "health", Type: TypeTextarea, Required: true,
			Placeholder: "curl -fsS http://127.0.0.1:3000/health | grep -q ok",
			ShowIf:      &Condition{Key: "health_mode", In: []string{"command"}},
		},
		{
			Key: "health_timeout", Label: "超时（秒）", Section: "health", Type: TypeNumber, Default: 60, Min: Num(5), Max: Num(600),
			Help: "超时视为失败，触发自动回滚",
		},

		// 发布
		{
			Key: "keep_releases", Label: "保留版本数", Section: "release", Type: TypeNumber, Default: 5, Min: Num(1), Max: Num(50),
			Help: "服务器上保留的历史版本数（Docker 镜像应用指镜像标签数），当前版本永远不删",
		},
		{
			Key: "ai_approval", Label: "AI 部署需要审批", Section: "release", Type: TypeBool, Default: true,
			Help: "通过访问令牌（API、MCP）发起的部署和回滚，先进入待审批，在页面上批准后才执行；页面上直接发起的不受影响",
		},

		// 配置文件
		{
			Key: "config_files", Label: "上传的配置文件", Section: "files", Type: TypeRecords,
			Help: "上传位置由部署方式决定（Go 服务为工作目录，Docker 镜像为 compose 目录），上传后权限 600",
			Fields: []Field{
				{
					Key: "name", Label: "文件名", Type: TypeText, Required: true, Placeholder: "config.toml",
					Pattern: `^[A-Za-z0-9._-]+$`, PatternHint: "只写文件名，不含目录",
				},
				{
					Key: "source", Label: "来源", Type: TypeSelect, Default: "platform",
					Options: []Option{
						{Value: "platform", Label: "在平台里填写，随部署上传"},
						{Value: "server", Label: "以服务器上已有的为准，只检查存在"},
					},
				},
				{
					Key: "content", Label: "内容", Type: TypeCode, Language: "toml", Sensitive: true,
					ShowIf: &Condition{Key: "source", In: []string{"platform"}},
				},
			},
		},

		// 前置条件
		{
			Key: "required_files", Label: "必需文件", Section: "precondition", Type: TypeList,
			Placeholder: "config.toml",
			Help:        "服务器上必须存在的文件，路径相对上传配置文件的目录",
		},
		{
			Key: "required_config", Label: "必需配置项", Section: "precondition", Type: TypeRecords,
			Help: "toml 文件里必须满足的取值，如 config.toml 的 MCP.enabled 必须是 true",
			Fields: []Field{
				{Key: "file", Label: "文件", Type: TypeText, Required: true, Placeholder: "config.toml"},
				{Key: "key", Label: "键", Type: TypeText, Required: true, Placeholder: "MCP.enabled", Help: "节名.键名，不区分大小写"},
				{Key: "value", Label: "期望值", Type: TypeText, Required: true, Placeholder: "true"},
			},
		},
	}
}

// 代码来源（通用参数 repo_source）。
const (
	RepoSourceLocal = "local" // 运行 deployhub 的机器上已有的仓库目录
	RepoSourceGit   = "git"   // git 地址，平台自己克隆和拉取
)
