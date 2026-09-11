// Package auth 封装与 HRPAuth 的 token 校验交互。
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TokenResult token 校验结果。
type TokenResult struct {
	UID    int64
	Scopes []string
}

// Verifier 定义 token 校验接口。
type Verifier interface {
	Verify(ctx context.Context, token string) (*TokenResult, error)
}

// JWTVerifier 本地验签 JWT access_token。
// 由于 HRPAuth 使用 OAuth2 Authorization Code + PKCE，且后端拿不到 JWKS 公钥，
// 当前实现采用轻量级 JWT 解析 + issuer 校验 + 过期校验。
// 生产环境应替换为完整 JWKS 验签。
type JWTVerifier struct {
	issuer string
}

// NewJWTVerifier 创建 JWTVerifier。
func NewJWTVerifier(issuer string) *JWTVerifier {
	return &JWTVerifier{issuer: issuer}
}

// Verify 解析 JWT 并校验基础字段（iss/exp），提取 sub 作为 UID。
func (v *JWTVerifier) Verify(ctx context.Context, token string) (*TokenResult, error) {
	claims, err := parseJWTClaims(token)
	if err != nil {
		return nil, fmt.Errorf("解析 token 失败: %w", err)
	}

	// 校验 issuer
	if iss, ok := claims["iss"].(string); ok && v.issuer != "" && iss != v.issuer {
		return nil, fmt.Errorf("issuer 不匹配: got %s, want %s", iss, v.issuer)
	}

	// 校验过期
	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return nil, fmt.Errorf("token 已过期")
		}
	}

	// 提取 UID (sub)
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return nil, fmt.Errorf("token 缺少 sub claim")
	}

	uid, err := strconv.ParseInt(sub, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("sub 不是有效的 int64: %s", sub)
	}

	// 提取 scopes
	var scopes []string
	if scope, ok := claims["scope"].(string); ok && scope != "" {
		scopes = strings.Fields(scope)
	} else if scp, ok := claims["scp"].([]any); ok {
		for _, s := range scp {
			if str, ok := s.(string); ok {
				scopes = append(scopes, str)
			}
		}
	}
	if len(scopes) == 0 {
		scopes = []string{"openid"} // 默认
	}

	return &TokenResult{
		UID:    uid,
		Scopes: scopes,
	}, nil
}

// RequireScope 检查 tokenResult 中是否包含指定 scope。
func RequireScope(result *TokenResult, required string) bool {
	for _, s := range result.Scopes {
		if s == required {
			return true
		}
	}
	return false
}

// IsTokenExpiredError 判断错误是否为 token 过期。
func IsTokenExpiredError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "token 已过期")
}

// parseJWTClaims 解析 JWT payload（不验证签名，仅解码 claims）。
func parseJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("base64 解码失败: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	return claims, nil
}
