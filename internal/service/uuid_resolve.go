package service

import (
	"context"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"MatchCoreArena-Server/internal/auth"
)

// resolveUUID 将参与者 UUID 解析为 uid：先查 Redis 缓存，未命中调用
// HRPAuth 服务端点（服务令牌鉴权），成功后写回缓存。
// 错误原样返回（auth.ErrUserNotFound / auth.ErrServiceAuth 等），
// 由调用方映射为类型化业务错误。
func resolveUUID(ctx context.Context, client *auth.Client, redis *goredis.Client, uuid string) (int64, error) {
	cacheKey := "mca:uuid_to_uid:" + uuid
	if redis != nil {
		if val, err := redis.Get(ctx, cacheKey).Result(); err == nil && val != "" {
			return strconv.ParseInt(val, 10, 64)
		}
	}

	uid, err := client.LookupUserByUUID(ctx, uuid)
	if err != nil {
		return 0, err
	}

	if redis != nil {
		redis.Set(ctx, cacheKey, strconv.FormatInt(uid, 10), 24*time.Hour)
	}
	return uid, nil
}
