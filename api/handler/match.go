package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// MatchHandler 对战 HTTP handler。
type MatchHandler struct {
	svc service.MatchService
}

// NewMatchHandler 创建 MatchHandler。
func NewMatchHandler(svc service.MatchService) *MatchHandler {
	return &MatchHandler{svc: svc}
}

// Report POST /api/matches
func (h *MatchHandler) Report(c *gin.Context) {
	uid := middleware.GetUID(c)

	var body service.ReportInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "请求参数不完整: "+err.Error())
		return
	}

	m, err := h.svc.Report(c.Request.Context(), uid, body)
	if err != nil {
		response.Fail(c, "上报对战结果失败", err)
		return
	}
	response.Created(c, "对战结果已记录", m)
}

// GetByID GET /api/matches/:id
func (h *MatchHandler) GetByID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "id 格式错误")
		return
	}

	m, err := h.svc.GetByID(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, "获取对战详情失败", err)
		return
	}
	response.OK(c, "获取对战详情成功", m)
}

// ListByMe GET /api/matches/me
func (h *MatchHandler) ListByMe(c *gin.Context) {
	uid := middleware.GetUID(c)

	var page service.Page
	if err := c.ShouldBindQuery(&page); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "分页参数错误")
		return
	}
	if page.PageNum <= 0 {
		page.PageNum = 1
	}
	if page.PageSize <= 0 || page.PageSize > 100 {
		page.PageSize = 20
	}

	matches, err := h.svc.ListByUser(c.Request.Context(), uid, page)
	if err != nil {
		response.Fail(c, "获取对战历史失败", err)
		return
	}
	response.OK(c, "获取对战历史成功", matches)
}
