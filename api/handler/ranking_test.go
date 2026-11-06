package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/handler"
	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/config"
	"MatchCoreArena-Server/internal/auth"
)

// newTiersRouter 装配带鉴权中间件的 GET /api/rankings/tiers 路由。
func newTiersRouter(v auth.Verifier, tiers []config.RankTier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	api := r.Group("/api")
	api.Use(middleware.Auth(v))
	h := handler.NewRankingHandler(nil, tiers)
	api.GET("/rankings/tiers", h.Tiers)
	return r
}

// TestRankingTiers 覆盖段位阶梯展示端点（契约
// matchcorearena-business.yaml GET /api/rankings/tiers）：
// 纯展示配置透传；未配置返回空数组（前端不显示进度条）；需要鉴权。
func TestRankingTiers(t *testing.T) {
	verifier := &fakeVerifier{
		results: map[string]*auth.TokenResult{
			"tok-user": {UID: 7, Scopes: []string{"user.read"}},
		},
	}

	t.Run("configured tiers are returned in order", func(t *testing.T) {
		tiers := []config.RankTier{
			{Name: "青铜", MinScore: 0},
			{Name: "白银", MinScore: 1000},
		}
		r := newTiersRouter(verifier, tiers)

		req := httptest.NewRequest(http.MethodGet, "/api/rankings/tiers", nil)
		req.Header.Set("Authorization", "Bearer tok-user")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var env struct {
			Data []config.RankTier `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v (body=%s)", err, w.Body.String())
		}
		if len(env.Data) != 2 || env.Data[0].Name != "青铜" || env.Data[1].MinScore != 1000 {
			t.Errorf("tiers = %+v, want 青铜(0) 白银(1000)", env.Data)
		}
	})

	t.Run("unconfigured returns empty array", func(t *testing.T) {
		r := newTiersRouter(verifier, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/rankings/tiers", nil)
		req.Header.Set("Authorization", "Bearer tok-user")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var env struct {
			Data []config.RankTier `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v (body=%s)", err, w.Body.String())
		}
		if env.Data == nil || len(env.Data) != 0 {
			t.Errorf("data = %v, want empty non-null array", env.Data)
		}
	})

	t.Run("missing token is rejected 401", func(t *testing.T) {
		r := newTiersRouter(verifier, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/rankings/tiers", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
		}
	})
}
