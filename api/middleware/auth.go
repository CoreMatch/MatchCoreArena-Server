package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/response"
)

const (
	// ContextKeyUID 当前请求的用户 UID。
	ContextKeyUID = "uid"
	// ContextKeyScopes 当前请求的 token scopes。
	ContextKeyScopes = "scopes"
)

// Auth 返回 Bearer token 校验中间件。
// 校验通过后将 uid、scopes 写入 gin.Context。
func Auth(v auth.Verifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			response.FailCode(c, apperr.CodeOAuthLoginRequired, "缺少 Authorization Bearer token")
			c.Abort()
			return
		}
		token := strings.TrimPrefix(raw, "Bearer ")
		if token == "" {
			response.FailCode(c, apperr.CodeOAuthLoginRequired, "Bearer token 为空")
			c.Abort()
			return
		}

		result, err := v.Verify(c.Request.Context(), token)
		if err != nil {
			response.FailCode(c, apperr.CodeOAuthInvalidGrant, "token 校验失败")
			c.Abort()
			return
		}

		c.Set(ContextKeyUID, result.UID)
		c.Set(ContextKeyScopes, result.Scopes)
		c.Next()
	}
}

// GetUID 从 gin.Context 中取出当前用户 UID（必须在 Auth 中间件之后调用）。
func GetUID(c *gin.Context) int64 {
	v, _ := c.Get(ContextKeyUID)
	uid, _ := v.(int64)
	return uid
}

// GetScopes 从 gin.Context 中取出当前用户 scopes。
func GetScopes(c *gin.Context) []string {
	v, _ := c.Get(ContextKeyScopes)
	scopes, _ := v.([]string)
	return scopes
}
