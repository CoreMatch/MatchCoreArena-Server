package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// UserHandler 用户 HTTP handler。
type UserHandler struct {
	svc service.UserService
}

// NewUserHandler 创建 UserHandler。
func NewUserHandler(svc service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// GetMe GET /api/users/me
func (h *UserHandler) GetMe(c *gin.Context) {
	uid := middleware.GetUID(c)
	user, err := h.svc.GetMe(c.Request.Context(), uid)
	if err != nil {
		response.Fail(c, "获取用户信息失败", err)
		return
	}
	response.OK(c, "获取用户信息成功", user)
}

// GetByUID GET /api/users/:uid
func (h *UserHandler) GetByUID(c *gin.Context) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "uid 格式错误")
		return
	}
	user, err := h.svc.GetByUID(c.Request.Context(), uid)
	if err != nil {
		response.Fail(c, "获取用户信息失败", err)
		return
	}
	response.OK(c, "获取用户信息成功", user)
}

// AddExperience POST /api/users/me/experience
func (h *UserHandler) AddExperience(c *gin.Context) {
	uid := middleware.GetUID(c)

	var body struct {
		Amount int64 `json:"amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "amount 参数必填")
		return
	}

	user, err := h.svc.AddExperience(c.Request.Context(), uid, body.Amount)
	if err != nil {
		response.Fail(c, "增加经验失败", err)
		return
	}
	response.OK(c, "增加经验成功", user)
}
