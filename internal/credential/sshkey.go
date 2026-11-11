package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"deployhub/internal/driver"
	"deployhub/internal/model"
)

// SSHKey 是 SSH 私钥这种凭据的名称。服务器登录和拉取 git 仓库都可以选它。
const SSHKey = "sshkey"

// SSHKeyField 是私钥内容所在的字段。
const SSHKeyField = "private_key"

var sshKeyKind = Kind{
	Name:  SSHKey,
	Title: "SSH 私钥",
	Help:  "私钥加密保存在平台里，用来登录服务器和拉取 git 仓库，不需要事先放在运行 deployhub 的机器上。验证后会显示对应的公钥，把公钥加到服务器的 ~/.ssh/authorized_keys 或 git 仓库的部署密钥里。",
	Local: true,
	Fields: []driver.Field{
		{
			Key: SSHKeyField, Label: "私钥", Type: driver.TypeTextarea, Required: true, Sensitive: true,
			Placeholder: "-----BEGIN OPENSSH PRIVATE KEY-----\n…\n-----END OPENSSH PRIVATE KEY-----",
			Help:        "整个私钥文件的内容（如 ~/.ssh/id_ed25519），不能带口令。建议给部署单独生成一把：ssh-keygen -t ed25519 -N '' -f deploy_key",
		},
	},
	test: func(ctx context.Context, v map[string]string) ([]model.InfoItem, error) {
		pub, fingerprint, err := InspectSSHKey(ctx, v[SSHKeyField])
		if err != nil {
			return nil, err
		}
		return []model.InfoItem{{Label: "指纹", Value: fingerprint}, {Label: "公钥", Value: pub}}, nil
	},
}

// NormalizeSSHKey 整理粘贴进来的私钥：换行统一成 \n，去掉首尾空白，结尾保留一个换行（OpenSSH 要求）。
func NormalizeSSHKey(key string) string {
	key = strings.ReplaceAll(key, "\r\n", "\n")
	key = strings.ReplaceAll(key, "\r", "\n")
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return key + "\n"
}

// InspectSSHKey 用本机的 ssh-keygen 解析私钥，返回公钥和指纹。私钥格式不对或带口令时返回错误。
// 私钥只短暂写进一个权限 600 的临时文件。
func InspectSSHKey(ctx context.Context, key string) (public, fingerprint string, err error) {
	key = NormalizeSSHKey(key)
	if key == "" {
		return "", "", errors.New("还没有填私钥")
	}
	if !strings.Contains(key, "PRIVATE KEY-----") {
		return "", "", errors.New("这不是私钥：应以 -----BEGIN OPENSSH PRIVATE KEY----- 这样的一行开头（不要填 .pub 公钥文件的内容）")
	}
	f, err := os.CreateTemp("", "deployhub-key-*")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err == nil {
		_, err = f.WriteString(key)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", err
	}
	// -P ""：带口令的私钥直接失败，不等待输入
	out, err := exec.CommandContext(ctx, "ssh-keygen", "-y", "-P", "", "-f", f.Name()).CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", "", errors.New("本机找不到 ssh-keygen 命令")
		}
		msg := strings.ToLower(string(out))
		if strings.Contains(msg, "passphrase") {
			return "", "", errors.New("这把私钥带口令，平台用不了；请换一把没有口令的私钥")
		}
		return "", "", errors.New("私钥格式不对，ssh-keygen 解析不了；请确认粘贴的是完整的私钥文件内容")
	}
	public = strings.TrimSpace(string(out))
	out, err = exec.CommandContext(ctx, "ssh-keygen", "-l", "-f", f.Name()).CombinedOutput()
	if err != nil {
		return public, "", nil
	}
	// 输出形如：256 SHA256:xxxx comment (ED25519)
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) >= 2 {
		fingerprint = parts[1]
		if last := parts[len(parts)-1]; strings.HasPrefix(last, "(") {
			fingerprint = fmt.Sprintf("%s %s", strings.Trim(last, "()"), fingerprint)
		}
	}
	return public, fingerprint, nil
}
