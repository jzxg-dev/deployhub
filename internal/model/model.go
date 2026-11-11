// Package model 是各层共用的数据结构。接口返回的 JSON 也直接用它们。
package model

import "time"

// Server 是部署目标服务器。Name 创建后不能修改，应用参数里用它引用服务器。
type Server struct {
	ID      int64  `json:"-"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	KeyPath string `json:"keyPath"`
	// KeyCredential 是平台保存的 SSH 私钥（种类为 sshkey 的凭据）的名称；不为空时用它登录，不用 KeyPath。
	KeyCredential string `json:"keyCredential"`
	// KeyFile 是 KeyCredential 的私钥在本机的临时文件，连接前由业务层填写，不保存、不返回。
	KeyFile    string       `json:"-"`
	SSHOptions []string     `json:"sshOptions"`
	Facts      *ServerFacts `json:"facts,omitempty"`
	CheckedAt  *time.Time   `json:"checkedAt,omitempty"`
	CreatedAt  time.Time    `json:"createdAt"`
	UpdatedAt  time.Time    `json:"updatedAt"`
	// AppCount 是部署到这台服务器的应用数，只在列表里填写。
	AppCount int `json:"appCount"`
}

// ServerFacts 是测试连接时检测到的服务器环境。
type ServerFacts struct {
	Machine        string `json:"machine"`        // uname -m 原值，如 x86_64
	Arch           string `json:"arch"`           // 对应的 Go 架构：amd64、arm64，认不出时为空
	OS             string `json:"os"`             // /etc/os-release 的 PRETTY_NAME
	User           string `json:"user"`           // 登录用户
	Docker         bool   `json:"docker"`         // docker 可用（含权限）
	DockerVersion  string `json:"dockerVersion"`  // docker 服务端版本
	Compose        bool   `json:"compose"`        // docker compose 插件可用
	Rsync          bool   `json:"rsync"`          // 有 rsync，传输可以增量
	Systemd        bool   `json:"systemd"`        // 有 systemctl
	SudoNoPassword bool   `json:"sudoNoPassword"` // sudo -n 可用
}

// Secret 是一项密钥或变量。值加密保存，列表和详情都不返回值。
type Secret struct {
	ID          int64     `json:"-"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// UsedBy 是引用了这项密钥的应用（项目/应用），只在列表里填写。
	UsedBy []string `json:"usedBy"`
}

// Credential 是一组登录信息，如 Cloudflare 的账号 ID 和 API Token。Name 创建后不能修改，应用参数里用它引用。
type Credential struct {
	ID    int64  `json:"-"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // 凭据种类，如 cloudflare
	Title string `json:"title"`
	// Values 是不敏感的字段（如账号 ID）；敏感字段（如 API Token）加密保存，不返回。
	Values map[string]string `json:"values"`
	// Secrets 是已经保存了值的敏感字段。
	Secrets   []string   `json:"secrets"`
	Info      []InfoItem `json:"info"` // 最近一次验证的结果
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	// UsedBy 是选择了这个凭据的应用（项目/应用），只在列表里填写。
	UsedBy []string `json:"usedBy"`
}

// InfoItem 是一条显示用的信息。
type InfoItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Project 把相关的应用放在一起。
type Project struct {
	ID          int64     `json:"-"`
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	AppCount    int       `json:"appCount"`
}

// App 是一个独立部署的应用。Params 是部署方式参数（键见 driver 声明），不含 driver 本身。
type App struct {
	ID        int64          `json:"-"`
	ProjectID int64          `json:"-"`
	Project   string         `json:"project"`
	Name      string         `json:"name"`
	Title     string         `json:"title"`
	Driver    string         `json:"driver"`
	ServerID  *int64         `json:"-"`
	Server    string         `json:"server"`
	Params    map[string]any `json:"params"`
	Revision  int            `json:"revision"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

// AppRevision 是应用某次保存时的参数。
type AppRevision struct {
	Revision  int            `json:"revision"`
	Params    map[string]any `json:"params"`
	CreatedAt time.Time      `json:"createdAt"`
}

// Job 是一次部署或回滚任务。
type Job struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"` // deploy / rollback
	// Status：pending_approval 待审批 / queued / running / success / failed / rolled_back / canceled / rejected 已拒绝
	Status string `json:"status"`
	// Initiator 是发起方：page（页面），或 token:<令牌名称>（访问令牌，即 AI 和脚本）
	Initiator string `json:"initiator"`
	// IdempotencyKey 是发起时带的幂等键，同一个键重复提交时返回原来的任务。
	IdempotencyKey string `json:"-"`
	// ContinueOnError 表示批量部署时某个应用失败后继续部署剩下的。
	ContinueOnError bool       `json:"continueOnError"`
	Error           string     `json:"error"`
	CreatedAt       time.Time  `json:"createdAt"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	Units           []*JobUnit `json:"units"`
}

// JobUnit 是任务里的一个应用。
type JobUnit struct {
	ID         int64      `json:"-"`
	JobID      int64      `json:"jobId"`
	Seq        int        `json:"seq"`
	AppID      *int64     `json:"-"`
	Project    string     `json:"project"`
	App        string     `json:"app"`
	Title      string     `json:"title"`
	Driver     string     `json:"driver"`
	Server     string     `json:"server"`
	Revision   int        `json:"revision"`
	Target     string     `json:"target,omitempty"`
	Version    string     `json:"version"`
	Previous   string     `json:"previous"`
	Commit     string     `json:"commit"`
	Status     string     `json:"status"` // pending / running / success / failed / rolled_back / canceled / skipped
	Step       string     `json:"step"`
	Error      string     `json:"error"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// JobEvent 是任务执行中的一条事件。
type JobEvent struct {
	ID      int64     `json:"id"`
	JobID   int64     `json:"jobId"`
	UnitSeq *int      `json:"unit,omitempty"`
	Time    time.Time `json:"time"`
	Type    string    `json:"type"` // step_start / step_done / step_fail / output / info / warn / error
	Step    string    `json:"step,omitempty"`
	Message string    `json:"message"`
}

// User 是一个登录账号。项目、服务器、密钥、凭据、部署记录都按账号隔离，管理员也看不到别的账号的数据。
type User struct {
	ID       int64  `json:"-"`
	Username string `json:"username"`
	// Admin 为 true 时可以管理账号（新建、重置密码、停用、删除）。
	Admin       bool       `json:"admin"`
	Disabled    bool       `json:"disabled"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
	// ProjectCount 是账号下的项目数，只在账号列表里填写。
	ProjectCount int `json:"projectCount"`
}

// 访问令牌的权限级别，从低到高。
const (
	LevelRead   = "read"   // 只读：查看状态、日志、历史、部署计划
	LevelDeploy = "deploy" // 可部署：再加部署、回滚、取消
	LevelAdmin  = "admin"  // 管理：再加新建和修改项目、应用、服务器
)

// LevelRank 返回权限级别的高低，不认识的级别是 0。
func LevelRank(level string) int {
	switch level {
	case LevelRead:
		return 1
	case LevelDeploy:
		return 2
	case LevelAdmin:
		return 3
	}
	return 0
}

// AccessToken 是一个访问令牌，给 AI 和脚本调用 HTTP API、MCP 用。令牌明文不在这里，页面上要看时单独读取。
type AccessToken struct {
	ID         int64      `json:"-"`
	Name       string     `json:"name"`
	Level      string     `json:"level"`
	Hint       string     `json:"hint"` // 令牌开头的几个字符
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	// Revealable 表示页面上能再次查看令牌；支持查看之前创建的令牌库里只有哈希，是 false
	Revealable bool `json:"revealable"`
}
