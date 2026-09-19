package handler

import (
	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// MaintenanceHandler 处理维护相关的 API 请求。
type MaintenanceHandler struct {
	maintenanceSvc service.MaintenanceService
}

// NewMaintenanceHandler 创建 MaintenanceHandler 实例。
func NewMaintenanceHandler(maintenanceSvc service.MaintenanceService) *MaintenanceHandler {
	return &MaintenanceHandler{
		maintenanceSvc: maintenanceSvc,
	}
}

// ExecuteSQL 直接执行 SQL 语句。
// 需要 maintenance 权限。
func (h *MaintenanceHandler) ExecuteSQL(c *gin.Context) {
	// 1. 权限校验
	if !middleware.HasScope(c, "maintenance") {
		response.FailCode(c, apperr.CodeOAuthInsufficientScope, "无权执行维护操作")
		return
	}

	// 2. 参数绑定
	var req struct {
		SQL string `json:"sql" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailCode(c, apperr.CodeInvalidRequest, "无效的请求参数")
		return
	}

	// 3. 执行 SQL
	result, err := h.maintenanceSvc.ExecuteSQL(c.Request.Context(), req.SQL)
	if err != nil {
		response.Fail(c, "SQL 执行失败", err)
		return
	}

	// 4. 返回结果
	response.OK(c, "SQL 执行成功", result)
}
