package static

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// 进程 umask 是 077 时（deployhub 作为服务运行的常见情况），产物也要是 755/644，否则上传后 Nginx 读不到。
func TestCopyTreeIgnoresUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(filepath.Join(src, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.html", "assets/app.js", "assets/app.js.map"} {
		if err := os.WriteFile(filepath.Join(src, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n, _, err := copyTree(src, dst, []string{"*.map"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("复制了 %d 个文件，应该是 2 个（跳过 *.map）", n)
	}
	for path, want := range map[string]os.FileMode{
		"":              0o755,
		"assets":        0o755,
		"index.html":    0o644,
		"assets/app.js": 0o644,
	} {
		info, err := os.Stat(filepath.Join(dst, path))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%q 权限是 %o，应该是 %o", path, got, want)
		}
	}
}
