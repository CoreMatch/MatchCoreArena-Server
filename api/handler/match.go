package handler

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/metrics"
	"MatchCoreArena-Server/internal/reportlog"
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
// 对局上报唯一入口（虚拟对局即运维方经此端点提交的假数据，服务端不区分）。
// 仅允许持 match:report scope 的 OPS 级凭据（运维令牌/服务令牌）上报。
// 同一 game_id 重复上报幂等：返回既有记录（200），不重复结算。
func (h *MatchHandler) Report(c *gin.Context) {
	if !middleware.HasScope(c, "match:report") || !middleware.GetOps(c) {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "上报对局结果需要 OPS 级凭据（运维/服务令牌）")
		return
	}

	uid := middleware.GetUID(c)

	var body service.TeamReportInput
	if err := c.ShouldBindJSON(&body); err != nil {
		metrics.Default.Inc(metrics.MatchesRejectedPrefix+string(apperr.CodeMCAInvalidRequest), 1)
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "请求参数格式错误: "+err.Error())
		return
	}

	m, replayed, err := h.svc.Report(c.Request.Context(), uid, body)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			metrics.Default.Inc(metrics.MatchesRejectedPrefix+string(ae.Code), 1)
		} else {
			metrics.Default.Inc(metrics.MatchesRejectedPrefix+string(apperr.CodeInternal), 1)
		}
		response.Fail(c, "上报对战结果失败", err)
		return
	}

	if replayed {
		metrics.Default.Inc(metrics.MatchesReportedReplayTotal, 1)
		response.OK(c, "对局已记录（game_id 幂等重放）", m)
		return
	}
	metrics.Default.Inc(metrics.MatchesReportedTotal, 1)
	response.Created(c, "对战结果已记录", m)
}

// List GET /api/matches
// 全局对局查询（仅 OPS 级凭据，供运维调试台）：分页 + 过滤
//（source、map_name、game_id 精确、uid 参与者、finished_from/finished_to
// 时间区间），按 finished_at 倒序；条目与单局详情同形状。
func (h *MatchHandler) List(c *gin.Context) {
	if !middleware.GetOps(c) {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "全局对局查询仅对 OPS 级凭据开放")
		return
	}

	var q struct {
		service.MatchFilter
		service.Page
	}
	if err := c.ShouldBindQuery(&q); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "查询参数错误: "+err.Error())
		return
	}
	if q.PageNum <= 0 {
		q.PageNum = 1
	}
	if q.PageSize <= 0 || q.PageSize > 100 {
		q.PageSize = 20
	}

	matches, err := h.svc.List(c.Request.Context(), q.MatchFilter, q.Page)
	if err != nil {
		response.Fail(c, "查询对战列表失败", err)
		return
	}
	response.OK(c, "查询对战列表成功", matches)
}

// GetByGameID GET /api/matches/by-game-id/:game_id
// 按 game_id 查询对局（仅 OPS 级凭据），便于排查幂等重放与冲突。
func (h *MatchHandler) GetByGameID(c *gin.Context) {
	if !middleware.GetOps(c) {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "按 game_id 查询仅对 OPS 级凭据开放")
		return
	}

	gameID := c.Param("game_id")
	if gameID == "" {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "game_id 不能为空")
		return
	}

	m, err := h.svc.GetByGameID(c.Request.Context(), gameID)
	if err != nil {
		response.Fail(c, "按 game_id 查询对战失败", err)
		return
	}
	response.OK(c, "按 game_id 查询对战成功", m)
}

// Rejections GET /api/matches/rejections
// 最近的矛盾拒绝与 game_id 冲突记录（仅 OPS 级凭据）：有界内存环，
// 含时间、错误码、game_id 与脱敏原因摘要（UUID 打码），不含玩家载荷字段。
func (h *MatchHandler) Rejections(c *gin.Context) {
	if !middleware.GetOps(c) {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "拒绝记录仅对 OPS 级凭据开放")
		return
	}
	response.OK(c, "查询拒绝记录成功", reportlog.Default.Snapshot())
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
	page := bindPage(c)
	if page.PageNum <= 0 {
		return
	}

	matches, err := h.svc.ListByUser(c.Request.Context(), uid, page)
	if err != nil {
		response.Fail(c, "获取对战历史失败", err)
		return
	}
	response.OK(c, "获取对战历史成功", matches)
}

// ListByUser GET /api/users/:uid/matches
// 按玩家查询历史战绩（分页）。
func (h *MatchHandler) ListByUser(c *gin.Context) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "uid 格式错误")
		return
	}
	page := bindPage(c)
	if page.PageNum <= 0 {
		return
	}

	matches, err := h.svc.ListByUser(c.Request.Context(), uid, page)
	if err != nil {
		response.Fail(c, "获取对战历史失败", err)
		return
	}
	response.OK(c, "获取对战历史成功", matches)
}

// bindPage 解析并归一分页参数；非法时已写响应，返回 PageNum<=0 供调用方短路。
func bindPage(c *gin.Context) service.Page {
	var page service.Page
	if err := c.ShouldBindQuery(&page); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "分页参数错误")
		return service.Page{}
	}
	if page.PageNum <= 0 {
		page.PageNum = 1
	}
	if page.PageSize <= 0 || page.PageSize > 100 {
		page.PageSize = 20
	}
	return page
}
