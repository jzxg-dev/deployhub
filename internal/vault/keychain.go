package vault

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const keychainService = "deployhub"

// MacKeychain 通过 /usr/bin/security 读写 macOS 钥匙串，不需要额外依赖。
type MacKeychain struct{}

// SystemKeychain 返回当前系统可用的钥匙串；不是 macOS 时返回 nil（改用文件）。
func SystemKeychain() Keychain {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if _, err := exec.LookPath("/usr/bin/security"); err != nil {
		return nil
	}
	return MacKeychain{}
}

// Get 读取主密钥，不存在时 ok 为 false。
func (MacKeychain) Get(account string) (string, bool, error) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", keychainService, "-a", account, "-w").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 { // errSecItemNotFound
			return "", false, nil
		}
		return "", false, fmt.Errorf("读取钥匙串：%w", err)
	}
	return strings.TrimSpace(string(out)), true, nil
}

// Set 保存主密钥。通过标准输入传给 security，密钥不出现在进程参数里。
func (MacKeychain) Set(account, value string) error {
	cmd := exec.Command("/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -l %s -w %s\n",
		keychainService, account, "deployhub 主密钥", value))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("写入钥匙串：%v %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
