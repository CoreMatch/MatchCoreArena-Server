package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/response"
)

// AuthHandler 认证 HTTP handler。
// 作为 HRPAuth 的透明代理：前端调 MCA，MCA 透传到 HRPAuth。
type AuthHandler struct {
	client *auth.Client
}

// NewAuthHandler 创建 AuthHandler。
func NewAuthHandler(client *auth.Client) *AuthHandler {
	return &AuthHandler{client: client}
}

// LoginTicketRequest 前端发给 MCA 的登录请求。
type LoginTicketRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginTicket POST /api/auth/login-ticket
// 透传邮箱密码到 HRPAuth /oauth/login-ticket。
// 响应可能直接含 access_token，或含 totp_required + login_ticket。
func (h *AuthHandler) LoginTicket(c *gin.Context) {
	var req LoginTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "缺少必要参数: email, password")
		return
	}

	result, err := h.client.GetLoginTicket(req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrCredentialsInvalid) {
			response.FailCode(c, apperr.CodeOAuthInvalidGrant, "邮箱或密码错误")
			return
		}
		if errors.Is(err, auth.ErrRateLimited) {
			response.FailCode(c, apperr.CodeOAuthRateLimited, "请求过于频繁，请稍后重试")
			return
		}
		response.FailCode(c, apperr.CodeOAuthHRPAuthDown, "HRPAuth 不可用")
		return
	}

	// 直接返回 HRPAuth 原始响应（前端自行处理 totp_required 或 token）
	response.OK(c, "认证请求已处理", result)
}

// TotpVerifyRequest 前端发给 MCA 的 TOTP 验证请求。
type TotpVerifyRequest struct {
	LoginTicket string `json:"login_ticket" binding:"required"`
	Passcode    string `json:"passcode" binding:"required"`
}

// TotpVerify POST /api/auth/totp-verify
// 透传 login_ticket + passcode 到 HRPAuth /totp/verify。
func (h *AuthHandler) TotpVerify(c *gin.Context) {
	var req TotpVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "缺少必要参数: login_ticket, passcode")
		return
	}

	tokenPair, err := h.client.VerifyTotp(req.LoginTicket, req.Passcode)
	if err != nil {
		if errors.Is(err, auth.ErrTotpInvalid) {
			response.FailCode(c, apperr.CodeOAuthInvalidGrant, "TOTP 验证失败")
			return
		}
		response.FailCode(c, apperr.CodeOAuthHRPAuthDown, "HRPAuth 不可用")
		return
	}

	response.OK(c, "TOTP 验证成功", tokenPair)
}

// RefreshRequest 前端发给 MCA 的 token 刷新请求。
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh POST /api/auth/refresh
// 透传 refresh_token 到 HRPAuth /oauth/token。
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "缺少 refresh_token")
		return
	}

	tokenPair, err := h.client.RefreshToken(req.RefreshToken)
	if err != nil {
		if errors.Is(err, auth.ErrTokenRefreshFailed) {
			response.FailCode(c, apperr.CodeOAuthRefreshFailed, "refresh_token 失效或过期")
			return
		}
		response.FailCode(c, apperr.CodeOAuthHRPAuthDown, "HRPAuth 不可用")
		return
	}

	response.OK(c, "刷新成功", tokenPair)
}

// Logout POST /api/auth/logout
// 用当前 Bearer token 调用 HRPAuth /oauth/revoke 吊销。
func (h *AuthHandler) Logout(c *gin.Context) {
	token := c.GetString("_access_token")
	if token == "" {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "缺少 access_token")
		return
	}

	if err := h.client.RevokeToken(token); err != nil {
		response.FailCode(c, apperr.CodeOAuthRevokeFailed, "注销失败")
		return
	}

	response.OK(c, "注销成功", nil)
}
