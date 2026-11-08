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
		case "/services/users/uuid-2":
			if r.Header.Get("Authorization") != "Bearer svc-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"uid":8,"uuid":"uuid-2","email":"b@b.c","username":"bob"}}`))
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

// singleWinnerReport 返回最小合法上报：胜队一名参与者（uuid-1）对
// 败队一名参与者（uuid-2）。所有评分字段留零值——建行与初始积分
// 必须来自系统默认。
func singleWinnerReport() service.TeamReportInput {
	return service.TeamReportInput{
		MatchType: "bedwars",
		WinnerTeam: service.TeamInfo{
			TeamID:       "A",
			InitialCount: 1,
			AliveCount:   1,
			Members:      []service.ParticipantReport{{UUID: "uuid-1"}},
		},
		LoserTeams: []service.TeamInfo{{
			TeamID:       "B",
			InitialCount: 1,
			AliveCount:   1,
			Members:      []service.ParticipantReport{{UUID: "uuid-2"}},
		}},
		StartedAt:  "2026-11-05T10:00:00Z",
		FinishedAt: "2026-11-05T10:10:00Z",
	}
}

// expectFullSettlementAfterRating 登记"评分信息已就绪后"的完整结算
// SQL 期望（matches 主表 12 列 → 双方连胜查询 → 逐个写入：users 更新
// → 参与者明细 10 列 → 排行榜）。
func expectFullSettlementAfterRating(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`INSERT INTO matches`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`SELECT mp\.is_winner`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"is_winner"}))
	mock.ExpectQuery(`SELECT mp\.is_winner`).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"is_winner"}))
	for _, uid := range []int64{7, 8} {
		mock.ExpectExec(`UPDATE users SET rank_score`).
			WithArgs(sqlmock.AnyArg(), uid).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`INSERT INTO match_participants`).
			WithArgs(sqlmock.AnyArg(), uid, sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \?`).
			WithArgs(uid).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(25))
		mock.ExpectExec(`INSERT INTO rankings`).
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
}

// TestReportLazyProvisioning 覆盖懒初始化主路径：参与者缺本地 users 行
// 时，结算事务内自动建行（uid 只来自 HRPAuth 解析结果，初始评分取表
// 默认值），随后正常完成结算。历史依据：bb8b7c6 原有此行为，5161b1b
// 删除，现按作者决策恢复并收紧信任边界。
func TestReportLazyProvisioning(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cli := newFakeHRPAuth(t)
	svc := service.NewMatchService(db, cli, nil) // redis=nil：强制走 HRPAuth 解析

	mock.ExpectBegin()
	// 首次查评分：无行。
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}))
	// 懒建行：只写 uid（INSERT IGNORE），uid 必须等于解析出的 7。
	mock.ExpectExec(`INSERT IGNORE INTO users \(uid\) VALUES \(\?\)`).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 建行后复查：表默认值（0 分 0 胜）。
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	// 败方参与者：本地已有行。
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	expectFullSettlementAfterRating(mock)

	m, replayed, err := svc.Report(t.Context(), 1, singleWinnerReport())
	if err != nil {
		t.Fatalf("lazy provisioning should settle the match, got: %v", err)
	}
	if replayed {
		t.Error("report without game_id must not be a replay")
	}
	if m.ID != 1 || m.MatchType != "bedwars" {
		t.Fatalf("unexpected match result: %+v", m)
	}
	// 全部期望消费（含建行 INSERT 恰好一次、无回滚）→ 结算完整落库。
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestReportProvisioningIdempotent 覆盖幂等：本地已有 users 行时
// 直接读取，绝不重复建行（未登记任何 INSERT 期望，若代码尝试建行，
// sqlmock 会以未预期调用报错）。
func TestReportProvisioningIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cli := newFakeHRPAuth(t)
	svc := service.NewMatchService(db, cli, nil)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(100, 3))
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	expectFullSettlementAfterRating(mock)

	if _, _, err := svc.Report(t.Context(), 1, singleWinnerReport()); err != nil {
		t.Fatalf("existing row should settle without provisioning, got: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (no INSERT must happen for an existing row): %v", err)
	}
}

// TestReportProvisioningImpossible 覆盖建行不可能的判据冲突：
// INSERT IGNORE 被忽略（如 uid 被软删除行占用）→ 复查仍无行 →
// 类型化 mca_user_not_provisioned（409），事务回滚，matches 主表
// 不落任何行（未登记 INSERT matches 期望）。
func TestReportProvisioningImpossible(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cli := newFakeHRPAuth(t)
	svc := service.NewMatchService(db, cli, nil)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}))
	mock.ExpectExec(`INSERT IGNORE INTO users \(uid\) VALUES \(\?\)`).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 被忽略：0 行影响
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}))
	mock.ExpectRollback()

	_, _, err = svc.Report(t.Context(), 1, singleWinnerReport())
	if err == nil {
		t.Fatal("expected an error when provisioning is impossible")
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
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (tx must roll back before any match write): %v", err)
	}
}
