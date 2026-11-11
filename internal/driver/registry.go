package driver

import (
	"fmt"
	"regexp"
)

// Registry 是已注册的部署方式。
type Registry struct {
	list   []Driver
	byName map[string]Driver
}

// Catalog 是返回给前端的全部声明：分组、通用参数、各部署方式。
type Catalog struct {
	Sections []Section `json:"sections"`
	Common   []Field   `json:"common"`
	Drivers  []Spec    `json:"drivers"`
}

// NewRegistry 注册部署方式，并检查每份声明是否正确（键重复、分组不存在、显示条件无效等）。
func NewRegistry(drivers ...Driver) (*Registry, error) {
	r := &Registry{byName: map[string]Driver{}}
	common := CommonFields()
	if err := checkFields("通用参数", common, true); err != nil {
		return nil, err
	}
	for _, d := range drivers {
		s := d.Spec()
		if !nameRe.MatchString(s.Name) {
			return nil, fmt.Errorf("部署方式标识 %q 无效", s.Name)
		}
		if _, dup := r.byName[s.Name]; dup {
			return nil, fmt.Errorf("部署方式 %s 重复注册", s.Name)
		}
		if s.Title == "" || s.Summary == "" || s.DefaultCheck == "" {
			return nil, fmt.Errorf("部署方式 %s 缺少名称、说明或默认检查", s.Name)
		}
		for _, k := range s.Omit {
			if !Omittable[k] {
				return nil, fmt.Errorf("部署方式 %s 不能去掉通用参数 %q", s.Name, k)
			}
		}
		if err := checkSteps(s); err != nil {
			return nil, err
		}
		// 部署方式的参数和通用参数在同一个表单里，键不能重复，显示条件可以引用通用参数
		if err := checkFields("部署方式 "+s.Name, FieldsOf(s), true); err != nil {
			return nil, err
		}
		r.byName[s.Name] = d
		r.list = append(r.list, d)
	}
	return r, nil
}

// Get 按标识取部署方式。
func (r *Registry) Get(name string) (Driver, bool) {
	d, ok := r.byName[name]
	return d, ok
}

// Catalog 返回给前端的全部声明。
func (r *Registry) Catalog() Catalog {
	c := Catalog{Sections: Sections, Common: CommonFields(), Drivers: []Spec{}}
	for _, d := range r.list {
		s := d.Spec()
		s.Steps = s.DeploySteps() // 前端用来显示还没开始的步骤
		c.Drivers = append(c.Drivers, s)
	}
	return c
}

// checkSteps 检查自定义的部署步骤：只能是已知的步骤，按固定顺序，切换和健康检查不能去掉。
func checkSteps(s Spec) error {
	if len(s.Steps) == 0 {
		return nil
	}
	pos := map[string]int{}
	for i, st := range DefaultSteps {
		pos[st.Key] = i
	}
	last := -1
	for _, st := range s.Steps {
		i, ok := pos[st.Key]
		if !ok {
			return fmt.Errorf("部署方式 %s 的步骤 %q 不存在", s.Name, st.Key)
		}
		if i <= last {
			return fmt.Errorf("部署方式 %s 的步骤 %s 重复或顺序不对", s.Name, st.Key)
		}
		if st.Label == "" {
			return fmt.Errorf("部署方式 %s 的步骤 %s 缺少名称", s.Name, st.Key)
		}
		last = i
	}
	if s.StepLabel(StepActivate) == "" || s.StepLabel(StepHealth) == "" {
		return fmt.Errorf("部署方式 %s 不能去掉切换和健康检查步骤", s.Name)
	}
	return nil
}

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	keyRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// 选项来源：servers、detect:<名称>、credentials:<凭据种类>
	optionsFromRe = regexp.MustCompile(`^(servers|detect:[a-z][a-z0-9_]*|credentials:[a-z][a-z0-9]*)$`)
)

var validTypes = map[FieldType]bool{
	TypeText: true, TypeTextarea: true, TypeNumber: true, TypeBool: true, TypeSelect: true,
	TypeList: true, TypeKeyValue: true, TypeCode: true, TypeRecords: true,
}

// checkFields 检查同一层级的字段声明；topLevel 为 true 时要求填写分组。
func checkFields(where string, fields []Field, topLevel bool) error {
	sections := map[string]bool{}
	for _, s := range Sections {
		sections[s.Key] = true
	}
	byKey := map[string]Field{}
	for _, f := range fields {
		if !keyRe.MatchString(f.Key) {
			return fmt.Errorf("%s：参数键 %q 无效", where, f.Key)
		}
		if _, dup := byKey[f.Key]; dup {
			return fmt.Errorf("%s：参数 %s 重复", where, f.Key)
		}
		byKey[f.Key] = f
	}
	for _, f := range fields {
		at := fmt.Sprintf("%s 的参数 %s", where, f.Key)
		if f.Label == "" {
			return fmt.Errorf("%s 缺少名称", at)
		}
		if !validTypes[f.Type] {
			return fmt.Errorf("%s 的类型 %q 无效", at, f.Type)
		}
		if topLevel && !sections[f.Section] {
			return fmt.Errorf("%s 的分组 %q 不存在", at, f.Section)
		}
		if !topLevel && f.Section != "" {
			return fmt.Errorf("%s 是子字段，不能填写分组", at)
		}
		if f.Pattern != "" {
			if _, err := regexp.Compile(f.Pattern); err != nil {
				return fmt.Errorf("%s 的正则无效：%w", at, err)
			}
			if f.PatternHint == "" {
				return fmt.Errorf("%s 有正则但缺少不匹配时的提示", at)
			}
		}
		if (f.Min != nil || f.Max != nil) && f.Type != TypeNumber {
			return fmt.Errorf("%s 不是数字，不能设置范围", at)
		}
		if f.OptionsFrom != "" && !optionsFromRe.MatchString(f.OptionsFrom) {
			return fmt.Errorf("%s 的选项来源 %q 无效", at, f.OptionsFrom)
		}
		switch f.Type {
		case TypeSelect:
			if len(f.Options) == 0 && f.OptionsFrom == "" {
				return fmt.Errorf("%s 是下拉，但没有选项", at)
			}
			if f.Default != nil {
				def, ok := f.Default.(string)
				if !ok || !hasOption(f.Options, def) {
					return fmt.Errorf("%s 的默认值 %v 不在选项中", at, f.Default)
				}
			}
		case TypeRecords:
			if !topLevel {
				return fmt.Errorf("%s：records 不能嵌套", at)
			}
			if len(f.Fields) == 0 {
				return fmt.Errorf("%s 是多条记录，但没有子字段", at)
			}
			if err := checkFields(at, f.Fields, false); err != nil {
				return err
			}
		case TypeCode:
			if f.Language == "" {
				return fmt.Errorf("%s 是代码编辑器，但没有指定语法", at)
			}
		}
		if f.Type != TypeRecords && len(f.Fields) > 0 {
			return fmt.Errorf("%s 不是多条记录，不能有子字段", at)
		}
		if err := checkDefault(at, f); err != nil {
			return err
		}
		if c := f.ShowIf; c != nil {
			target, ok := byKey[c.Key]
			if !ok || c.Key == f.Key {
				return fmt.Errorf("%s 的显示条件引用了不存在的参数 %q", at, c.Key)
			}
			if len(c.In) == 0 && !c.NotEmpty {
				return fmt.Errorf("%s 的显示条件为空", at)
			}
			if target.Type == TypeSelect && target.OptionsFrom == "" {
				for _, v := range c.In {
					if !hasOption(target.Options, v) {
						return fmt.Errorf("%s 的显示条件值 %q 不在 %s 的选项中", at, v, c.Key)
					}
				}
			}
		}
	}
	return nil
}

func checkDefault(at string, f Field) error {
	if f.Default == nil {
		return nil
	}
	ok := false
	switch f.Type {
	case TypeText, TypeTextarea, TypeSelect, TypeCode:
		_, ok = f.Default.(string)
	case TypeNumber:
		switch f.Default.(type) {
		case int, float64:
			ok = true
		}
	case TypeBool:
		_, ok = f.Default.(bool)
	case TypeList:
		_, ok = f.Default.([]string)
	case TypeKeyValue:
		_, ok = f.Default.(map[string]string)
	}
	if !ok {
		return fmt.Errorf("%s 的默认值类型 %T 和参数类型 %s 不符", at, f.Default, f.Type)
	}
	return nil
}

func hasOption(opts []Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}
