package service_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/service"
)

// newFakeHRPAuth 返回一个最小假 HRPAuth：/status 供客户端启动校验，
// /oauth/token 为 client_credentials 签发服务令牌，
// /services/users/uuid-1 把 uuid-1 解析为 uid 7（ops 级服务端点，
// 契约 hrpauth-business.yaml）。
func newFakeHRPAuth(t *testing.T) *auth.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/status":
			w.WriteHeader(http.StatusOK)
		case "/oauth/token":
			if r.FormValue("grant_type") != "client_credentials" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"svc-token","expires_in":3600},"access_token":"svc-token","expires_in":3600}`))
		case "/services/users/uuid-1":
			if r.Header.Get("Authorization") != "Bearer svc-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"uid":7,"uuid":"uuid-1","email":"a@b.c","username":"alice"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	cli, err := auth.NewClient(srv.URL, "test-client", "svc-client-id", "svc-client-secret")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return cli
}

// TestReportMissingUsersRow 覆盖"参与者缺本地 users 行"的结算失败路径：
// 错误必须是类型化的 mca_user_not_provisioned（409），而不是裸
// internal_error（500）；且事务在任何写入前回滚，matches 主表不落任何行。
// （建行机制本身待作者决策，见 PENDING-DECISIONS 第 4 条；此测试只钉住
// 错误形态与回滚行为，不引入自动建行。）
func TestReportMissingUsersRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cli := newFakeHRPAuth(t)
	svc := service.NewMatchService(db, cli, nil) // redis=nil：强制走 HRPAuth 解析

	// 事务内第一步查评分信息即无行 → 期望 BEGIN、SELECT 无行、ROLLBACK。
	// 刻意不注册任何 INSERT 期望：若代码尝试写 matches，sqlmock 会报
	// 未预期调用，测试即失败。
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}))
	mock.ExpectRollback()

	in := service.TeamReportInput{
		MatchType: "bedwars",
		WinnerTeam: service.TeamInfo{
			TeamID:  "A",
			Members: []service.ParticipantReport{{UUID: "uuid-1"}},
		},
		StartedAt:  "2026-11-05T10:00:00Z",
		FinishedAt: "2026-11-05T10:10:00Z",
	}

	_, err = svc.Report(t.Context(), 1, in)
	if err == nil {
		t.Fatal("expected an error for a participant without a local users row")
	}

	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected a typed *apperr.Error, got %T: %v", err, err)
	}
	if ae.Code != apperr.CodeMCAUserNotProvisioned {
		t.Fatalf("code = %q, want mca_user_not_provisioned", ae.Code)
	}
	if ae.HTTPStatus() != http.StatusConflict {
		t.Fatalf("status = %d, want 409", ae.HTTPStatus())
	}

	// BEGIN + SELECT + ROLLBACK 全部消费、无未预期的 INSERT → matches 未写入。
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (matches must not be written, tx must roll back): %v", err)
	}
}
