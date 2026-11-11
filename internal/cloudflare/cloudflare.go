// Package cloudflare 是 Cloudflare 接口的最小客户端：验证 API Token、读取账号，
// 以及 Workers 的版本和部署（读取线上版本、把某个版本设为 100% 流量用于回滚）。
// 发布新版本由 wrangler 完成，这里不上传代码。
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// DefaultBaseURL 是 Cloudflare 接口地址。环境变量 CLOUDFLARE_API_BASE_URL 可以改（和 wrangler 用同一个变量，测试时指向模拟服务）。
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// BaseURL 返回实际使用的接口地址。
func BaseURL() string {
	if v := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return DefaultBaseURL
}

// Client 是某个账号的接口客户端。
type Client struct {
	BaseURL   string
	AccountID string
	Token     string
	HTTP      *http.Client
}

// New 创建客户端。
func New(accountID, token string) *Client {
	return &Client{BaseURL: BaseURL(), AccountID: accountID, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// APIError 是接口返回的错误。
type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	hint := ""
	switch {
	case e.Status == http.StatusUnauthorized || e.Code == 10000 || e.Code == 9109 || e.Code == 1000:
		hint = "：API Token 无效、已过期，或没有这个账号的权限"
	case e.Status == http.StatusForbidden:
		hint = "：API Token 没有这项操作的权限"
	}
	if e.Code != 0 {
		return fmt.Sprintf("Cloudflare 接口返回错误（HTTP %d，代码 %d）%s：%s", e.Status, e.Code, hint, msg)
	}
	return fmt.Sprintf("Cloudflare 接口返回错误（HTTP %d）%s：%s", e.Status, hint, msg)
}

// IsNotFound 表示资源不存在，如 Worker 还没有部署过。
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Code == 10007)
}

type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiMessage    `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// do 发请求并解析 {success, errors, result} 格式的响应，result 放进 out。
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 Cloudflare 接口：%w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("读取 Cloudflare 接口响应：%w", err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		if resp.StatusCode >= 400 {
			return &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(data))}
		}
		return fmt.Errorf("Cloudflare 接口返回的不是 JSON（HTTP %d）", resp.StatusCode)
	}
	if resp.StatusCode >= 400 || !env.Success {
		e := &APIError{Status: resp.StatusCode}
		if len(env.Errors) > 0 {
			e.Code = env.Errors[0].Code
			msgs := make([]string, len(env.Errors))
			for i, m := range env.Errors {
				msgs[i] = m.Message
			}
			e.Message = strings.Join(msgs, "；")
		}
		return e
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("解析 Cloudflare 接口响应：%w", err)
		}
	}
	return nil
}

// TokenInfo 是 API Token 的验证结果。
type TokenInfo struct {
	Status string // active 才能用
	Scope  string // user（用户令牌）或 account（账号令牌）
}

// VerifyToken 验证 API Token：先按用户令牌验证，不是再按账号令牌验证。
func (c *Client) VerifyToken(ctx context.Context) (*TokenInfo, error) {
	var res struct {
		Status string `json:"status"`
	}
	userErr := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &res)
	if userErr == nil {
		return &TokenInfo{Status: res.Status, Scope: "user"}, nil
	}
	if c.AccountID != "" {
		if err := c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(c.AccountID)+"/tokens/verify", nil, &res); err == nil {
			return &TokenInfo{Status: res.Status, Scope: "account"}, nil
		}
	}
	return nil, userErr
}

// Account 是账号信息。
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Account 读取账号，用来确认账号 ID 正确、Token 能访问。
func (c *Client) Account(ctx context.Context) (*Account, error) {
	var a Account
	if err := c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(c.AccountID), nil, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// WorkersSubdomain 返回账号的 workers.dev 子域名，没开通时为空。
func (c *Client) WorkersSubdomain(ctx context.Context) (string, error) {
	var res struct {
		Subdomain string `json:"subdomain"`
	}
	if err := c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(c.AccountID)+"/workers/subdomain", nil, &res); err != nil {
		if IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return res.Subdomain, nil
}

// Version 是 Worker 的一个版本。
type Version struct {
	ID       string `json:"id"`
	Number   int    `json:"number"`
	Metadata struct {
		CreatedOn   string `json:"created_on"`
		AuthorEmail string `json:"author_email"`
		Source      string `json:"source"`
	} `json:"metadata"`
	Annotations map[string]string `json:"annotations"`
}

// Tag 是 wrangler --tag 写的标签。
func (v *Version) Tag() string { return v.Annotations["workers/tag"] }

// Message 是 wrangler --message 写的说明。
func (v *Version) Message() string { return v.Annotations["workers/message"] }

// Deployment 是一次部署：哪些版本各占多少流量。
type Deployment struct {
	ID          string `json:"id"`
	CreatedOn   string `json:"created_on"`
	Source      string `json:"source"`
	AuthorEmail string `json:"author_email"`
	Versions    []struct {
		VersionID  string  `json:"version_id"`
		Percentage float64 `json:"percentage"`
	} `json:"versions"`
	Annotations map[string]string `json:"annotations"`
}

// Main 返回流量最大的版本。
func (d *Deployment) Main() (string, float64) {
	id, max := "", -1.0
	for _, v := range d.Versions {
		if v.Percentage > max {
			id, max = v.VersionID, v.Percentage
		}
	}
	return id, max
}

func (c *Client) scriptPath(script string) string {
	return "/accounts/" + url.PathEscape(c.AccountID) + "/workers/scripts/" + url.PathEscape(script)
}

// Versions 返回可以部署的版本，新的在前。Worker 不存在时返回 IsNotFound 的错误。
func (c *Client) Versions(ctx context.Context, script string) ([]Version, error) {
	var res struct {
		Items []Version `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, c.scriptPath(script)+"/versions?deployable=true", nil, &res); err != nil {
		return nil, err
	}
	sort.SliceStable(res.Items, func(i, j int) bool { return res.Items[i].Metadata.CreatedOn > res.Items[j].Metadata.CreatedOn })
	return res.Items, nil
}

// Deployments 返回部署记录，新的在前。
func (c *Client) Deployments(ctx context.Context, script string) ([]Deployment, error) {
	var res struct {
		Deployments []Deployment `json:"deployments"`
	}
	if err := c.do(ctx, http.MethodGet, c.scriptPath(script)+"/deployments", nil, &res); err != nil {
		return nil, err
	}
	sort.SliceStable(res.Deployments, func(i, j int) bool { return res.Deployments[i].CreatedOn > res.Deployments[j].CreatedOn })
	return res.Deployments, nil
}

// Deploy 把某个已上传的版本设为 100% 流量（回滚用），立即生效。message 最多 100 个字符。
func (c *Client) Deploy(ctx context.Context, script, versionID, message string) error {
	body := map[string]any{
		"strategy": "percentage",
		"versions": []map[string]any{{"version_id": versionID, "percentage": 100}},
		"annotations": map[string]string{
			"workers/message": Truncate(message, MaxMessage),
		},
	}
	return c.do(ctx, http.MethodPost, c.scriptPath(script)+"/deployments", body, nil)
}

// 接口对版本标签和说明的长度限制。
const (
	MaxTag     = 25
	MaxMessage = 100
)

// Truncate 把字符串截到最多 n 个字符（按字符，不截断汉字）。
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
