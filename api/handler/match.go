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
// virtualEnabled 来自配置 virtual_matches.enabled，关闭时虚拟对局入口返回 400。
type MatchHandler struct {
	svc            service.MatchService
	virtualEnabled bool
}

// NewMatchHandler 创建 MatchHandler。
func NewMatchHandler(svc service.MatchService, virtualEnabled bool) *MatchHandler {
	return &MatchHandler{svc: svc, virtualEnabled: virtualEnabled}
}

// Report POST /api/matches
func (h *MatchHandler) Report(c *gin.Context) {
	// 仅允许拥有 match:report scope 的客户端上报（系统/管理员）
	if !middleware.HasScope(c, "match:report") {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "无权上报对战结果")
		return
	}

	uid := middleware.GetUID(c)

	var body service.TeamReportInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "请求参数格式错误: "+err.Error())
		return
	}

	m, err := h.svc.Report(c.Request.Context(), uid, body)
	if err != nil {
		response.Fail(c, "上报对战结果失败", err)
		return
	}
	response.Created(c, "团队对战结果已记录", m)
}

// Virtual POST /api/matches/virtual
// 虚拟对局入口：不依赖游戏侧连接，直接灌入对局数据完成定级结算。
// 与游戏上报同一鉴权姿态（match:report scope），不改动 POST /api/matches 语义。
func (h *MatchHandler) Virtual(c *gin.Context) {
	if !h.virtualEnabled {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "虚拟对局未启用")
		return
	}
	if !middleware.HasScope(c, "match:report") {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "无权提交虚拟对局")
		return
	}

	uid := middleware.GetUID(c)

	var body service.VirtualReportInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "请求参数格式错误: "+err.Error())
		return
	}

	result, err := h.svc.ReportVirtual(c.Request.Context(), uid, body)
	if err != nil {
		response.Fail(c, "提交虚拟对局失败", err)
		return
	}
	response.Created(c, "虚拟对局已记录", result)
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
