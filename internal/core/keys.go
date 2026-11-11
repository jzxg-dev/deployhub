package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"deployhub/internal/credential"
)

// 平台保存的 SSH 私钥在库里是加密的，但系统的 ssh 和 git 只能从文件读私钥。
// 所以用到时把私钥写到账号数据目录的 keys/ 下（目录 700、文件 600），文件名是内容的哈希；
// 私钥改了或凭据删了就把对应的文件删掉。

func (s *Service) keyDir() string { return filepath.Join(s.dataDir, "keys") }

func (s *Service) keyPath(private string) string {
	sum := sha256.Sum256([]byte(credential.NormalizeSSHKey(private)))
	return filepath.Join(s.keyDir(), hex.EncodeToString(sum[:16]))
}

// keyFile 返回私钥对应的文件路径，文件不存在时写出来。
func (s *Service) keyFile(private string) (string, error) {
	private = credential.NormalizeSSHKey(private)
	path := s.keyPath(private)
	if b, err := os.ReadFile(path); err == nil && string(b) == private {
		return path, nil
	}
	if err := os.MkdirAll(s.keyDir(), 0o700); err != nil {
		return "", fmt.Errorf("准备私钥文件：%w", err)
	}
	// 先写临时文件再改名：别的 ssh 进程不会读到写了一半的私钥
	tmp, err := os.CreateTemp(s.keyDir(), ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("准备私钥文件：%w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err == nil {
		_, err = tmp.WriteString(private)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return "", fmt.Errorf("准备私钥文件：%w", err)
	}
	return path, nil
}

// removeKeyFile 删除私钥对应的文件（私钥改了或凭据删了）。
func (s *Service) removeKeyFile(private string) {
	if credential.NormalizeSSHKey(private) != "" {
		_ = os.Remove(s.keyPath(private))
	}
}

// credentialKeyFile 把名为 name 的 SSH 私钥凭据准备成文件，返回路径。
func (s *Service) credentialKeyFile(ctx context.Context, name string) (string, error) {
	kind, values, err := s.CredentialValues(ctx, name)
	if err != nil {
		return "", err
	}
	if kind != credential.SSHKey {
		return "", invalid("凭据 " + name + " 不是 SSH 私钥")
	}
	private := values[credential.SSHKeyField]
	if credential.NormalizeSSHKey(private) == "" {
		return "", invalid("凭据 " + name + " 还没有填私钥，请在「凭据」里补上")
	}
	return s.keyFile(private)
}
