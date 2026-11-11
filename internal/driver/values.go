package driver

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// FieldError 是定位到具体参数的校验错误。Field 是参数路径，如 main、config_files.0.name。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Clean 按声明整理并校验参数，返回要保存的值和错误。和前端 schema.ts 的规则一致，以这里为准。
//
//   - 没传的参数用默认值；传了空值的：有默认值的保留空值（表示有意清空，如不排除任何文件），没有默认值的去掉（部署时用自动值）。
//   - 不显示的参数（显示条件不满足）不保存。
//   - 不认识的参数报错，避免拼错的参数悄悄不生效。
//   - kv 保存为 map[string]string，list 为 []string，records 为 []map[string]any，number 为整数。
func Clean(fields []Field, raw map[string]any) (map[string]any, []FieldError) {
	return clean(fields, raw, "")
}

func clean(fields []Field, raw map[string]any, prefix string) (map[string]any, []FieldError) {
	var errs []FieldError
	byKey := map[string]Field{}
	for _, f := range fields {
		byKey[f.Key] = f
	}
	var unknown []string
	for k := range raw {
		if _, ok := byKey[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		errs = append(errs, FieldError{Field: prefix + k, Message: "不认识的参数"})
	}

	// 先补默认值，显示条件按补完后的值判断
	values := map[string]any{}
	for _, f := range fields {
		if v, ok := raw[f.Key]; ok && v != nil {
			values[f.Key] = v
		} else if f.Default != nil {
			values[f.Key] = f.Default
		}
	}

	out := map[string]any{}
	for _, f := range fields {
		if !visible(f, values) {
			continue
		}
		path := prefix + f.Key
		v, present := values[f.Key]
		norm, empty, err := normalize(f, v, path)
		if err != nil {
			errs = append(errs, err...)
			continue
		}
		if empty {
			if f.Required {
				errs = append(errs, FieldError{Field: path, Message: "请填写" + spaced(f.Label)})
			} else if present && f.Default != nil {
				out[f.Key] = norm
			}
			continue
		}
		if e := checkValue(f, norm, path); e != nil {
			errs = append(errs, *e)
			continue
		}
		out[f.Key] = norm
	}
	return out, errs
}

// visible 判断字段是否显示（显示条件引用同一层级的另一个字段）。
func visible(f Field, values map[string]any) bool {
	c := f.ShowIf
	if c == nil {
		return true
	}
	v := values[c.Key]
	if len(c.In) > 0 {
		s := fmt.Sprint(v)
		if v == nil {
			s = ""
		}
		for _, want := range c.In {
			if s == want {
				return true
			}
		}
		return false
	}
	if c.NotEmpty {
		return !isEmpty(v)
	}
	return true
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case []string:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	case map[string]string:
		return len(x) == 0
	}
	return false
}

// normalize 把 JSON 解出来的值整理成对应类型；empty 表示整理后为空。
func normalize(f Field, v any, path string) (out any, empty bool, errs []FieldError) {
	typeErr := func(want string) []FieldError {
		return []FieldError{{Field: path, Message: "应为" + want}}
	}
	if v == nil {
		return nil, true, nil
	}
	switch f.Type {
	case TypeText, TypeSelect:
		s, ok := v.(string)
		if !ok {
			return nil, false, typeErr("文本")
		}
		s = strings.TrimSpace(s)
		return s, s == "", nil
	case TypeTextarea, TypeCode:
		s, ok := v.(string)
		if !ok {
			return nil, false, typeErr("文本")
		}
		return s, strings.TrimSpace(s) == "", nil
	case TypeNumber:
		var n float64
		switch x := v.(type) {
		case float64:
			n = x
		case int:
			n = float64(x)
		case int64:
			n = float64(x)
		default:
			return nil, false, typeErr("数字")
		}
		if n != math.Trunc(n) || math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, false, typeErr("整数")
		}
		return int64(n), false, nil
	case TypeBool:
		b, ok := v.(bool)
		if !ok {
			return nil, false, typeErr("开关（true 或 false）")
		}
		return b, false, nil
	case TypeList:
		var items []string
		switch x := v.(type) {
		case []string:
			items = x
		case []any:
			for _, it := range x {
				s, ok := it.(string)
				if !ok {
					return nil, false, typeErr("文本列表")
				}
				items = append(items, s)
			}
		default:
			return nil, false, typeErr("文本列表")
		}
		list := []string{}
		seen := map[string]bool{}
		for _, s := range items {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if seen[s] {
				return nil, false, []FieldError{{Field: path, Message: "有重复的项：" + s}}
			}
			seen[s] = true
			list = append(list, s)
		}
		return list, len(list) == 0, nil
	case TypeKeyValue:
		var m map[string]string
		switch x := v.(type) {
		case map[string]string:
			m = x
		case map[string]any:
			m = map[string]string{}
			for k, val := range x {
				s, ok := val.(string)
				if !ok {
					return nil, false, []FieldError{{Field: path, Message: fmt.Sprintf("%s 的值应为文本", k)}}
				}
				m[k] = s
			}
		default:
			return nil, false, typeErr("键值对")
		}
		out := map[string]string{}
		for k, val := range m {
			k = strings.TrimSpace(k)
			if k == "" {
				return nil, false, []FieldError{{Field: path, Message: label(f.KeyLabel, "键") + "不能为空"}}
			}
			out[k] = val
		}
		return out, len(out) == 0, nil
	case TypeRecords:
		items, ok := v.([]any)
		if !ok {
			if typed, ok2 := v.([]map[string]any); ok2 {
				for _, r := range typed {
					items = append(items, r)
				}
			} else {
				return nil, false, typeErr("记录列表")
			}
		}
		rows := []map[string]any{}
		for i, it := range items {
			row, ok := it.(map[string]any)
			if !ok {
				errs = append(errs, FieldError{Field: fmt.Sprintf("%s.%d", path, i), Message: "应为一条记录"})
				continue
			}
			cleaned, e := clean(f.Fields, row, fmt.Sprintf("%s.%d.", path, i))
			errs = append(errs, e...)
			rows = append(rows, cleaned)
		}
		if len(errs) > 0 {
			return nil, false, errs
		}
		return rows, len(rows) == 0, nil
	}
	return nil, false, typeErr("已知类型")
}

// regexCache 缓存编译好的正则，可以被多个请求同时使用。
var regexCache sync.Map

func checkValue(f Field, v any, path string) *FieldError {
	if s, ok := v.(string); ok && f.Pattern != "" && (f.Type == TypeText || f.Type == TypeSelect) {
		var re *regexp.Regexp
		if cached, ok := regexCache.Load(f.Pattern); ok {
			re = cached.(*regexp.Regexp)
		} else {
			re = regexp.MustCompile(f.Pattern)
			regexCache.Store(f.Pattern, re)
		}
		if !re.MatchString(s) {
			return &FieldError{Field: path, Message: f.PatternHint}
		}
	}
	if s, ok := v.(string); ok && f.Type == TypeSelect && len(f.Options) > 0 && !f.AllowCustom && !hasOption(f.Options, s) {
		return &FieldError{Field: path, Message: "不是可选的值：" + s}
	}
	if n, ok := v.(int64); ok {
		if f.Min != nil && float64(n) < *f.Min {
			return &FieldError{Field: path, Message: "不能小于 " + strconv.FormatFloat(*f.Min, 'f', -1, 64)}
		}
		if f.Max != nil && float64(n) > *f.Max {
			return &FieldError{Field: path, Message: "不能大于 " + strconv.FormatFloat(*f.Max, 'f', -1, 64)}
		}
	}
	return nil
}

func label(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// spaced 在以字母或数字开头的名称前加空格，如「请填写 HTTP 地址」。
func spaced(label string) string {
	if label != "" && (label[0] < 0x80) {
		return " " + label
	}
	return label
}

// ---------- ${名称} 引用 ----------

var refRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

// Ref 是参数里的一处 ${名称} 引用。
type Ref struct {
	Name     string // 密钥名称
	Field    string // 出现的参数路径，如 env、config_files.0.name
	Required bool   // 没写默认值（${名称:-默认值}），对应的密钥必须存在
}

// Refs 找出参数里引用的密钥与变量。只在 text、list、kv 的值里展开 ${名称}；
// textarea（命令）和 code（模板、配置文件）里的 ${...} 原样保留，因为它们本身可能用到 shell 或 compose 的变量。
func Refs(fields []Field, values map[string]any) []Ref {
	var refs []Ref
	add := func(path, s string) {
		for _, m := range refRe.FindAllStringSubmatch(s, -1) {
			refs = append(refs, Ref{Name: m[1], Field: path, Required: m[2] == ""})
		}
	}
	var walk func(fields []Field, values map[string]any, prefix string)
	walk = func(fields []Field, values map[string]any, prefix string) {
		for _, f := range fields {
			v := values[f.Key]
			path := prefix + f.Key
			switch f.Type {
			case TypeText:
				if s, ok := v.(string); ok {
					add(path, s)
				}
			case TypeList:
				switch x := v.(type) {
				case []string:
					for _, s := range x {
						add(path, s)
					}
				case []any:
					for _, it := range x {
						if s, ok := it.(string); ok {
							add(path, s)
						}
					}
				}
			case TypeKeyValue:
				var keys []string
				switch x := v.(type) {
				case map[string]string:
					for k := range x {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						add(path, x[k])
					}
				case map[string]any:
					for k := range x {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						if s, ok := x[k].(string); ok {
							add(path, s)
						}
					}
				}
			case TypeRecords:
				switch x := v.(type) {
				case []map[string]any:
					for i, r := range x {
						walk(f.Fields, r, fmt.Sprintf("%s.%d.", path, i))
					}
				case []any:
					for i, it := range x {
						if r, ok := it.(map[string]any); ok {
							walk(f.Fields, r, fmt.Sprintf("%s.%d.", path, i))
						}
					}
				}
			}
		}
	}
	walk(fields, values, "")
	return refs
}

// RefNames 返回引用到的密钥名称（去重、排序）。
func RefNames(refs []Ref) []string {
	seen := map[string]bool{}
	var names []string
	for _, r := range refs {
		if !seen[r.Name] {
			seen[r.Name] = true
			names = append(names, r.Name)
		}
	}
	sort.Strings(names)
	return names
}

// Sensitive 遍历敏感字段（Field.Sensitive）的值，fn 返回替换后的值。用于保存前加密、读取后解密。
func Sensitive(fields []Field, values map[string]any, fn func(string) (string, error)) error {
	for _, f := range fields {
		v, ok := values[f.Key]
		if !ok {
			continue
		}
		if f.Sensitive {
			if s, ok := v.(string); ok && s != "" {
				out, err := fn(s)
				if err != nil {
					return fmt.Errorf("参数 %s：%w", f.Key, err)
				}
				values[f.Key] = out
			}
			continue
		}
		if f.Type == TypeRecords {
			switch x := v.(type) {
			case []map[string]any:
				for _, r := range x {
					if err := Sensitive(f.Fields, r, fn); err != nil {
						return err
					}
				}
			case []any:
				for _, it := range x {
					if r, ok := it.(map[string]any); ok {
						if err := Sensitive(f.Fields, r, fn); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

// Expand 把参数里的 ${名称} 换成密钥的值（范围同 Refs：text、list、kv，以及记录里的这些字段），返回新的参数，不改动原参数。
// ${名称:-默认值} 在密钥不存在时用默认值；没有默认值的密钥不存在时报错。
func Expand(fields []Field, values map[string]any, lookup func(name string) (string, bool)) (map[string]any, error) {
	var firstErr error
	repl := func(s string) string {
		return refRe.ReplaceAllStringFunc(s, func(m string) string {
			sub := refRe.FindStringSubmatch(m)
			if v, ok := lookup(sub[1]); ok {
				return v
			}
			if sub[2] != "" {
				return sub[2][2:] // 去掉 ":-"
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("引用的 ${%s} 不存在，请在「密钥与变量」里添加", sub[1])
			}
			return m
		})
	}
	var expand func(fields []Field, values map[string]any) map[string]any
	expand = func(fields []Field, values map[string]any) map[string]any {
		out := make(map[string]any, len(values))
		for k, v := range values {
			out[k] = v
		}
		for _, f := range fields {
			v, ok := values[f.Key]
			if !ok {
				continue
			}
			switch f.Type {
			case TypeText:
				if s, ok := v.(string); ok {
					out[f.Key] = repl(s)
				}
			case TypeList:
				var list []string
				switch x := v.(type) {
				case []string:
					list = x
				case []any:
					for _, it := range x {
						if s, ok := it.(string); ok {
							list = append(list, s)
						}
					}
				}
				expanded := make([]string, len(list))
				for i, s := range list {
					expanded[i] = repl(s)
				}
				out[f.Key] = expanded
			case TypeKeyValue:
				m := map[string]string{}
				switch x := v.(type) {
				case map[string]string:
					for k, s := range x {
						m[k] = repl(s)
					}
				case map[string]any:
					for k, it := range x {
						if s, ok := it.(string); ok {
							m[k] = repl(s)
						}
					}
				}
				out[f.Key] = m
			case TypeRecords:
				var rows []map[string]any
				switch x := v.(type) {
				case []map[string]any:
					rows = x
				case []any:
					for _, it := range x {
						if r, ok := it.(map[string]any); ok {
							rows = append(rows, r)
						}
					}
				}
				expanded := make([]any, len(rows))
				for i, r := range rows {
					expanded[i] = expand(f.Fields, r)
				}
				out[f.Key] = expanded
			}
		}
		return out
	}
	out := expand(fields, values)
	return out, firstErr
}
