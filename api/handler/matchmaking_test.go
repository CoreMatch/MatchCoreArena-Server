package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/handler"
	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/service"
)

// fakeMatchmakingService 记录匹配调用，避免依赖数据库。
type fakeMatchmakingService struct {
	joinCalls  int
	leaveCalls int
	lastUID    int64
	lastType   string
	joinErr    error
	leaveErr   error
}

func (f *fakeMatchmakingService) Join(_ context.Context, uid int64, matchType string) (*service.QueueEntry, error) {
	f.joinCalls++
	f.lastUID = uid
	f.lastType = matchType
	if f.joinErr != nil {
		return nil, f.joinErr
	}
	return &service.QueueEntry{UID: uid, MatchType: matchType, RankScore: 1500}, nil
}

func (f *fakeMatchmakingService) Leave(_ context.Context, uid int64) error {
	f.leaveCalls++
	f.lastUID = uid
	return f.leaveErr
}

func (f *fakeMatchmakingService) Status(_ context.Context, uid int64) (*service.MatchmakingStatus, error) {
	f.lastUID = uid
	return &service.MatchmakingStatus{InQueue: false, RecentGroups: []service.FormedGroup{}}, nil
}

// newMatchmakingRouter 装配带鉴权中间件的匹配路由。
func newMatchmakingRouter(v auth.Verifier, svc service.MatchmakingService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	api := r.Group("/api")
	api.Use(middleware.Auth(v))
	h := handler.NewMatchmakingHandler(svc)
	api.POST("/matchmaking/join", h.Join)
	api.DELETE("/matchmaking/leave", h.Leave)
	api.GET("/matchmaking/status", h.Status)
	return r
}

// TestMatchmakingEndpoints 覆盖匹配端点的鉴权与错误信封
//（契约 matchcorearena-business.yaml /api/matchmaking/*）。
func TestMatchmakingEndpoints(t *testing.T) {
	verifier := &fakeVerifier{
		results: map[string]*auth.TokenResult{
			"tok-user": {UID: 7, Scopes: []string{"user.read"}},
		},
	}

	t.Run("join succeeds for any authenticated user", func(t *testing.T) {
		svc := &fakeMatchmakingService{}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodPost, "/api/matchmaking/join",
			strings.NewReader(`{"match_type": "1v1"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer tok-user")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		if svc.joinCalls != 1 || svc.lastUID != 7 || svc.lastType != "1v1" {
			t.Errorf("unexpected join call: %+v", svc)
		}
	})

	t.Run("join without token is rejected 401", func(t *testing.T) {
		svc := &fakeMatchmakingService{}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodPost, "/api/matchmaking/join",
			strings.NewReader(`{"match_type": "1v1"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
		}
		if svc.joinCalls != 0 {
			t.Errorf("Join must not be called without a token")
		}
	})

	t.Run("duplicate join surfaces 409 envelope", func(t *testing.T) {
		svc := &fakeMatchmakingService{joinErr: apperr.New(apperr.CodeMCAQueueAlreadyJoined, "已在匹配队列中")}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodPost, "/api/matchmaking/join",
			strings.NewReader(`{"match_type": "1v1"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer tok-user")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "mca_queue_already_joined" {
			t.Fatalf("code = %q, want mca_queue_already_joined", code)
		}
	})

	t.Run("leave without queue entry surfaces 404 envelope", func(t *testing.T) {
		svc := &fakeMatchmakingService{leaveErr: apperr.New(apperr.CodeMCAQueueNotJoined, "未在匹配队列中")}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodDelete, "/api/matchmaking/leave", nil)
		req.Header.Set("Authorization", "Bearer tok-user")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "mca_queue_not_joined" {
			t.Fatalf("code = %q, want mca_queue_not_joined", code)
		}
	})

	t.Run("status returns envelope for authenticated user", func(t *testing.T) {
		svc := &fakeMatchmakingService{}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodGet, "/api/matchmaking/status", nil)
		req.Header.Set("Authorization", "Bearer tok-user")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		if svc.lastUID != 7 {
			t.Errorf("status uid = %d, want 7", svc.lastUID)
		}
	})
}
