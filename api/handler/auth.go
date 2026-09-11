package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// AuthHandler 认证 HTTP handler。
type AuthHandler struct {
	oauthClient *auth.OAuthClient
	userSvc     service.UserService
}

// NewAuthHandler 创建 AuthHandler。
func NewAuthHandler(oauthClient *auth.OAuthClient, userSvc service.UserService) *AuthHandler {
	return &AuthHandler{
		oauthClient: oauthClient,
		userSvc:     userSvc,
	}
}

// LoginResponse 登录参数响应。
type LoginResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
	CodeVerifier     string `json:"code_verifier"`
}

// Login GET /api/auth/login
// 返回授权 URL、state、code_verifier，由前端自行跳转。
func (h *AuthHandler) Login(c *gin.Context) {
	authURL, state, codeVerifier, err := h.oauthClient.BuildLoginParams()
	if err != nil {
		response.Fail(c, "构建授权 URL 失败", err)
		return
	}

	response.OK(c, "获取登录参数成功", LoginResponse{
		AuthorizationURL: authURL,
		State:            state,
		CodeVerifier:     codeVerifier,
	})
}

// CallbackRequest 回调请求。
type CallbackRequest struct {
	Code         string `json:"code" binding:"required"`
	State        string `json:"state" binding:"required"`
	CodeVerifier string `json:"code_verifier" binding:"required"`
}

// CallbackResponse 回调响应。
type CallbackResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// Callback POST /api/auth/callback
// 前端拿到 code 后 POST 到此端点，后端用 code 换取 token。
func (h *AuthHandler) Callback(c *gin.Context) {
	var req CallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "缺少必要参数: code, state, code_verifier")
		return
	}

	ctx := context.Background()

	// 用 code + code_verifier 换取 token
	tokenSet, err := h.oauthClient.ExchangeCode(ctx, req.Code, req.CodeVerifier)
	if err != nil {
		response.FailCode(c, apperr.CodeOAuthInvalidGrant, "授权码换 token 失败")
		return
	}

	// 用 access_token 获取用户信息，确保用户在本地落库
	if tokenSet.AccessToken != "" {
		userInfo, err := h.oauthClient.FetchUserInfo(ctx, tokenSet.AccessToken)
		if err == nil {
			if sub, ok := userInfo["sub"].(string); ok {
				h.userSvc.EnsureUser(c.Request.Context(), sub)
			}
		}
		// userinfo 失败不阻塞登录流程
	}

	response.OK(c, "登录成功", CallbackResponse{
		AccessToken:  tokenSet.AccessToken,
		RefreshToken: tokenSet.RefreshToken,
		ExpiresIn:    tokenSet.ExpiresIn,
		TokenType:    tokenSet.TokenType,
	})
}

// RefreshRequest 刷新请求。
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh POST /api/auth/refresh
// 用 refresh_token 换取新的 access_token。
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailCode(c, apperr.CodeMCAInvalidRequest, "缺少 refresh_token")
		return
	}

	tokenSet, err := h.oauthClient.RefreshToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		response.FailCode(c, apperr.CodeOAuthRefreshFailed, "刷新 token 失败")
		return
	}

	response.OK(c, "刷新成功", CallbackResponse{
		AccessToken:  tokenSet.AccessToken,
		RefreshToken: tokenSet.RefreshToken,
		ExpiresIn:    tokenSet.ExpiresIn,
		TokenType:    tokenSet.TokenType,
	})
}

// LogoutRequest 注销请求。
type LogoutRequest struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// Logout POST /api/auth/logout
// 吊销 token。支持通过 Bearer header 或 body 传入 token。
func (h *AuthHandler) Logout(c *gin.Context) {
	var req LogoutRequest
	c.ShouldBindJSON(&req) // body 可选

	token := req.AccessToken
	if token == "" {
		// 从 header 取
		token = c.GetString("_access_token") // 由中间件注入
	}

	ctx := context.Background()
	if token != "" {
		h.oauthClient.RevokeToken(ctx, token)
	}
	if req.RefreshToken != "" {
		h.oauthClient.RevokeToken(ctx, req.RefreshToken)
	}

	response.OK(c, "注销成功", nil)
}
