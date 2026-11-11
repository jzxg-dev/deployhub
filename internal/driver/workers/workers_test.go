package workers

import (
	"strings"
	"testing"
	"unicode/utf8"

	"deployhub/internal/cloudflare"
)

func TestTagMessageLabel(t *testing.T) {
	v := "feature-very-long-branch-name-a1b2c3d-20261108-1530-2"
	if tag := Tag(v); tag != "a1b2c3d-20261108-1530-2" || len(tag) > cloudflare.MaxTag {
		t.Fatalf("标签应去掉分支且不超过 25 个字符：%q", tag)
	}
	msg := Message(v, strings.Repeat("修复回调签名", 30))
	if utf8.RuneCountInString(msg) != cloudflare.MaxMessage || !strings.HasPrefix(msg, v+" ") {
		t.Fatalf("说明应以版本号开头、最多 100 个字符：%q", msg)
	}
	ver := &cloudflare.Version{ID: "0f1e2d3c-aaaa-bbbb-cccc-000000000000", Annotations: map[string]string{"workers/tag": Tag(v), "workers/message": msg}}
	if Label(ver) != v || !Matches(ver, v) || !Matches(ver, "0f1e2d3c") || Matches(ver, "0f1e") {
		t.Fatalf("页面上的版本名应是完整版本号，可以用 ID 前 8 位指定：%q", Label(ver))
	}
	other := &cloudflare.Version{ID: "12345678-0000", Annotations: map[string]string{"workers/message": "控制台改了变量"}}
	if Label(other) != "12345678" {
		t.Fatalf("没有标签的版本用 ID 前 8 位：%q", Label(other))
	}
}
