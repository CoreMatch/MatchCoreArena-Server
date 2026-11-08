package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newCORSTestRouter 构造仅含 /api/status 的最小路由（与 api.Register
// 同形：GET 挂 StatusCORS，OPTIONS 单独注册）。
func newCORSTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cors := StatusCORS()
	r.GET("/api/status", cors, func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	r.OPTIONS("/api/status", cors)
	return r
}

// do 发起带可选 Origin 的请求并返回记录器。
func do(t *testing.T, r *gin.Engine, method, origin string, extraHeaders map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/status", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// assertNoCORS 断言响应不含任何 CORS 授权头（尤其不得回显 Origin）。
func assertNoCORS(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want 空（未命中白名单不得回显）", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want 空（绝不下发）", got)
	}
}

// TestStatusCORSHit GET 命中白名单：回显 Origin、带 Vary、无 credentials。
func TestStatusCORSHit(t *testing.T) {
	r := newCORSTestRouter()
	for _, origin := range []string{"https://mcnb.dev", "https://www.mcnb.dev"} {
		w := do(t, r, http.MethodGet, origin, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", origin, w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("%s: Access-Control-Allow-Origin = %q, want %q", origin, got, origin)
		}
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("%s: Vary = %q, want Origin", origin, got)
		}
		if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("%s: Access-Control-Allow-Credentials = %q, want 空", origin, got)
		}
	}
}

// TestStatusCORSMiss GET 未命中：正常 200 但不回显 Origin、无 CORS 头。
func TestStatusCORSMiss(t *testing.T) {
	r := newCORSTestRouter()
	for _, origin := range []string{
		"https://evil.example",
		"http://mcnb.dev",          // 协议不同
		"https://mcnb.dev.evil.example", // 后缀伪装
		"null",
	} {
		w := do(t, r, http.MethodGet, origin, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200（未命中也照常响应）", origin, w.Code)
		}
		assertNoCORS(t, w)
	}
}

// TestStatusCORSNoOrigin 无 Origin 的普通请求：200、无 CORS 授权头。
func TestStatusCORSNoOrigin(t *testing.T) {
	r := newCORSTestRouter()
	w := do(t, r, http.MethodGet, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	assertNoCORS(t, w)
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin（缓存需按 Origin 区分）", got)
	}
}

// TestStatusCORSPreflightHit 白名单预检：204 + 方法/头 + Vary。
func TestStatusCORSPreflightHit(t *testing.T) {
	r := newCORSTestRouter()

	// 携带 Access-Control-Request-Headers 时追加 Allow-Headers。
	w := do(t, r, http.MethodOptions, "https://mcnb.dev", map[string]string{
		"Access-Control-Request-Method":  http.MethodGet,
		"Access-Control-Request-Headers": "Content-Type",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://mcnb.dev" {
		t.Errorf("Access-Control-Allow-Origin = %q, want https://mcnb.dev", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != http.MethodGet {
		t.Errorf("Access-Control-Allow-Methods = %q, want GET", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type" {
		t.Errorf("Access-Control-Allow-Headers = %q, want Content-Type", got)
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
	if w.Body.Len() != 0 {
		t.Errorf("preflight body = %q, want 空", w.Body.String())
	}

	// 未请求自定义头时不下发 Allow-Headers。
	w = do(t, r, http.MethodOptions, "https://www.mcnb.dev", map[string]string{
		"Access-Control-Request-Method": http.MethodGet,
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://www.mcnb.dev" {
		t.Errorf("Access-Control-Allow-Origin = %q, want https://www.mcnb.dev", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "" {
		t.Errorf("Access-Control-Allow-Headers = %q, want 空（预检未请求头）", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want 空（绝不下发）", got)
	}
}

// TestStatusCORSPreflightMiss 未命中预检：204 但不回显 Origin、无 CORS 头。
func TestStatusCORSPreflightMiss(t *testing.T) {
	r := newCORSTestRouter()
	for _, origin := range []string{"https://evil.example", ""} {
		w := do(t, r, http.MethodOptions, origin, map[string]string{
			"Access-Control-Request-Method": http.MethodGet,
		})
		if w.Code != http.StatusNoContent {
			t.Fatalf("%q: preflight status = %d, want 204", origin, w.Code)
		}
		assertNoCORS(t, w)
		if got := w.Header().Get("Access-Control-Allow-Methods"); got != "" {
			t.Errorf("%q: Access-Control-Allow-Methods = %q, want 空", origin, got)
		}
	}
}
