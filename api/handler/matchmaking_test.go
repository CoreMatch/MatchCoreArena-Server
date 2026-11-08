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
	joinCalls       int
	leaveCalls      int
	assignmentCalls int
	lastUID         int64
	lastType        string
	lastMembers     []string
	lastUUID        string
	joinErr         error
	leaveErr        error
	assignmentErr   error
}

func (f *fakeMatchmakingService) Join(_ context.Context, uid int64, matchType string, partyMembers []string) ([]*service.QueueEntry, error) {
	f.joinCalls++
	f.lastUID = uid
	f.lastType = matchType
	f.lastMembers = partyMembers
	if f.joinErr != nil {
		return nil, f.joinErr
	}
	if len(partyMembers) > 0 {
		entries := make([]*service.QueueEntry, 0, len(partyMembers))
		for i := range partyMembers {
			entries = append(entries, &service.QueueEntry{
				UID: int64(100 + i), MatchType: matchType, RankScore: 1500, PartyID: 5,
			})
		}
		return entries, nil
	}
	return []*service.QueueEntry{{UID: uid, MatchType: matchType, RankScore: 1500}}, nil
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

func (f *fakeMatchmakingService) QueueDepth(context.Context) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func (f *fakeMatchmakingService) Assignment(_ context.Context, uuid string) (*service.MatchAssignment, error) {
	f.assignmentCalls++
	f.lastUUID = uuid
	if f.assignmentErr != nil {
		return nil, f.assignmentErr
	}
	return &service.MatchAssignment{
		Assigned:  true,
		GroupID:   5,
		MatchType: "2v2",
		FormedAt:  "2026-11-08T10:00:00Z",
		TeamID:    "A",
	}, nil
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
	api.GET("/matchmaking/assignment/:uuid", h.Assignment)
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
		if svc.joinCalls != 1 || svc.lastUID != 7 || svc.lastType != "1v1" || len(svc.lastMembers) != 0 {
			t.Errorf("unexpected join call: %+v", svc)
		}
	})

	t.Run("join with party_members forwards member list", func(t *testing.T) {
		svc := &fakeMatchmakingService{}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodPost, "/api/matchmaking/join",
			strings.NewReader(`{"match_type": "2v2", "party_members": ["uuid-a", "uuid-b"]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer tok-user")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		if len(svc.lastMembers) != 2 || svc.lastMembers[0] != "uuid-a" || svc.lastMembers[1] != "uuid-b" {
			t.Errorf("party_members = %v, want [uuid-a uuid-b]", svc.lastMembers)
		}
		// 队伍入队返回整队条目（数组）。
		if !strings.Contains(w.Body.String(), `"party_id":5`) {
			t.Errorf("party join must return all member entries, body=%s", w.Body.String())
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

// TestAssignmentEndpoint 覆盖成局分配查询的 OPS 门控与信封
//（契约 matchcorearena-business.yaml 1.9.0
// GET /api/matchmaking/assignment/{uuid}）。
func TestAssignmentEndpoint(t *testing.T) {
	verifier := &fakeVerifier{
		results: map[string]*auth.TokenResult{
			"tok-user": {UID: 7, Scopes: []string{"user.read"}},
			"tok-ops":  {UID: -100, Scopes: []string{"match:report"}, Ops: true},
		},
	}

	t.Run("ops credential gets assignment", func(t *testing.T) {
		svc := &fakeMatchmakingService{}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodGet, "/api/matchmaking/assignment/uuid-1", nil)
		req.Header.Set("Authorization", "Bearer tok-ops")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		if svc.assignmentCalls != 1 || svc.lastUUID != "uuid-1" {
			t.Errorf("unexpected assignment call: %+v", svc)
		}
		if !strings.Contains(w.Body.String(), `"assigned":true`) || !strings.Contains(w.Body.String(), `"group_id":5`) {
			t.Errorf("body missing assignment payload: %s", w.Body.String())
		}
	})

	t.Run("player token rejected 403", func(t *testing.T) {
		svc := &fakeMatchmakingService{}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodGet, "/api/matchmaking/assignment/uuid-1", nil)
		req.Header.Set("Authorization", "Bearer tok-user")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "oauth_insufficient_scope" {
			t.Fatalf("code = %q, want oauth_insufficient_scope", code)
		}
		if svc.assignmentCalls != 0 {
			t.Errorf("Assignment must not be called for player tokens")
		}
	})

	t.Run("missing token rejected 401", func(t *testing.T) {
		svc := &fakeMatchmakingService{}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodGet, "/api/matchmaking/assignment/uuid-1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("unknown uuid surfaces 404 envelope", func(t *testing.T) {
		svc := &fakeMatchmakingService{assignmentErr: apperr.New(apperr.CodeMCAUserNotFound, "玩家不存在")}
		r := newMatchmakingRouter(verifier, svc)

		req := httptest.NewRequest(http.MethodGet, "/api/matchmaking/assignment/uuid-unknown", nil)
		req.Header.Set("Authorization", "Bearer tok-ops")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "mca_user_not_found" {
			t.Fatalf("code = %q, want mca_user_not_found", code)
		}
	})
}
