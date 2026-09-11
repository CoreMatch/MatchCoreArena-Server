package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// TeamHandler 战队 HTTP handler。
type TeamHandler struct {
	svc service.TeamService
}

// NewTeamHandler 创建 TeamHandler。
func NewTeamHandler(svc service.TeamService) *TeamHandler {
	return &TeamHandler{svc: svc}
}

// Create POST /api/teams
func (h *TeamHandler) Create(c *gin.Context) {
	uid := middleware.GetUID(c)

	var body struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "name 参数必填")
		return
	}

	team, err := h.svc.Create(c.Request.Context(), uid, body.Name, body.Description)
	if err != nil {
		response.Fail(c, "创建战队失败", err)
		return
	}
	response.Created(c, "战队创建成功", team)
}

// GetByID GET /api/teams/:id
func (h *TeamHandler) GetByID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}

	team, err := h.svc.GetByID(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, "获取战队信息失败", err)
		return
	}
	response.OK(c, "获取战队信息成功", team)
}

// Delete DELETE /api/teams/:id
func (h *TeamHandler) Delete(c *gin.Context) {
	uid := middleware.GetUID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), uid, id); err != nil {
		response.Fail(c, "解散战队失败", err)
		return
	}
	response.OK(c, "战队已解散", nil)
}

// ListMembers GET /api/teams/:id/members
func (h *TeamHandler) ListMembers(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}

	members, err := h.svc.ListMembers(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, "获取成员列表失败", err)
		return
	}
	response.OK(c, "获取成员列表成功", members)
}

// AddMember POST /api/teams/:id/members
func (h *TeamHandler) AddMember(c *gin.Context) {
	uid := middleware.GetUID(c)
	teamID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}

	var body struct {
		UserUID int64 `json:"user_uid" binding:"required"`
		Role    int   `json:"role"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "user_uid 参数必填")
		return
	}

	member, err := h.svc.AddMember(c.Request.Context(), uid, teamID, body.UserUID, body.Role)
	if err != nil {
		response.Fail(c, "添加成员失败", err)
		return
	}
	response.OK(c, "成员添加成功", member)
}

// UpdateMemberRole PUT /api/teams/:id/members/:uid/role
func (h *TeamHandler) UpdateMemberRole(c *gin.Context) {
	uid := middleware.GetUID(c)
	teamID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}
	targetUID, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "uid 格式错误")
		return
	}

	var body struct {
		Role int `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "role 参数必填")
		return
	}

	if err := h.svc.UpdateMemberRole(c.Request.Context(), uid, teamID, targetUID, body.Role); err != nil {
		response.Fail(c, "调整角色失败", err)
		return
	}
	response.OK(c, "角色调整成功", nil)
}

// RemoveMember DELETE /api/teams/:id/members/:uid
func (h *TeamHandler) RemoveMember(c *gin.Context) {
	uid := middleware.GetUID(c)
	teamID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}
	targetUID, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "uid 格式错误")
		return
	}

	if err := h.svc.RemoveMember(c.Request.Context(), uid, teamID, targetUID); err != nil {
		response.Fail(c, "移除成员失败", err)
		return
	}
	response.OK(c, "成员已移除", nil)
}
