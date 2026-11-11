package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// SeasonHandler 赛季 HTTP handler（契约 1.12.0 手动更季）。
type SeasonHandler struct {
	svc service.SeasonService
}

// NewSeasonHandler 创建 SeasonHandler。
func NewSeasonHandler(svc service.SeasonService) *SeasonHandler {
	return &SeasonHandler{svc: svc}
}

// rollInput POST /api/seasons/roll 请求体。
type rollInput struct {
	TargetSeason int    `json:"target_season" binding:"required"`
	ResetScores  bool   `json:"reset_scores"`
	Note         string `json:"note"`
}

// Roll POST /api/seasons/roll
// 运维手动更季（仅 OPS 级凭据）：目标赛季必须是当前赛季 +1；
// 是否重置评分由调用方显式指定（默认不重置）；重复目标 409。
// 不存在任何按时间的自动更季。
func (h *SeasonHandler) Roll(c *gin.Context) {
	if !middleware.GetOps(c) {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "赛季更迭仅对 OPS 级凭据开放")
		return
	}

	var body rollInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "请求参数格式错误: "+err.Error())
		return
	}
	if len(body.Note) > 200 {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "note 不得超过 200 字符")
	}

	rec, err := h.svc.Roll(c.Request.Context(), body.TargetSeason, body.ResetScores,
		fmt.Sprintf("ops-uid:%d", middleware.GetUID(c)), body.Note)
	if err != nil {
		response.Fail(c, "赛季更迭失败", err)
		return
	}
	response.Created(c, fmt.Sprintf("已进入赛季 %d", rec.Season), rec)
}

// List GET /api/seasons
// 赛季历史（新到旧，首条为当前赛季）。
func (h *SeasonHandler) List(c *gin.Context) {
	seasons, err := h.svc.List(c.Request.Context())
	if err != nil {
		response.Fail(c, "查询赛季记录失败", err)
		return
	}
	response.OK(c, "查询赛季记录成功", seasons)
}
