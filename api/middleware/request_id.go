package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const requestIDKey = "request_id"

// RequestID 为每个请求注入 X-Request-Id。
// 优先读取客户端传入的 X-Request-Id；缺失时自动生成 UUID。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-Id")
		if rid == "" {
			rid = uuid.New().String()
		}
		c.Set(requestIDKey, rid)
		c.Header("X-Request-Id", rid)
		c.Next()
	}
}
