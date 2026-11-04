package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newTestRouter assembles the full route table without external
// dependencies. Handlers that need a database or the HRPAuth client are
// constructed with nil dependencies; the tests below only hit endpoints
// that do not reach for them.
func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, nil, nil, nil, nil, "test-version")
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
