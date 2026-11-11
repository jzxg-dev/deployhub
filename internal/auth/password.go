package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// 密码用 PBKDF2-HMAC-SHA256 加盐保存，格式：pbkdf2-sha256$<迭代次数>$<盐>$<哈希>（盐和哈希是 base64）。
// 迭代次数写在哈希里，以后调大不影响已有的密码。
const (
	passwordScheme     = "pbkdf2-sha256"
	passwordIterations = 600_000
	passwordSaltSize   = 16
	passwordKeySize    = 32
)

// 同时计算密码哈希的请求数上限：一次计算要占用一个 CPU 核几百毫秒，不限制的话大量登录请求能拖慢整个服务。
var hashSlots = make(chan struct{}, 4)

func derive(password string, salt []byte, iterations int) ([]byte, error) {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return pbkdf2.Key(sha256.New, password, salt, iterations, passwordKeySize)
}

// HashPassword 生成密码的哈希。
func HashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐：%w", err)
	}
	key, err := derive(password, salt, passwordIterations)
	if err != nil {
		return "", fmt.Errorf("计算密码哈希：%w", err)
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", passwordScheme, passwordIterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword 判断密码和哈希是否匹配。哈希格式不对时返回 false。
func VerifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != passwordScheme {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 || iterations > 10_000_000 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := derive(password, salt, iterations)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// BurnPassword 做一次和校验密码同样耗时的计算。用户名不存在时调用，
// 这样登录失败的响应时间不会暴露用户名是否存在。
func BurnPassword(password string) {
	_, _ = derive(password, make([]byte, passwordSaltSize), passwordIterations)
}
