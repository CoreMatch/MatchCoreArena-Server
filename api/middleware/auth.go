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
	// ContextKeyAccessToken 当前请求的 access_token（用于 logout 等场景）。
	ContextKeyAccessToken = "_access_token"
)

// Auth 返回 Bearer token 校验中间件。
// 支持两种方式获取 token：
//   - Authorization: Bearer <token>
//   - Query 参数 access_token（仅用于 SSE 等无法设置 header 的场景）
//
// 校验通过后将 uid、scopes 写入 gin.Context。
// token 过期时返回401 + oauth_token_expired，由客户端自行调用 /api/auth/refresh。
func Auth(v auth.Verifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			response.FailCode(c, apperr.CodeOAuthLoginRequired, "缺少 Authorization Bearer token")
			c.Abort()
			return
		}

		result, err := v.Verify(c.Request.Context(), token)
		if err != nil {
			if auth.IsTokenExpiredError(err) {
				response.FailCode(c, apperr.CodeOAuthTokenExpired, "token 已过期，请刷新")
				c.Abort()
				return
			}
			response.FailCode(c, apperr.CodeOAuthInvalidGrant, "token 校验失败")
			c.Abort()
			return
		}

		c.Set(ContextKeyUID, result.UID)
		c.Set(ContextKeyScopes, result.Scopes)
		c.Set(ContextKeyAccessToken, token)
		c.Next()
	}
}

// extractToken 从请求中提取 token。
// 优先从 Authorization header 获取，其次从 query 参数获取。
func extractToken(c *gin.Context) string {
	// 1. Authorization: Bearer <token>
	if raw := c.GetHeader("Authorization"); strings.HasPrefix(raw, "Bearer ") {
		token := strings.TrimPrefix(raw, "Bearer ")
		if token != "" {
			return token
		}
	}

	// 2. Query 参数（用于 SSE 等场景）
	if token := c.Query("access_token"); token != "" {
		return token
	}

	return ""
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

// GetAccessToken 从 gin.Context 中取出当前请求的 access_token。
func GetAccessToken(c *gin.Context) string {
	v, _ := c.Get(ContextKeyAccessToken)
	token, _ := v.(string)
	return token
}
