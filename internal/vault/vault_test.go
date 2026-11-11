package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	v, err := New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := v.Encrypt("秘密")
	b, _ := v.Encrypt("秘密")
	if a == b {
		t.Fatal("同样的明文每次加密结果应不同")
	}
	if !IsEncrypted(a) {
		t.Fatal("应识别为密文")
	}
	got, err := v.Decrypt(a)
	if err != nil || got != "秘密" {
		t.Fatalf("Decrypt = %q %v", got, err)
	}

	other, _ := New(bytes.Repeat([]byte{2}, 32))
	if _, err := other.Decrypt(a); err == nil {
		t.Fatal("主密钥不对时应解密失败")
	}
	if _, err := v.Decrypt("plain"); err == nil {
		t.Fatal("非密文应报错")
	}
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("主密钥长度不对应报错")
	}
}

func TestOpenWithFile(t *testing.T) {
	dir := t.TempDir()
	v1, where, err := Open(dir, "acct", nil)
	if err != nil {
		t.Fatal(err)
	}
	if where != filepath.Join(dir, KeyFile) {
		t.Fatalf("存放位置 %s", where)
	}
	st, err := os.Stat(filepath.Join(dir, KeyFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("master.key 权限应为 600：%v %v", st.Mode(), err)
	}
	enc, _ := v1.Encrypt("x")
	v2, _, err := Open(dir, "acct", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v2.Decrypt(enc); err != nil || got != "x" {
		t.Fatal("再次打开应读到同一个主密钥")
	}
}

type memKeychain struct {
	m      map[string]string
	setErr error
}

func (k *memKeychain) Get(a string) (string, bool, error) { v, ok := k.m[a]; return v, ok, nil }
func (k *memKeychain) Set(a, v string) error {
	if k.setErr != nil {
		return k.setErr
	}
	k.m[a] = v
	return nil
}

func TestOpenWithKeychain(t *testing.T) {
	dir := t.TempDir()
	kc := &memKeychain{m: map[string]string{}}
	v1, where, err := Open(dir, "acct", kc)
	if err != nil || where != "钥匙串" {
		t.Fatalf("Open = %s %v", where, err)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("用钥匙串时不应写 master.key")
	}
	enc, _ := v1.Encrypt("x")
	v2, _, _ := Open(dir, "acct", kc)
	if got, _ := v2.Decrypt(enc); got != "x" {
		t.Fatal("再次打开应读到钥匙串里的主密钥")
	}
}

func TestOpenFallsBackToFileWhenKeychainFails(t *testing.T) {
	dir := t.TempDir()
	kc := &memKeychain{m: map[string]string{}, setErr: errors.New("拒绝")}
	_, where, err := Open(dir, "acct", kc)
	if err != nil || where != filepath.Join(dir, KeyFile) {
		t.Fatalf("钥匙串写入失败时应改用文件：%s %v", where, err)
	}
}
