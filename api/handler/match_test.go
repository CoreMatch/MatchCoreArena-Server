package handler_test

import (
	"context"
	"encoding/json"
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

// fakeVerifier 按 token 字符串返回预设的校验结果，用于驱动
// Auth 中间件的全部分支。
type fakeVerifier struct {
	results map[string]*auth.TokenResult
	errs    map[string]error
}

func (f *fakeVerifier) Verify(_ context.Context, token string) (*auth.TokenResult, error) {
	if err, ok := f.errs[token]; ok {
		return nil, err
	}
	if r, ok := f.results[token]; ok {
		return r, nil
	}
	return nil, auth.ErrTokenInvalid
}

func (f *fakeVerifier) InvalidateCache(context.Context, string) {}

// fakeMatchService 记录 Report 调用，避免依赖数据库。
type fakeMatchService struct {
	reportCalls int
	lastUID     int64
	lastInput   service.TeamReportInput
	reportErr   error // 非 nil 时 Report 返回该错误
}

func (f *fakeMatchService) Report(_ context.Context, uid int64, in service.TeamReportInput) (*service.Match, error) {
	f.reportCalls++
	f.lastUID = uid
	f.lastInput = in
	if f.reportErr != nil {
		return nil, f.reportErr
	}
	return &service.Match{ID: 42, MatchType: in.MatchType}, nil
}

func (f *fakeMatchService) ReportVirtual(_ context.Context, uid int64, in service.VirtualReportInput) (*service.VirtualMatchResult, error) {
	f.reportCalls++
	f.lastUID = uid
	if f.reportErr != nil {
		return nil, f.reportErr
	}
	return &service.VirtualMatchResult{Match: service.Match{ID: 43, MatchType: in.MatchType, Source: service.MatchSourceVirtual}}, nil
}

func (f *fakeMatchService) GetByID(context.Context, int64) (*service.Match, error) {
	return nil, nil
}

func (f *fakeMatchService) ListByUser(context.Context, int64, service.Page) ([]*service.Match, error) {
	return nil, nil
}

// newMatchRouter 装配带鉴权中间件的 POST /api/matches 路由。
func newMatchRouter(v auth.Verifier, svc service.MatchService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	api := r.Group("/api")
	api.Use(middleware.Auth(v))
	h := handler.NewMatchHandler(svc, true)
	api.POST("/matches", h.Report)
	return r
}

// validReportBody 满足 TeamReportInput 全部 binding:"required" 的最小载荷。
const validReportBody = `{
	"match_type": "bedwars",
	"winner_team": {"team_id": "A", "members": [{"uuid": "p1"}]},
	"loser_teams": [{"team_id": "B", "members": [{"uuid": "p2"}]}],
	"started_at": "2026-11-05T10:00:00Z",
	"finished_at": "2026-11-05T10:10:00Z"
}`

func newScopeVerifier() *fakeVerifier {
	return &fakeVerifier{
		results: map[string]*auth.TokenResult{
			"tok-report":      {UID: 7, Scopes: []string{"user.read", "match:report"}},
			"tok-plain":       {UID: 8, Scopes: []string{"user.read"}},
			"tok-maintenance": {UID: 9, Scopes: []string{"maintenance"}},
		},
		errs: map[string]error{
			"tok-expired": auth.ErrTokenExpired,
			"tok-invalid": auth.ErrTokenInvalid,
		},
	}
}

func doMatch(t *testing.T, r *gin.Engine, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(http.MethodPost, "/api/matches", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(http.MethodPost, "/api/matches", nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// assertEnvelope 断言统一错误信封并返回 code。
func assertEnvelope(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, w.Body.String())
	}
	if env.Success {
		t.Fatalf("success must be false on the error path (body=%s)", w.Body.String())
	}
	return env.Code
}

// TestMatchReportScopeMatrix 覆盖受保护组 POST /api/matches 的
// scope 矩阵：有/无 match:report、maintenance 全权、过期/无效令牌、
// 缺令牌、错误载荷（契约 matchcorearena-business.yaml）。
func TestMatchReportScopeMatrix(t *testing.T) {
	t.Run("with match:report scope succeeds", func(t *testing.T) {
		svc := &fakeMatchService{}
		r := newMatchRouter(newScopeVerifier(), svc)

		w := doMatch(t, r, "tok-report", validReportBody)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		if svc.reportCalls != 1 {
			t.Fatalf("expected Report to be called once, got %d", svc.reportCalls)
		}
		if svc.lastUID != 7 {
			t.Errorf("reporter uid = %d, want 7", svc.lastUID)
		}
		if svc.lastInput.MatchType != "bedwars" {
			t.Errorf("match_type = %q, want bedwars", svc.lastInput.MatchType)
		}
	})

	t.Run("without match:report scope is rejected 403", func(t *testing.T) {
		svc := &fakeMatchService{}
		r := newMatchRouter(newScopeVerifier(), svc)

		w := doMatch(t, r, "tok-plain", validReportBody)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "oauth_insufficient_scope" {
			t.Fatalf("code = %q, want oauth_insufficient_scope", code)
		}
		if svc.reportCalls != 0 {
			t.Errorf("Report must not be called without the scope, got %d calls", svc.reportCalls)
		}
	})

	t.Run("maintenance scope grants match:report", func(t *testing.T) {
		svc := &fakeMatchService{}
		r := newMatchRouter(newScopeVerifier(), svc)

		w := doMatch(t, r, "tok-maintenance", validReportBody)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		if svc.reportCalls != 1 {
			t.Fatalf("expected Report to be called once, got %d", svc.reportCalls)
		}
	})

	t.Run("expired token is rejected 401", func(t *testing.T) {
		svc := &fakeMatchService{}
		r := newMatchRouter(newScopeVerifier(), svc)

		w := doMatch(t, r, "tok-expired", validReportBody)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "oauth_token_expired" {
			t.Fatalf("code = %q, want oauth_token_expired", code)
		}
		if svc.reportCalls != 0 {
			t.Errorf("Report must not be called with an expired token, got %d calls", svc.reportCalls)
		}
	})

	t.Run("invalid token is rejected 401", func(t *testing.T) {
		svc := &fakeMatchService{}
		r := newMatchRouter(newScopeVerifier(), svc)

		w := doMatch(t, r, "tok-invalid", validReportBody)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "oauth_invalid_grant" {
			t.Fatalf("code = %q, want oauth_invalid_grant", code)
		}
	})

	t.Run("missing token is rejected 401", func(t *testing.T) {
		svc := &fakeMatchService{}
		r := newMatchRouter(newScopeVerifier(), svc)

		w := doMatch(t, r, "", validReportBody)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "oauth_login_required" {
			t.Fatalf("code = %q, want oauth_login_required", code)
		}
	})

	t.Run("malformed payload is rejected 400", func(t *testing.T) {
		svc := &fakeMatchService{}
		r := newMatchRouter(newScopeVerifier(), svc)

		// 缺少全部 binding:"required" 字段。
		w := doMatch(t, r, "tok-report", `{"match_type": ""}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "mca_invalid_request" {
			t.Fatalf("code = %q, want mca_invalid_request", code)
		}
		if svc.reportCalls != 0 {
			t.Errorf("Report must not be called for a malformed payload, got %d calls", svc.reportCalls)
		}
	})

	t.Run("non-json body is rejected 400", func(t *testing.T) {
		svc := &fakeMatchService{}
		r := newMatchRouter(newScopeVerifier(), svc)

		w := doMatch(t, r, "tok-report", `{not-json`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if code := assertEnvelope(t, w); code != "mca_invalid_request" {
			t.Fatalf("code = %q, want mca_invalid_request", code)
		}
	})
}

// TestMatchReportProvisioningFailedMapping 钉住"懒建行失败"（如 uid 被
// 软删除行占用）的对外错误形态：类型化的 mca_user_not_provisioned + 409，
// 而不是裸 internal_error + 500。正常缺行由结算事务内懒初始化覆盖
// （见 service 层测试），此码仅在建行不可能时出现。
func TestMatchReportProvisioningFailedMapping(t *testing.T) {
	svc := &fakeMatchService{
		reportErr: apperr.New(apperr.CodeMCAUserNotProvisioned,
			"参与者 (uid: 7) users 行无法建立（uid 可能被已删除账户占用）"),
	}
	r := newMatchRouter(newScopeVerifier(), svc)

	w := doMatch(t, r, "tok-report", validReportBody)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	if code := assertEnvelope(t, w); code != "mca_user_not_provisioned" {
		t.Fatalf("code = %q, want mca_user_not_provisioned", code)
	}
	if svc.reportCalls != 1 {
		t.Fatalf("expected Report to be called once, got %d", svc.reportCalls)
	}
}
