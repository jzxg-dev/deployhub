// Package driver 定义部署方式：每种部署方式声明自己的参数，前端据此自动生成表单；
// 执行部分见 Executor。
package driver

// FieldType 是参数的输入类型，决定前端用什么控件、值是什么形状。
type FieldType string

const (
	TypeText     FieldType = "text"     // 单行文本，值为 string
	TypeTextarea FieldType = "textarea" // 多行文本，值为 string
	TypeNumber   FieldType = "number"   // 数字，值为 number
	TypeBool     FieldType = "bool"     // 开关，值为 bool
	TypeSelect   FieldType = "select"   // 下拉，值为 string
	TypeList     FieldType = "list"     // 字符串列表，值为 []string
	TypeKeyValue FieldType = "kv"       // 键值对，值为 map[string]string
	TypeCode     FieldType = "code"     // 代码编辑器（配置文件、模板），值为 string
	TypeRecords  FieldType = "records"  // 多条记录，每条由 Fields 描述的子字段组成，值为 []map[string]any
)

// Option 是下拉选项。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
}

// Condition 控制字段何时显示：Key 指向同一层级的另一个字段。
// In 不为空时，该字段的值在 In 中才显示；NotEmpty 为 true 时，该字段有值才显示。
type Condition struct {
	Key      string   `json:"key"`
	In       []string `json:"in,omitempty"`
	NotEmpty bool     `json:"notEmpty,omitempty"`
}

// Field 是一个参数的声明。
type Field struct {
	Key     string    `json:"key"`               // 小写字母和下划线，同一层级唯一
	Label   string    `json:"label"`             // 页面上显示的名称
	Section string    `json:"section,omitempty"` // 所在分组，见 Sections；records 的子字段不填
	Type    FieldType `json:"type"`

	// Required 表示必填；有显示条件的字段只在显示时才要求必填。
	Required bool `json:"required,omitempty"`
	// Default 是固定的默认值，新建时预先填入。
	Default any `json:"default,omitempty"`
	// DefaultHint 说明「留空时用什么」，用于默认值依赖其他信息的情况，如 /opt/<项目>/<应用>。
	DefaultHint string `json:"defaultHint,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`

	Options []Option `json:"options,omitempty"` // select 的固定选项
	// OptionsFrom 表示选项来自运行时数据：servers（已添加的服务器）、detect:<名称>（仓库识别结果）、
	// credentials:<种类>（已添加的某种凭据，如 credentials:cloudflare）。
	OptionsFrom string `json:"optionsFrom,omitempty"`
	// AllowCustom 表示除了选项之外还可以手填，用于识别结果只是建议的情况。
	AllowCustom bool `json:"allowCustom,omitempty"`

	Pattern     string   `json:"pattern,omitempty"`     // 正则，JS 和 Go 都能用的写法
	PatternHint string   `json:"patternHint,omitempty"` // 不匹配时的提示
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`

	Language   string `json:"language,omitempty"`   // code 的语法：ini、yaml、toml、shell
	KeyLabel   string `json:"keyLabel,omitempty"`   // kv 的键名称
	ValueLabel string `json:"valueLabel,omitempty"` // kv 的值名称

	Fields []Field `json:"fields,omitempty"` // records 的子字段

	ShowIf *Condition `json:"showIf,omitempty"`
	// Immutable 表示创建后不能修改。
	Immutable bool `json:"immutable,omitempty"`
	// Sensitive 表示内容加密存储，通过访问令牌读取时不返回。
	Sensitive bool `json:"sensitive,omitempty"`
}

// Section 是表单里的一个分组。
type Section struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
}

// StepLabel 是部署的一个步骤（预检之后）和它在页面上的名称。
type StepLabel struct {
	Key   string `json:"key"` // prepare、build、upload、activate、health、cleanup
	Label string `json:"label"`
}

// 部署步骤，对应 Executor 的方法。
const (
	StepPrepare  = "prepare"
	StepBuild    = "build"
	StepUpload   = "upload"
	StepActivate = "activate"
	StepHealth   = "health"
	StepCleanup  = "cleanup"
)

// DefaultSteps 是部署到服务器的步骤。
var DefaultSteps = []StepLabel{
	{StepPrepare, "准备服务器"},
	{StepBuild, "构建"},
	{StepUpload, "上传"},
	{StepActivate, "切换"},
	{StepHealth, "健康检查"},
	{StepCleanup, "清理"},
}

// Omittable 是部署方式可以去掉的通用参数（Spec.Omit）。
var Omittable = map[string]bool{
	"server": true, "keep_releases": true, "config_files": true, "required_files": true, "required_config": true,
}

// Spec 是一种部署方式的说明和参数声明。
type Spec struct {
	Name        string `json:"name"`        // 标识，存在应用上，不能改
	Title       string `json:"title"`       // 页面上显示的名称
	Summary     string `json:"summary"`     // 一句话说明，用在选择卡片上
	Description string `json:"description"` // 详细说明：怎么构建、传到哪、怎么切换
	Icon        string `json:"icon"`        // lucide 图标名
	// DefaultCheck 说明选择「默认检查」时做什么。
	DefaultCheck string  `json:"defaultCheck"`
	Fields       []Field `json:"fields"`
	// Omit 是这种部署方式用不到的通用参数，只能是 Omittable 里的。不需要服务器的部署方式（如 Workers）去掉 server。
	Omit []string `json:"omit,omitempty"`
	// Steps 是部署步骤，为空时用 DefaultSteps；没列出的步骤不执行。顺序固定，只能改名称和去掉。
	Steps []StepLabel `json:"steps,omitempty"`
}

// UsesServer 表示部署到服务器（需要 SSH）。
func (s Spec) UsesServer() bool { return !s.Omits("server") }

// Omits 表示去掉了某个通用参数。
func (s Spec) Omits(key string) bool {
	for _, k := range s.Omit {
		if k == key {
			return true
		}
	}
	return false
}

// DeploySteps 返回部署步骤。
func (s Spec) DeploySteps() []StepLabel {
	if len(s.Steps) == 0 {
		return DefaultSteps
	}
	return s.Steps
}

// StepLabel 返回步骤的名称；部署方式没有这一步时返回空字符串。
func (s Spec) StepLabel(key string) string {
	for _, st := range s.DeploySteps() {
		if st.Key == key {
			return st.Label
		}
	}
	return ""
}

// FieldsOf 返回部署方式的全部参数：通用参数（去掉 Omit 的）+ 部署方式自己的参数。
func FieldsOf(s Spec) []Field {
	var out []Field
	for _, f := range CommonFields() {
		if !s.Omits(f.Key) {
			out = append(out, f)
		}
	}
	return append(out, s.Fields...)
}

// Driver 是一种部署方式。
type Driver interface {
	Spec() Spec
}

// Num 返回数字的指针，用于 Field 的 Min、Max。
func Num(v float64) *float64 { return &v }
