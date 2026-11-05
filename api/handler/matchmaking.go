package handler

import (
	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// MatchmakingHandler 匹配 HTTP handler。
type MatchmakingHandler struct {
	svc service.MatchmakingService
}

// NewMatchmakingHandler 创建 MatchmakingHandler。
func NewMatchmakingHandler(svc service.MatchmakingService) *MatchmakingHandler {
	return &MatchmakingHandler{svc: svc}
}

// joinInput POST /api/matchmaking/join 请求体。
type joinInput struct {
	MatchType string `json:"match_type" binding:"required"`
}

// Join POST /api/matchmaking/join
func (h *MatchmakingHandler) Join(c *gin.Context) {
	uid := middleware.GetUID(c)

	var body joinInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "请求参数格式错误: "+err.Error())
		return
	}

	entry, err := h.svc.Join(c.Request.Context(), uid, body.MatchType)
	if err != nil {
		response.Fail(c, "加入匹配队列失败", err)
		return
	}
	response.Created(c, "已加入匹配队列", entry)
}

// Leave DELETE /api/matchmaking/leave
func (h *MatchmakingHandler) Leave(c *gin.Context) {
	uid := middleware.GetUID(c)
	if err := h.svc.Leave(c.Request.Context(), uid); err != nil {
		response.Fail(c, "离开匹配队列失败", err)
		return
	}
	response.OK(c, "已离开匹配队列", nil)
}

// Status GET /api/matchmaking/status
func (h *MatchmakingHandler) Status(c *gin.Context) {
	uid := middleware.GetUID(c)
	st, err := h.svc.Status(c.Request.Context(), uid)
	if err != nil {
		response.Fail(c, "查询匹配状态失败", err)
		return
	}
	response.OK(c, "查询匹配状态成功", st)
}
