package builtin

import (
	"encoding/json"
	"testing"
)

func TestBuiltinRegistry(t *testing.T) {
	r, err := Registry()
	if err != nil {
		t.Fatalf("内置部署方式的声明有误：%v", err)
	}
	c := r.Catalog()
	if len(c.Drivers) != 4 {
		t.Fatalf("内置部署方式 %d 种，期望 4 种", len(c.Drivers))
	}
	for _, name := range []string{"gobinary", "static", "image", "workers"} {
		if _, ok := r.Get(name); !ok {
			t.Fatalf("缺少部署方式 %s", name)
		}
	}
	if _, err := json.Marshal(c); err != nil {
		t.Fatalf("Catalog 无法序列化：%v", err)
	}
}
