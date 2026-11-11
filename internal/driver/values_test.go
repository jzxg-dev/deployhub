package driver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testFields() []Field {
	return []Field{
		{Key: "name", Label: "应用标识", Section: "basic", Type: TypeText, Required: true, Pattern: `^[a-z][a-z0-9-]*$`, PatternHint: "小写字母开头"},
		{Key: "order", Label: "顺序", Section: "basic", Type: TypeNumber, Default: 100, Min: Num(0), Max: Num(1000)},
		{Key: "mode", Label: "检查方式", Section: "health", Type: TypeSelect, Default: "default",
			Options: []Option{{Value: "default", Label: "默认"}, {Value: "http", Label: "HTTP"}}},
		{Key: "url", Label: "HTTP 地址", Section: "health", Type: TypeText, Required: true, ShowIf: &Condition{Key: "mode", In: []string{"http"}}},
		{Key: "groups", Label: "分组", Section: "basic", Type: TypeList},
		{Key: "exclude", Label: "排除", Section: "build", Type: TypeList, Default: []string{"*.map"}},
		{Key: "env", Label: "环境变量", Section: "runtime", Type: TypeKeyValue, KeyLabel: "变量名"},
		{Key: "cgo", Label: "CGO", Section: "build", Type: TypeBool, Default: false},
		{Key: "files", Label: "配置文件", Section: "files", Type: TypeRecords, Fields: []Field{
			{Key: "name", Label: "文件名", Type: TypeText, Required: true},
			{Key: "source", Label: "来源", Type: TypeSelect, Default: "platform", Options: []Option{{Value: "platform", Label: "平台"}, {Value: "server", Label: "服务器"}}},
			{Key: "content", Label: "内容", Type: TypeCode, Language: "toml", Sensitive: true, ShowIf: &Condition{Key: "source", In: []string{"platform"}}},
		}},
	}
}

// fromJSON 模拟接口收到的 JSON
func fromJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCleanDefaultsAndVisibility(t *testing.T) {
	out, errs := Clean(testFields(), fromJSON(t, `{"name":" open-api ","url":"http://x","groups":["api"," ","api2"],"env":{"A":"1"}}`))
	if len(errs) > 0 {
		t.Fatalf("不应有错误：%v", errs)
	}
	want := map[string]any{
		"name": "open-api", "order": int64(100), "mode": "default", "groups": []string{"api", "api2"},
		"exclude": []string{"*.map"}, "env": map[string]string{"A": "1"}, "cgo": false,
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("Clean =\n%#v\n期望\n%#v", out, want)
	}
	// url 不显示（mode=default），所以不保存
	if _, ok := out["url"]; ok {
		t.Fatal("不显示的参数不应保存")
	}
}

func TestCleanKeepsExplicitEmptyForDefaults(t *testing.T) {
	out, errs := Clean(testFields(), fromJSON(t, `{"name":"a1","exclude":[]}`))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if v, ok := out["exclude"]; !ok || len(v.([]string)) != 0 {
		t.Fatalf("有默认值的参数传了空值，应保留空值：%#v", out["exclude"])
	}
}

func TestCleanErrors(t *testing.T) {
	cases := []struct {
		json, field, msg string
	}{
		{`{}`, "name", "请填写应用标识"},
		{`{"name":"Bad"}`, "name", "小写字母开头"},
		{`{"name":"a1","mode":"http"}`, "url", "请填写 HTTP 地址"},
		{`{"name":"a1","mode":"nope"}`, "mode", "不是可选的值"},
		{`{"name":"a1","order":1.5}`, "order", "整数"},
		{`{"name":"a1","order":2000}`, "order", "不能大于 1000"},
		{`{"name":"a1","order":"5"}`, "order", "数字"},
		{`{"name":"a1","groups":["a","a"]}`, "groups", "重复"},
		{`{"name":"a1","env":{" ":"x"}}`, "env", "变量名不能为空"},
		{`{"name":"a1","cgo":"yes"}`, "cgo", "开关"},
		{`{"name":"a1","typo":1}`, "typo", "不认识的参数"},
		{`{"name":"a1","files":[{"source":"server"}]}`, "files.0.name", "请填写文件名"},
		{`{"name":"a1","files":[{"name":"c","bad":1}]}`, "files.0.bad", "不认识的参数"},
	}
	for _, c := range cases {
		_, errs := Clean(testFields(), fromJSON(t, c.json))
		found := false
		for _, e := range errs {
			if e.Field == c.field && strings.Contains(e.Message, c.msg) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s：期望 %s 报「%s」，实际 %v", c.json, c.field, c.msg, errs)
		}
	}
}

func TestCleanRecords(t *testing.T) {
	out, errs := Clean(testFields(), fromJSON(t, `{"name":"a1","files":[{"name":"config.toml","content":"x=1"},{"name":"b","source":"server","content":"忽略"}]}`))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	rows := out["files"].([]map[string]any)
	if rows[0]["source"] != "platform" || rows[0]["content"] != "x=1" {
		t.Fatalf("第一条 = %#v", rows[0])
	}
	if _, ok := rows[1]["content"]; ok {
		t.Fatal("来源为服务器时不保存内容")
	}
}

func TestRefs(t *testing.T) {
	values := map[string]any{
		"name":   "${APP}",
		"groups": []any{"${G:-api}"},
		"env":    map[string]any{"URL": "${API_URL}/v1", "X": "${API_URL}"},
		"files":  []any{map[string]any{"name": "${FILE}", "content": "${NOT_EXPANDED}"}},
	}
	refs := Refs(testFields(), values)
	if got := RefNames(refs); !reflect.DeepEqual(got, []string{"API_URL", "APP", "FILE", "G"}) {
		t.Fatalf("names = %v（code 里的不展开）", got)
	}
	want := map[string]Ref{
		"APP":  {Name: "APP", Field: "name", Required: true},
		"G":    {Name: "G", Field: "groups", Required: false},
		"FILE": {Name: "FILE", Field: "files.0.name", Required: true},
	}
	for _, r := range refs {
		if w, ok := want[r.Name]; ok && w != r {
			t.Errorf("%s = %+v，期望 %+v", r.Name, r, w)
		}
	}
}

func TestSensitive(t *testing.T) {
	values := map[string]any{"files": []map[string]any{{"name": "c", "content": "secret"}, {"name": "d"}}}
	err := Sensitive(testFields(), values, func(s string) (string, error) { return "enc(" + s + ")", nil })
	if err != nil {
		t.Fatal(err)
	}
	rows := values["files"].([]map[string]any)
	if rows[0]["content"] != "enc(secret)" || rows[0]["name"] != "c" {
		t.Fatalf("只应替换敏感字段：%#v", rows[0])
	}
	if _, ok := rows[1]["content"]; ok {
		t.Fatal("没有值的敏感字段不应补上")
	}
}

func TestBuiltinFieldsCleanable(t *testing.T) {
	// 通用参数的默认值都应能通过校验（必填项除外）
	_, errs := Clean(CommonFields(), map[string]any{"name": "a1", "title": "x", "repo_path": "/r", "branch": "main", "server": "s"})
	if len(errs) > 0 {
		t.Fatalf("通用参数默认值校验失败：%v", errs)
	}
}

func TestExpand(t *testing.T) {
	values := map[string]any{
		"name":   "${APP}",
		"groups": []any{"${G:-api}"},
		"env":    map[string]any{"URL": "${API_URL}/v1"},
		"files":  []any{map[string]any{"name": "${FILE:-config.toml}", "content": "${NOT_EXPANDED}"}},
	}
	secrets := map[string]string{"APP": "pay", "API_URL": "https://x"}
	out, err := Expand(testFields(), values, func(n string) (string, bool) { v, ok := secrets[n]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if out["name"] != "pay" || out["groups"].([]string)[0] != "api" || out["env"].(map[string]string)["URL"] != "https://x/v1" {
		t.Fatalf("out = %#v", out)
	}
	f := out["files"].([]any)[0].(map[string]any)
	if f["name"] != "config.toml" || f["content"] != "${NOT_EXPANDED}" {
		t.Fatalf("files = %#v", f)
	}
	if values["name"] != "${APP}" {
		t.Fatal("不应改动原参数")
	}
	if _, err := Expand(testFields(), map[string]any{"name": "${MISSING}"}, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("没有默认值的密钥不存在时应报错")
	}
}

func TestParseState(t *testing.T) {
	st := ParseState("current=main-b\nrelease=main-b\nrelease=main-a\nlast=2026-11-07 12:00:00\topen-api\tmain-b\tdeploy\tsuccess\tdeploy\n")
	if st.Current != "main-b" || len(st.Releases) != 2 || !strings.Contains(st.Last, "success") {
		t.Fatalf("%+v", st)
	}
	if ParseState("current=.\n").Current != "" {
		t.Fatal("没有 current 时应为空")
	}
}
