package middleware

import (
	"log"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/response"
)

// Recovery 捕获 panic，返回统一 internal_error 信封。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PANIC] %v\n%s", r, debug.Stack())
				response.FailCode(c, apperr.CodeInternal, "内部错误")
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
