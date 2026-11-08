package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// statusCORSAllowedOrigins 是 GET /api/status 的跨域白名单（契约
// matchcorearena-business.yaml 1.8.0）：仅 mcnb.dev 门户可在浏览器读取
// 服务在线状态。精确匹配、无通配、绝不回显白名单之外的 Origin，也绝不
// 下发 Access-Control-Allow-Credentials。白名单之外的 Origin（含无
// Origin）照常响应，只是不带任何 CORS 头，由浏览器侧拦截。
//
// 该中间件只挂在 /api/status 上，不是全局 CORS；其余端点（尤其全部
// 鉴权端点）不受影响。
var statusCORSAllowedOrigins = map[string]bool{
	"https://mcnb.dev":     true,
	"https://www.mcnb.dev": true,
}

// StatusCORS 仅对 /api/status 开放的收紧 CORS：
//   - 白名单 Origin 的实际请求：回显 Access-Control-Allow-Origin；
//   - 白名单 Origin 的预检（OPTIONS）：204 + Allow-Methods: GET，
//     仅当预检携带 Access-Control-Request-Headers 时追加
//     Allow-Headers: Content-Type；
//   - 其余 Origin / 无 Origin：不带任何 CORS 头（不回显 Origin）；
//   - 响应恒带 Vary: Origin，绝不下发 Access-Control-Allow-Credentials。
func StatusCORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Vary", "Origin")

		origin := c.GetHeader("Origin")
		if !statusCORSAllowedOrigins[origin] {
			// 未命中白名单：不回显 Origin、不下发任何 CORS 头。
			// OPTIONS 也照常应答 204，让浏览器侧自行拦截。
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
			c.Next()
			return
		}

		c.Header("Access-Control-Allow-Origin", origin)
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", http.MethodGet)
			if c.GetHeader("Access-Control-Request-Headers") != "" {
				c.Header("Access-Control-Allow-Headers", "Content-Type")
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
