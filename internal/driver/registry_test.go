package driver

import (
	"strings"
	"testing"
)

type fake struct{ spec Spec }

func (f fake) Spec() Spec { return f.spec }

func spec(fields ...Field) Spec {
	return Spec{Name: "fake", Title: "测试", Summary: "测试", DefaultCheck: "无", Fields: fields}
}

func TestRegistryRejectsBadSpecs(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{"标识无效", Spec{Name: "Bad-Name", Title: "x", Summary: "x", DefaultCheck: "x"}, "标识"},
		{"缺少说明", Spec{Name: "x"}, "缺少"},
		{"和通用参数重复", spec(Field{Key: "name", Label: "x", Section: "basic", Type: TypeText}), "重复"},
		{"键无效", spec(Field{Key: "Bad", Label: "x", Section: "basic", Type: TypeText}), "无效"},
		{"分组不存在", spec(Field{Key: "a", Label: "x", Section: "nope", Type: TypeText}), "分组"},
		{"类型无效", spec(Field{Key: "a", Label: "x", Section: "basic", Type: "color"}), "类型"},
		{"缺少名称", spec(Field{Key: "a", Section: "basic", Type: TypeText}), "名称"},
		{"下拉没有选项", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeSelect}), "没有选项"},
		{"默认值不在选项中", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeSelect,
			Options: []Option{{Value: "a", Label: "A"}}, Default: "b"}), "不在选项中"},
		{"默认值类型不符", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeNumber, Default: "5"}), "类型"},
		{"正则无效", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeText, Pattern: "(", PatternHint: "x"}), "正则"},
		{"正则缺提示", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeText, Pattern: "^a$"}), "提示"},
		{"非数字设范围", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeText, Min: Num(1)}), "范围"},
		{"代码没有语法", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeCode}), "语法"},
		{"记录没有子字段", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeRecords}), "子字段"},
		{"子字段填了分组", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeRecords,
			Fields: []Field{{Key: "b", Label: "y", Section: "basic", Type: TypeText}}}), "不能填写分组"},
		{"记录嵌套", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeRecords,
			Fields: []Field{{Key: "b", Label: "y", Type: TypeRecords, Fields: []Field{{Key: "c", Label: "z", Type: TypeText}}}}}), "嵌套"},
		{"显示条件引用不存在", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeText,
			ShowIf: &Condition{Key: "missing", NotEmpty: true}}), "不存在"},
		{"显示条件为空", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeText,
			ShowIf: &Condition{Key: "title"}}), "为空"},
		{"显示条件值不在选项中", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeText,
			ShowIf: &Condition{Key: "sync", In: []string{"nope"}}}), "不在"},
		{"选项来源无效", spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeSelect, OptionsFrom: "users"}), "选项来源"},
		{"去掉不能去掉的通用参数", func() Spec { s := spec(); s.Omit = []string{"repo_path"}; return s }(), "不能去掉"},
		{"步骤不存在", func() Spec { s := spec(); s.Steps = []StepLabel{{"publish", "发布"}}; return s }(), "不存在"},
		{"步骤顺序不对", func() Spec {
			s := spec()
			s.Steps = []StepLabel{{StepHealth, "检查"}, {StepActivate, "发布"}}
			return s
		}(), "顺序"},
		{"去掉了健康检查", func() Spec { s := spec(); s.Steps = []StepLabel{{StepActivate, "发布"}}; return s }(), "不能去掉"},
		{"引用了去掉的通用参数", func() Spec {
			s := spec(Field{Key: "a", Label: "x", Section: "basic", Type: TypeText, ShowIf: &Condition{Key: "server", NotEmpty: true}})
			s.Omit = []string{"server"}
			return s
		}(), "不存在"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewRegistry(fake{c.spec})
			if err == nil {
				t.Fatal("应该报错")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误 %q 不含 %q", err, c.want)
			}
		})
	}
}

func TestRegistryAcceptsShowIfOnCommonField(t *testing.T) {
	_, err := NewRegistry(fake{spec(Field{Key: "a", Label: "x", Section: "health", Type: TypeText,
		ShowIf: &Condition{Key: "health_mode", In: []string{"http"}}})})
	if err != nil {
		t.Fatalf("显示条件可以引用通用参数：%v", err)
	}
}

func TestRegistryRejectsDuplicateDriver(t *testing.T) {
	if _, err := NewRegistry(fake{spec()}, fake{spec()}); err == nil {
		t.Fatal("重复注册应该报错")
	}
}

func TestSpecOmitAndSteps(t *testing.T) {
	s := spec(Field{Key: "account", Label: "账号", Section: "account", Type: TypeSelect, OptionsFrom: "credentials:cloudflare"})
	s.Omit = []string{"server", "keep_releases"}
	s.Steps = []StepLabel{{StepPrepare, "检查账号"}, {StepBuild, "构建"}, {StepActivate, "发布"}, {StepHealth, "健康检查"}}
	r, err := NewRegistry(fake{s})
	if err != nil {
		t.Fatalf("声明应该有效：%v", err)
	}
	if s.UsesServer() || !spec().UsesServer() {
		t.Fatal("去掉 server 的部署方式不需要服务器，其他的需要")
	}
	for _, f := range FieldsOf(s) {
		if f.Key == "server" || f.Key == "keep_releases" {
			t.Fatalf("FieldsOf 不应包含去掉的参数 %s", f.Key)
		}
	}
	if s.StepLabel(StepUpload) != "" || s.StepLabel(StepActivate) != "发布" {
		t.Fatal("没列出的步骤不执行，列出的用自定义名称")
	}
	if got := spec().StepLabel(StepPrepare); got != "准备服务器" {
		t.Fatalf("没有自定义步骤时用默认名称，得到 %q", got)
	}
	if c := r.Catalog(); len(c.Drivers[0].Steps) != 4 {
		t.Fatalf("Catalog 应返回部署步骤：%+v", c.Drivers[0].Steps)
	}
}
