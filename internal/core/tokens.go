package core

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"deployhub/internal/auth"
	"deployhub/internal/model"
	"deployhub/internal/store"
)

// TokenInput 是创建访问令牌时提交的内容。
type TokenInput struct {
	Name  string `json:"name"`
	Level string `json:"level"`
}

// TokenPrefix 是访问令牌的开头，方便一眼认出来（也方便扫描工具发现泄露的令牌）。
const TokenPrefix = "dh_"

// ListTokens 返回当前账号的访问令牌（不含令牌本身）。
func (s *Service) ListTokens(ctx context.Context) ([]*model.AccessToken, error) {
	return s.store.ListTokens(ctx)
}

// CreateToken 创建访问令牌，返回令牌的明文。明文只有这一次机会拿到，库里只存哈希。
func (s *Service) CreateToken(ctx context.Context, in TokenInput) (*model.AccessToken, string, error) {
	in.Name = trim(in.Name)
	switch {
	case in.Name == "":
		return nil, "", fieldErr("name", "请填写名称，如 Claude Code")
	case utf8.RuneCountInString(in.Name) > 40:
		return nil, "", fieldErr("name", "名称最多 40 个字符")
	case strings.IndexFunc(in.Name, unicode.IsControl) >= 0:
		return nil, "", fieldErr("name", "名称里不能有换行等控制字符")
	}
	if model.LevelRank(in.Level) == 0 {
		return nil, "", fieldErr("level", "请选择权限：只读、可部署或管理")
	}
	random, err := auth.NewToken()
	if err != nil {
		return nil, "", err
	}
	token := TokenPrefix + random
	t := &model.AccessToken{Name: in.Name, Level: in.Level, Hint: token[:len(TokenPrefix)+4] + "…"}
	if err := s.store.CreateToken(ctx, t, auth.HashToken(token)); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return nil, "", fieldErr("name", "已有同名的访问令牌")
		}
		return nil, "", err
	}
	return t, token, nil
}

// RevokeToken 吊销访问令牌，之后它立即不能再用。
func (s *Service) RevokeToken(ctx context.Context, name string) error {
	return wrapStore(s.store.DeleteToken(ctx, name), "访问令牌")
}
