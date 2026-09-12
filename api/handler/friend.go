package handler

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// FriendHandler 好友 HTTP handler。
type FriendHandler struct {
	svc     service.FriendService
	authCli *auth.Client
}

// NewFriendHandler 创建 FriendHandler。
func NewFriendHandler(svc service.FriendService, authCli *auth.Client) *FriendHandler {
	return &FriendHandler{svc: svc, authCli: authCli}
}

// List GET /api/friends
func (h *FriendHandler) List(c *gin.Context) {
	uid := middleware.GetUID(c)
	friends, err := h.svc.List(c.Request.Context(), uid)
	if err != nil {
		response.Fail(c, "获取好友列表失败", err)
		return
	}
	response.OK(c, "获取好友列表成功", friends)
}

// Request POST /api/friends
func (h *FriendHandler) Request(c *gin.Context) {
	uid := middleware.GetUID(c)

	var body struct {
		Username string `json:"username" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "username 参数必填")
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "username 不能为空")
		return
	}

	// 通过 HRPAuth 查找目标用户
	targetUID, _, err := h.authCli.LookupUserByUsername(username)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			response.FailCode(c, apperr.CodeMCAInvalidRequest, "用户不存在")
			return
		}
		response.Fail(c, "查找用户失败", err)
		return
	}

	if targetUID == uid {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "不能添加自己为好友")
		return
	}

	f, err := h.svc.Request(c.Request.Context(), uid, targetUID)
	if err != nil {
		response.Fail(c, "发送好友请求失败", err)
		return
	}
	response.Created(c, "好友请求已发送", f)
}

// Accept PUT /api/friends/:id/accept
func (h *FriendHandler) Accept(c *gin.Context) {
	uid := middleware.GetUID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}
	if err := h.svc.Accept(c.Request.Context(), uid, id); err != nil {
		response.Fail(c, "接受好友请求失败", err)
		return
	}
	response.OK(c, "已接受好友请求", nil)
}

// Reject PUT /api/friends/:id/reject
func (h *FriendHandler) Reject(c *gin.Context) {
	uid := middleware.GetUID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}
	if err := h.svc.Reject(c.Request.Context(), uid, id); err != nil {
		response.Fail(c, "拒绝好友请求失败", err)
		return
	}
	response.OK(c, "已拒绝好友请求", nil)
}

// Delete DELETE /api/friends/:id
func (h *FriendHandler) Delete(c *gin.Context) {
	uid := middleware.GetUID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), uid, id); err != nil {
		response.Fail(c, "删除好友失败", err)
		return
	}
	response.OK(c, "好友已删除", nil)
}
