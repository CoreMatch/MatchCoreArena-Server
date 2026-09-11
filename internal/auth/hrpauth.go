// Package auth 封装与 HRPAuth 的 token 校验交互。
package auth

import (
	"context"
	"fmt"
	"strconv"

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
}

// HRPAuthVerifier 通过 HRPAuth 校验 token。
// 骨架阶段使用 MockVerifier；实现期替换为真实 HRPAuth 调用。
type HRPAuthVerifier struct {
	// HRPEndpoint 是 HRPAuth 的地址（如 http://localhost:8080）。
	HRPEndpoint string
	// Redis 用于缓存 token 校验结果。
	Redis *goredis.Client
}

// Verify 校验 token（骨架期 stub，始终通过）。
func (v *HRPAuthVerifier) Verify(ctx context.Context, token string) (*TokenResult, error) {
	// TODO: 实现期：
	//   1. 先查 Redis 缓存（key: "mca:token:" + token）
	//   2. 缓存未命中则调用 HRPAuth 的 /oauth/token introspection 或内省接口
	//   3. 成功后写入 Redis 缓存（TTL 与 HRPAuth token 有效期一致）
	return &TokenResult{
		UID:    1,
		Scopes: []string{"user"},
	}, nil
}

// MockVerifier 开发/骨架阶段用的 mock 校验器。
// 固定返回 uid=1, scopes=["user"]，用于绕过真实 HRPAuth 调用。
type MockVerifier struct{}

// Verify 始终通过，返回固定 uid=1。
func (m *MockVerifier) Verify(ctx context.Context, token string) (*TokenResult, error) {
	return &TokenResult{
		UID:    1,
		Scopes: []string{"user"},
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

// UIDFromRedis 从 Redis 中根据 token 获取缓存的 uid。
func UIDFromRedis(ctx context.Context, rdb *goredis.Client, token string) (int64, error) {
	val, err := rdb.Get(ctx, "mca:token:"+token).Result()
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

// CacheInRedis 将 token->uid 映射写入 Redis 缓存。
func CacheInRedis(ctx context.Context, rdb *goredis.Client, token string, uid int64, ttlSec int) error {
	key := fmt.Sprintf("mca:token:%s", token)
	return rdb.Set(ctx, key, uid, 0).Err()
}
