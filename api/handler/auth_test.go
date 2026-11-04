package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/handler"
	"MatchCoreArena-Server/internal/auth"
)

// TestHRPAuthDownCode 断言 HRPAuth 不可用时，三个认证透传端点都返回
// 统一信封且 code 为正确拼写 "oauth_hrpauth_down"（历史笔误
// "oauth_hrpaauth_down" 已修复，此测试防止拼写回退）。
func TestHRPAuthDownCode(t *testing.T) {
	// auth.NewClient 启动时校验 /status 可达：先用假服务器通过校验，
	// 随即关闭，使后续请求全部网络失败 → ErrUpstream → HRPAuth-down 分支。
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	cli, err := auth.NewClient(up.URL, "test-client")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	up.Close()

	gin.SetMode(gin.TestMode)
	h := handler.NewAuthHandler(cli, nil)
	r := gin.New()
	r.POST("/api/auth/login-ticket", h.LoginTicket)
	r.POST("/api/auth/totp-verify", h.TotpVerify)
	r.POST("/api/auth/refresh", h.Refresh)

	cases := []struct {
		path string
		body string
	}{
		{"/api/auth/login-ticket", `{"email":"a@b.c","password":"x"}`},
		{"/api/auth/totp-verify", `{"login_ticket":"lt","passcode":"000000"}`},
		{"/api/auth/refresh", `{"refresh_token":"rt"}`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502 (body=%s)", w.Code, w.Body.String())
			}
			var env struct {
				Success bool   `json:"success"`
				Code    string `json:"code"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("unmarshal: %v (body=%s)", err, w.Body.String())
			}
			if env.Success {
				t.Fatal("success must be false on the error path")
			}
			if env.Code != "oauth_hrpauth_down" {
				t.Fatalf("code = %q, want oauth_hrpauth_down", env.Code)
			}
		})
	}
}
