package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/config"
)

// newTestRouter assembles the full route table without external
// dependencies. Handlers that need a database or the HRPAuth client are
// constructed with nil dependencies; the tests below only hit endpoints
// that do not reach for them.
func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, nil, nil, nil, nil, "test-version", config.AppConfig{})
	return r
}

// TestStatusEndpoint covers GET /api/status (contract
// matchcorearena-business.yaml): public, unified envelope, version in data.
func TestStatusEndpoint(t *testing.T) {
	r := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Version string `json:"version"`
		} `json:"data"`
		Meta struct {
			RequestID string `json:"request_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, w.Body.String())
	}
	if !env.Success {
		t.Fatal("success must be true")
	}
	if env.Data.Version != "test-version" {
		t.Fatalf("data.version = %q, want test-version", env.Data.Version)
	}
}

// TestStatusCORSEndToEnd 覆盖 /api/status 的收紧 CORS（契约
// matchcorearena-business.yaml 1.8.0）：白名单命中回显 Origin，未命中
// 不回显，预检 204 带方法/头，其余路由不挂 CORS。
func TestStatusCORSEndToEnd(t *testing.T) {
	r := newTestRouter(t)

	// 白名单命中：回显 Origin + Vary。
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Origin", "https://mcnb.dev")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://mcnb.dev" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want https://mcnb.dev", got)
	}

	// 未命中：照常 200 但不回显 Origin。
	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want 空（未命中不得回显）", got)
	}

	// 预检：204 + 方法/头。
	req = httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	req.Header.Set("Origin", "https://www.mcnb.dev")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://www.mcnb.dev" {
		t.Fatalf("preflight Access-Control-Allow-Origin = %q, want https://www.mcnb.dev", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != http.MethodGet {
		t.Fatalf("preflight Access-Control-Allow-Methods = %q, want GET", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type" {
		t.Fatalf("preflight Access-Control-Allow-Headers = %q, want Content-Type", got)
	}

	// CORS 只挂 /api/status：鉴权路由不得带 CORS 头。
	req = httptest.NewRequest(http.MethodGet, "/api/users/me", nil)
	req.Header.Set("Origin", "https://mcnb.dev")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("/api/users/me Access-Control-Allow-Origin = %q, want 空（CORS 仅限 /api/status）", got)
	}
}

// TestProtectedEndpointsRequireBearer covers the auth gate error path: a
// protected route without a token answers 401 oauth_login_required.
func TestProtectedEndpointsRequireBearer(t *testing.T) {
	r := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/users/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
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
	if env.Code != "oauth_login_required" {
		t.Fatalf("code = %q, want oauth_login_required", env.Code)
	}
}
