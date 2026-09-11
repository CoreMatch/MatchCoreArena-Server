package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// RankingHandler 排行榜 HTTP handler。
type RankingHandler struct {
	svc service.RankingService
}

// NewRankingHandler 创建 RankingHandler。
func NewRankingHandler(svc service.RankingService) *RankingHandler {
	return &RankingHandler{svc: svc}
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

	rankings, err := h.svc.GetTop(c.Request.Context(), rankType, limit, season)
	if err != nil {
		response.Fail(c, "获取排行榜失败", err)
		return
	}
	response.OK(c, "获取排行榜成功", rankings)
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
