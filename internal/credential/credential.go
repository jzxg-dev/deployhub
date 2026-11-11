// Package credential 定义凭据的种类：成组的登录信息（如 Cloudflare 的账号 ID 和 API Token），
// 部署方式的参数里用 credentials:<种类> 选择。字段声明复用 driver.Field，前端据此生成表单。
package credential

import (
	"context"
	"fmt"

	"deployhub/internal/cloudflare"
	"deployhub/internal/driver"
	"deployhub/internal/model"
)

// Kind 是一种凭据。
type Kind struct {
	Name   string         `json:"name"`
	Title  string         `json:"title"`
	Help   string         `json:"help"`
	Fields []driver.Field `json:"fields"`
	// Local 表示验证只在本机进行、不联网（如解析 SSH 私钥），保存时就验证，格式不对不让保存。
	Local bool `json:"local,omitempty"`
	// test 验证凭据能不能用（只读），返回给页面显示的信息。
	test func(ctx context.Context, v map[string]string) ([]model.InfoItem, error)
}

// Test 验证凭据，只读。
func (k Kind) Test(ctx context.Context, values map[string]string) ([]model.InfoItem, error) {
	return k.test(ctx, values)
}

var kinds = []Kind{cloudflareKind, sshKeyKind}

// Kinds 返回所有凭据种类。
func Kinds() []Kind { return kinds }

// Get 按名称取凭据种类。
func Get(name string) (Kind, bool) {
	for _, k := range kinds {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

var cloudflareKind = Kind{
	Name:  "cloudflare",
	Title: "Cloudflare 账号",
	Help:  "部署 Cloudflare Workers 用。API Token 在 dash.cloudflare.com/profile/api-tokens 创建，不要用 Global API Key。",
	Fields: []driver.Field{
		{
			Key: "account_id", Label: "账号 ID", Type: driver.TypeText, Required: true,
			Placeholder: "32 位十六进制，控制台网址 dash.cloudflare.com/<账号 ID> 里的那一段",
			Pattern:     `^[0-9a-f]{32}$`, PatternHint: "32 位十六进制字符（小写）",
		},
		{
			Key: "api_token", Label: "API Token", Type: driver.TypeText, Required: true, Sensitive: true,
			Pattern: `^\S+$`, PatternHint: "不能包含空格或换行",
			Help: "可以用「Edit Cloudflare Workers」模板创建：需要 Account · Workers Scripts · Edit；绑定自定义域名还需要 Zone · Workers Routes · Edit 和 Zone · DNS · Edit",
		},
	},
	test: func(ctx context.Context, v map[string]string) ([]model.InfoItem, error) {
		c := cloudflare.New(v["account_id"], v["api_token"])
		tok, err := c.VerifyToken(ctx)
		if err != nil {
			return nil, err
		}
		if tok.Status != "active" {
			return nil, fmt.Errorf("API Token 的状态是 %s，不能使用", tok.Status)
		}
		acc, err := c.Account(ctx)
		if err != nil {
			return nil, fmt.Errorf("Token 有效，但访问不了账号 %s：%w", v["account_id"], err)
		}
		scope := map[string]string{"user": "用户令牌", "account": "账号令牌"}[tok.Scope]
		info := []model.InfoItem{{Label: "账号", Value: acc.Name}, {Label: "API Token", Value: "有效（" + scope + "）"}}
		sub, err := c.WorkersSubdomain(ctx)
		switch {
		case err != nil:
			info = append(info, model.InfoItem{Label: "workers.dev", Value: "读取失败（Token 可能没有 Workers 权限）：" + err.Error()})
		case sub == "":
			info = append(info, model.InfoItem{Label: "workers.dev", Value: "没有开通"})
		default:
			info = append(info, model.InfoItem{Label: "workers.dev", Value: sub + ".workers.dev"})
		}
		return info, nil
	},
}
