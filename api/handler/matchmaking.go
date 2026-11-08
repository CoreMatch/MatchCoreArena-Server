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
	// PartyMembers 可选成员 UUID 列表（与运维台字段同名同义）；
	// 非空时以队为单位整体入队。
	PartyMembers []string `json:"party_members"`
}

// Join POST /api/matchmaking/join
// 返回本次入队的全部队列条目（单人 1 条，队伍整队多条）。
func (h *MatchmakingHandler) Join(c *gin.Context) {
	uid := middleware.GetUID(c)

	var body joinInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "请求参数格式错误: "+err.Error())
		return
	}

	entries, err := h.svc.Join(c.Request.Context(), uid, body.MatchType, body.PartyMembers)
	if err != nil {
		response.Fail(c, "加入匹配队列失败", err)
		return
	}
	response.Created(c, "已加入匹配队列", entries)
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

// Assignment GET /api/matchmaking/assignment/:uuid
// 按玩家 UUID 查询当前有效成局分配（契约 1.9.0，代理路由用）。
// 仅 OPS 级凭据（运维令牌/服务令牌）：调用方是可信基础设施（如
// Velocity 代理插件），玩家令牌一律 403。响应只含路由所需的最小
// 信息，不回显成员名单与评分。
func (h *MatchmakingHandler) Assignment(c *gin.Context) {
	if !middleware.GetOps(c) {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "成局分配查询仅对 OPS 级凭据开放")
		return
	}

	uuid := c.Param("uuid")
	if uuid == "" {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "uuid 不能为空")
		return
	}

	asn, err := h.svc.Assignment(c.Request.Context(), uuid)
	if err != nil {
		response.Fail(c, "查询成局分配失败", err)
		return
	}
	response.OK(c, "查询成局分配成功", asn)
}
