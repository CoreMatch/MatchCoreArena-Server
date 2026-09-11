// Package apperr 定义 MatchCoreArena 服务的业务错误码常量。
//
// 命名规则：
//   - 业务错误码使用 lower_snake_case，前缀 mca_ 表示本服务命名空间。
//   - 复用 HA-Contract 中已有的 OAuth/通用错误码（oauth_* / internal_error）。
package apperr

import (
	"fmt"
	"net/http"
)

// Code 是业务错误码类型。
type Code string

const (
	// 复用 HA-Contract 已有的错误码
	CodeInvalidRequest         Code = "invalid_request"
	CodeOAuthLoginRequired     Code = "oauth_login_required"
	CodeOAuthInsufficientScope Code = "oauth_insufficient_scope"
	CodeOAuthInvalidGrant      Code = "oauth_invalid_grant"
	CodeOAuthStateMismatch     Code = "oauth_state_mismatch"
	CodeOAuthTokenExpired      Code = "oauth_token_expired"
	CodeOAuthRefreshFailed     Code = "oauth_refresh_failed"
	CodeOAuthRevokeFailed      Code = "oauth_revoke_failed"
	CodeOAuthRateLimited       Code = "oauth_rate_limited"
	CodeOAuthHRPAuthDown       Code = "oauth_hrpaauth_down"
	CodeInternal               Code = "internal_error"

	// 本服务业务错误码
	CodeMCAInvalidRequest      Code = "mca_invalid_request"
	CodeMCAUserNotFound        Code = "mca_user_not_found"
	CodeMCAFriendNotFound      Code = "mca_friend_not_found"
	CodeMCAFriendRequestExists Code = "mca_friend_request_exists"
	CodeMCATeamNotFound        Code = "mca_team_not_found"
	CodeMCATeamNameTaken       Code = "mca_team_name_taken"
	CodeMCANotTeamLeader       Code = "mca_not_team_leader"
	CodeMCAMatchNotFound       Code = "mca_match_not_found"
)

// codeHTTPMap 将错误码映射到建议 HTTP 状态码。
var codeHTTPMap = map[Code]int{
	CodeInvalidRequest:         http.StatusBadRequest,
	CodeOAuthLoginRequired:     http.StatusUnauthorized,
	CodeOAuthInsufficientScope: http.StatusForbidden,
	CodeOAuthInvalidGrant:      http.StatusUnauthorized,
	CodeOAuthStateMismatch:     http.StatusBadRequest,
	CodeOAuthTokenExpired:      http.StatusUnauthorized,
	CodeOAuthRefreshFailed:     http.StatusUnauthorized,
	CodeOAuthRevokeFailed:      http.StatusBadRequest,
	CodeOAuthRateLimited:       http.StatusTooManyRequests,
	CodeOAuthHRPAuthDown:       http.StatusBadGateway,
	CodeInternal:               http.StatusInternalServerError,
	CodeMCAInvalidRequest:      http.StatusBadRequest,
	CodeMCAUserNotFound:        http.StatusNotFound,
	CodeMCAFriendNotFound:      http.StatusNotFound,
	CodeMCAFriendRequestExists: http.StatusConflict,
	CodeMCATeamNotFound:        http.StatusNotFound,
	CodeMCATeamNameTaken:       http.StatusConflict,
	CodeMCANotTeamLeader:       http.StatusForbidden,
	CodeMCAMatchNotFound:       http.StatusNotFound,
}

// StatusFromCode 根据错误码返回建议 HTTP 状态码，未注册的码返回 500。
func StatusFromCode(c Code) int {
	if s, ok := codeHTTPMap[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Error 业务错误，携带 Code 和可选消息。
type Error struct {
	Code    Code
	Message string
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// New 创建业务错误，message 可为空（此时使用 code 的默认描述）。
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// HTTPStatus 返回建议 HTTP 状态码。
func (e *Error) HTTPStatus() int {
	return StatusFromCode(e.Code)
}
