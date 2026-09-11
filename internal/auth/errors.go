// Package auth 命名错误：HRPAuth 调用过程中可能出现的各类错误。
//
// 使用 errors.Is 判断具体错误类型。
package auth

import "errors"

var (
	// ErrTokenExpired access_token 已过期，客户端应调 /api/auth/refresh。
	ErrTokenExpired = errors.New("hrpauth: token expired")

	// ErrTokenInvalid access_token 无效（不是合法格式或在 HRPAuth 中不存在）。
	ErrTokenInvalid = errors.New("hrpauth: token invalid")

	// ErrCredentialsInvalid 邮箱或密码错误。
	ErrCredentialsInvalid = errors.New("hrpauth: credentials invalid")

	// ErrTotpInvalid TOTP passcode 错误或 login_ticket 失效。
	ErrTotpInvalid = errors.New("hrpauth: totp invalid")

	// ErrTokenRefreshFailed refresh_token 失效或过期。
	ErrTokenRefreshFailed = errors.New("hrpauth: refresh failed")

	// ErrRateLimited 触发 HRPAuth 限流。
	ErrRateLimited = errors.New("hrpauth: rate limited")

	// ErrUpstream HRPAuth 上游不可用（5xx、网络错误、超时）。
	ErrUpstream = errors.New("hrpauth: upstream unavailable")
)
