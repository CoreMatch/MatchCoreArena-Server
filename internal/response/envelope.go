// Package response 提供符合 HA-Contract 规范的统一响应信封。
//
// 成功响应：{ "success": true, "message": "xxx", "data": {...}, "meta": { "request_id": "xxx" } }
// 失败响应：{ "success": false, "message": "xxx", "code": "xxx", "error": "xxx", "meta": { "request_id": "xxx" } }
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/internal/apperr"
)

const requestIDKey = "request_id"

// Meta 响应元信息。
type Meta struct {
	RequestID string `json:"request_id"`
}

// SuccessEnvelope 成功响应信封。
type SuccessEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
	Meta    Meta   `json:"meta"`
}

// ErrorEnvelope 失败响应信封。
type ErrorEnvelope struct {
	Success bool   `json:"success"` // 始终 false
	Message string `json:"message"`
	Code    string `json:"code"`
	Error   string `json:"error"` // 与 code 同值，兼容别名
	Meta    Meta   `json:"meta"`
}

// requestIDFromCtx 从 gin.Context 中读取 request_id。
func requestIDFromCtx(c *gin.Context) string {
	if v, ok := c.Get(requestIDKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// OK 返回 200 成功响应。
func OK(c *gin.Context, message string, data any) {
	c.JSON(http.StatusOK, SuccessEnvelope{
		Success: true,
		Message: message,
		Data:    data,
		Meta:    Meta{RequestID: requestIDFromCtx(c)},
	})
}

// Created 返回 201 成功响应。
func Created(c *gin.Context, message string, data any) {
	c.JSON(http.StatusCreated, SuccessEnvelope{
		Success: true,
		Message: message,
		Data:    data,
		Meta:    Meta{RequestID: requestIDFromCtx(c)},
	})
}

// Fail 返回业务错误响应。
// 优先使用 *apperr.Error 中的 Code 和 HTTPStatus；否则使用 CodeInternal + 500。
func Fail(c *gin.Context, message string, err error) {
	code := apperr.CodeInternal
	status := http.StatusInternalServerError

	if ae, ok := err.(*apperr.Error); ok {
		code = ae.Code
		status = ae.HTTPStatus()
	}

	c.JSON(status, ErrorEnvelope{
		Success: false,
		Message: message,
		Code:    string(code),
		Error:   string(code),
		Meta:    Meta{RequestID: requestIDFromCtx(c)},
	})
}

// FailCode 返回指定错误码的响应。
func FailCode(c *gin.Context, code apperr.Code, message string) {
	c.JSON(apperr.StatusFromCode(code), ErrorEnvelope{
		Success: false,
		Message: message,
		Code:    string(code),
		Error:   string(code),
		Meta:    Meta{RequestID: requestIDFromCtx(c)},
	})
}
