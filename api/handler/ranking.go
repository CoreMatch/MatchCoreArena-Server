package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/config"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// RankingHandler 排行榜 HTTP handler。
type RankingHandler struct {
	svc   service.RankingService
	tiers []config.RankTier
}

// NewRankingHandler 创建 RankingHandler。tiers 为段位阶梯展示配置
//（可为空：端点返回空数组，前端不显示进度条）。
func NewRankingHandler(svc service.RankingService, tiers []config.RankTier) *RankingHandler {
	return &RankingHandler{svc: svc, tiers: tiers}
}

// GetTop GET /api/rankings/:type
func (h *RankingHandler) GetTop(c *gin.Context) {
	rankType := c.Param("type")
	if rankType == "" {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "type 参数必填")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	season, _ := strconv.Atoi(c.DefaultQuery("season", "1"))
	if season <= 0 {
		season = 1
	}

	result, err := h.svc.GetTop(c.Request.Context(), rankType, limit, season)
	if err != nil {
		response.Fail(c, "获取排行榜失败", err)
		return
	}
	response.OK(c, "获取排行榜成功", result)
}

// GetMyRank GET /api/rankings/me?type=xxx
func (h *RankingHandler) GetMyRank(c *gin.Context) {
	uid := middleware.GetUID(c)

	rankType := c.Query("type")
	if rankType == "" {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "type 参数必填")
		return
	}

	season, _ := strconv.Atoi(c.DefaultQuery("season", "1"))
	if season <= 0 {
		season = 1
	}

	ranking, err := h.svc.GetMyRank(c.Request.Context(), uid, rankType, season)
	if err != nil {
		response.Fail(c, "获取我的排名失败", err)
		return
	}
	response.OK(c, "获取我的排名成功", ranking)
}

// Tiers GET /api/rankings/tiers
// 段位阶梯展示配置（纯展示：不参与结算、不引入赛季）。未配置时返回
// 空数组，前端据此不显示进度条。
func (h *RankingHandler) Tiers(c *gin.Context) {
	tiers := h.tiers
	if tiers == nil {
		tiers = []config.RankTier{}
	}
	response.OK(c, "获取段位阶梯成功", tiers)
}
