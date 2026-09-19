package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// TokenResult token 校验结果。
type TokenResult struct {
	UID    int64
	Scopes []string
}

// Verifier 定义 token 校验接口。
type Verifier interface {
	Verify(ctx context.Context, token string) (*TokenResult, error)
	InvalidateCache(ctx context.Context, token string)
}

// TokenVerifier 通过调用 HRPAuth /user 校验 access_token。
// 使用玩家自身的 Bearer token 调用 HRPAuth，无需 service token。
// 校验结果缓存到 Redis 以减少对 HRPAuth 的调用。
type TokenVerifier struct {
	client           *Client
	redis            *goredis.Client
	maintenanceToken string
	tokenStore       *TokenStore
}

// NewTokenVerifier 创建 TokenVerifier。
func NewTokenVerifier(client *Client, redis *goredis.Client, maintenanceToken string, tokenStore *TokenStore) *TokenVerifier {
	return &TokenVerifier{
		client:           client,
		redis:            redis,
		maintenanceToken: maintenanceToken,
		tokenStore:       tokenStore,
	}
}

// Verify 通过 HRPAuth /user 校验 token 并提取 uid。
// 结果缓存5分钟。
func (v *TokenVerifier) Verify(ctx context.Context, token string) (*TokenResult, error) {
	// 0. 校验是否为运维超级凭据
	if v.maintenanceToken != "" && token == v.maintenanceToken {
		return &TokenResult{
			UID:    -1, // 运维专用 UID
			Scopes: []string{"maintenance"},
		}, nil
	}

	// 0.1 校验是否为高权限服务凭据 (tokens.yaml)
	if v.tokenStore != nil {
		if st, ok := v.tokenStore.FindByToken(token); ok {
			return &TokenResult{
				UID:    st.UID,
				Scopes: st.Scopes,
			}, nil
		}
	}

	// 1. Redis 缓存
	cacheKey := "mca:token_verify:" + token
	if v.redis != nil {
		if cached, err := v.redis.Get(ctx, cacheKey).Result(); err == nil && cached != "" {
			var result TokenResult
			if err := json.Unmarshal([]byte(cached), &result); err == nil {
				return &result, nil
			}
		}
	}

	// 2. 调用 HRPAuth /user
	user, err := v.client.GetUser(token)
	if err != nil {
		if errors.Is(err, ErrTokenExpired) {
			return nil, fmt.Errorf("token 已过期")
		}
		return nil, fmt.Errorf("token 校验失败: %w", err)
	}

	result := &TokenResult{
		UID:    user.UID,
		Scopes: []string{"user.read"}, // 默认 scope，HRPAuth OAuth2 token 保证含此 scope
	}

	// 3. 写入缓存
	if v.redis != nil {
		data, _ := json.Marshal(result)
		v.redis.Set(ctx, cacheKey, data, 5*time.Minute)
	}

	return result, nil
}

// IsTokenExpiredError 判断错误是否为 token 过期。
func IsTokenExpiredError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrTokenExpired)
}

// InvalidateCache 删除指定 token 的 Redis 校验缓存。
// 用于注销后立即失效 token，避免缓存导致已吊销 token 仍可通过鉴权。
func (v *TokenVerifier) InvalidateCache(ctx context.Context, token string) {
	if v.redis != nil {
		v.redis.Del(ctx, "mca:token_verify:"+token)
	}
}
