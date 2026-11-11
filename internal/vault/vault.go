// Package vault 加密保存敏感数据（密钥的值、应用配置文件的内容）。
//
// 用 AES-256-GCM 加密，主密钥存在 macOS 钥匙串；钥匙串不可用或用 -no-keychain 启动时，
// 存在数据目录下的 master.key（权限 600）。主密钥丢失后已加密的数据无法恢复。
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	keySize = 32
	prefix  = "v1:"
	// KeyFile 是钥匙串不可用时保存主密钥的文件名（在数据目录下）。
	KeyFile = "master.key"
)

// Vault 加密和解密字符串。
type Vault struct {
	aead cipher.AEAD
}

// New 用给定的 32 字节主密钥创建 Vault。
func New(key []byte) (*Vault, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("主密钥长度应为 %d 字节，实际 %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// Encrypt 加密，返回 v1:<base64(nonce|密文)>。
func (v *Vault) Encrypt(plain string) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := v.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 的结果。
func (v *Vault) Decrypt(s string) (string, error) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return "", errors.New("密文格式不对")
	}
	raw, err := base64.StdEncoding.DecodeString(rest)
	if err != nil || len(raw) < v.aead.NonceSize() {
		return "", errors.New("密文格式不对")
	}
	nonce, sealed := raw[:v.aead.NonceSize()], raw[v.aead.NonceSize():]
	plain, err := v.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", errors.New("解密失败：主密钥不对，或数据已损坏")
	}
	return string(plain), nil
}

// IsEncrypted 判断字符串是不是 Encrypt 的结果。
func IsEncrypted(s string) bool { return strings.HasPrefix(s, prefix) }

// Keychain 读写系统钥匙串里的主密钥。
type Keychain interface {
	Get(account string) (string, bool, error)
	Set(account, value string) error
}

// Open 读取或生成主密钥。account 区分不同的数据目录；keychain 为 nil 时只用文件。
// 返回 Vault 和主密钥的存放位置说明。
func Open(dataDir, account string, keychain Keychain) (*Vault, string, error) {
	file := filepath.Join(dataDir, KeyFile)

	if keychain != nil {
		if v, ok, err := keychain.Get(account); err == nil && ok {
			key, err := hex.DecodeString(strings.TrimSpace(v))
			if err != nil {
				return nil, "", fmt.Errorf("钥匙串里的主密钥格式不对：%w", err)
			}
			vt, err := New(key)
			return vt, "钥匙串", err
		}
	}

	if b, err := os.ReadFile(file); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil {
			return nil, "", fmt.Errorf("%s 格式不对：%w", file, err)
		}
		vt, err := New(key)
		return vt, file, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("读取 %s：%w", file, err)
	}

	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, "", err
	}
	encoded := hex.EncodeToString(key)
	where := file
	if keychain != nil && keychain.Set(account, encoded) == nil {
		where = "钥匙串"
	} else if err := os.WriteFile(file, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, "", fmt.Errorf("保存主密钥到 %s：%w", file, err)
	}
	vt, err := New(key)
	return vt, where, err
}
