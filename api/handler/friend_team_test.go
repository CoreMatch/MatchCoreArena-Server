package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/handler"
	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/service"
)

// newFriendTeamRouter 装配好友/战队全部路由，使用真实（未实现）service
// 与 fakeVerifier，验证 501 mca_not_implemented 信封贯穿 handler 层。
func newFriendTeamRouter(t *testing.T, v auth.Verifier, authCli *auth.Client) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	api := r.Group("/api")
	api.Use(middleware.Auth(v))

	friendH := handler.NewFriendHandler(service.NewFriendService(nil), authCli, nil)
	teamH := handler.NewTeamHandler(service.NewTeamService(nil))

	api.GET("/friends", friendH.List)
	api.POST("/friends", friendH.Request)
	api.PUT("/friends/:id/accept", friendH.Accept)
	api.PUT("/friends/:id/reject", friendH.Reject)
	api.DELETE("/friends/:id", friendH.Delete)

	api.POST("/teams", teamH.Create)
	api.GET("/teams/:id", teamH.GetByID)
	api.DELETE("/teams/:id", teamH.Delete)
	api.GET("/teams/:id/members", teamH.ListMembers)
	api.POST("/teams/:id/members", teamH.AddMember)
	api.PUT("/teams/:id/members/:uid/role", teamH.UpdateMemberRole)
	api.DELETE("/teams/:id/members/:uid", teamH.RemoveMember)
	return r
}

// newLookupStub 提供 /status 与 POST /user/lookup（username→uid 8），
// 供 POST /api/friends 走到 service 层。
func newLookupStub(t *testing.T) *auth.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/status":
			w.WriteHeader(http.StatusOK)
		case "/user/lookup":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uid":8,"username":"bob"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	cli, err := auth.NewClient(srv.URL, "test-client", "", "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return cli
}

// TestFriendTeamEndpointsAnswer501 钉住全部 12 个好友/战队端点以
// 501 + mca_not_implemented 统一信封作答，不再返回假数据或假成功
//（契约 matchcorearena-business.yaml；错误码 mca_not_implemented）。
func TestFriendTeamEndpointsAnswer501(t *testing.T) {
	verifier := &fakeVerifier{
		results: map[string]*auth.TokenResult{
			"tok-user": {UID: 7, Scopes: []string{"user.read"}},
		},
	}
	r := newFriendTeamRouter(t, verifier, newLookupStub(t))

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/friends", ""},
		{http.MethodPost, "/api/friends", `{"username":"bob"}`},
		{http.MethodPut, "/api/friends/1/accept", ""},
		{http.MethodPut, "/api/friends/1/reject", ""},
		{http.MethodDelete, "/api/friends/1", ""},
		{http.MethodPost, "/api/teams", `{"name":"t","description":"d"}`},
		{http.MethodGet, "/api/teams/1", ""},
		{http.MethodDelete, "/api/teams/1", ""},
		{http.MethodGet, "/api/teams/1/members", ""},
		{http.MethodPost, "/api/teams/1/members", `{"user_uid":8}`},
		{http.MethodPut, "/api/teams/1/members/8/role", `{"role":1}`},
		{http.MethodDelete, "/api/teams/1/members/8", ""},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			req.Header.Set("Authorization", "Bearer tok-user")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501 (body=%s)", w.Code, w.Body.String())
			}
			if code := assertEnvelope(t, w); code != "mca_not_implemented" {
				t.Fatalf("code = %q, want mca_not_implemented", code)
			}
		})
	}
}
