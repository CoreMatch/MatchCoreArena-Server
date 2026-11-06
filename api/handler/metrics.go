package handler

import (
	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/metrics"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// MetricsHandler 可观测性 HTTP handler。
type MetricsHandler struct {
	matchmakingSvc service.MatchmakingService
}

// NewMetricsHandler 创建 MetricsHandler。
func NewMetricsHandler(matchmakingSvc service.MatchmakingService) *MetricsHandler {
	return &MetricsHandler{matchmakingSvc: matchmakingSvc}
}

// Metrics GET /api/metrics
// 仅 OPS 级凭据可见：输出计数器快照与按模式的队列深度。
func (h *MetricsHandler) Metrics(c *gin.Context) {
	if !middleware.GetOps(c) {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "指标仅对 OPS 级凭据可见")
		return
	}

	depth, err := h.matchmakingSvc.QueueDepth(c.Request.Context())
	if err != nil {
		response.Fail(c, "查询队列深度失败", err)
		return
	}

	response.OK(c, "指标快照", gin.H{
		"counters":    metrics.Default.Snapshot(),
		"queue_depth": depth,
	})
}
