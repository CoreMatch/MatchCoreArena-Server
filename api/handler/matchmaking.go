package handler

import (
	"strconv"

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
	// PartyID 可选；>0 时以队伍为单位整体入队（仅队长可发起）。
	PartyID int64 `json:"party_id"`
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

	entries, err := h.svc.Join(c.Request.Context(), uid, body.MatchType, body.PartyID)
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

// createPartyInput POST /api/matchmaking/party 请求体。
type createPartyInput struct {
	MatchType string `json:"match_type" binding:"required"`
}

// CreateParty POST /api/matchmaking/party
func (h *MatchmakingHandler) CreateParty(c *gin.Context) {
	uid := middleware.GetUID(c)

	var body createPartyInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "请求参数格式错误: "+err.Error())
		return
	}

	party, err := h.svc.CreateParty(c.Request.Context(), uid, body.MatchType)
	if err != nil {
		response.Fail(c, "创建队伍失败", err)
		return
	}
	response.Created(c, "队伍已创建", party)
}

// GetParty GET /api/matchmaking/party/:id
func (h *MatchmakingHandler) GetParty(c *gin.Context) {
	partyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "队伍 id 格式错误")
		return
	}
	party, err := h.svc.GetParty(c.Request.Context(), partyID)
	if err != nil {
		response.Fail(c, "查询队伍失败", err)
		return
	}
	response.OK(c, "查询队伍成功", party)
}

// JoinParty POST /api/matchmaking/party/:id/join
func (h *MatchmakingHandler) JoinParty(c *gin.Context) {
	uid := middleware.GetUID(c)
	partyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "队伍 id 格式错误")
		return
	}
	party, err := h.svc.JoinParty(c.Request.Context(), uid, partyID)
	if err != nil {
		response.Fail(c, "加入队伍失败", err)
		return
	}
	response.OK(c, "已加入队伍", party)
}

// LeaveParty DELETE /api/matchmaking/party/:id
// 成员离开队伍；队长调用即解散队伍。
func (h *MatchmakingHandler) LeaveParty(c *gin.Context) {
	uid := middleware.GetUID(c)
	partyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "队伍 id 格式错误")
		return
	}
	if err := h.svc.LeaveParty(c.Request.Context(), uid, partyID); err != nil {
		response.Fail(c, "离开队伍失败", err)
		return
	}
	response.OK(c, "已离开队伍", nil)
}
