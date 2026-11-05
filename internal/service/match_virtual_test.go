package service_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/service"
)

// newVirtualFakeHRPAuth 与 newFakeHRPAuth 相同，但额外把 uuid-2 解析为
// uid 8，用于双人虚拟对局。
func newVirtualFakeHRPAuth(t *testing.T) *auth.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/status":
			w.WriteHeader(http.StatusOK)
		case "/oauth/token":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"svc-token","expires_in":3600},"access_token":"svc-token","expires_in":3600}`))
		case "/services/users/uuid-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"uid":7,"uuid":"uuid-1","email":"a@b.c","username":"alice"}}`))
		case "/services/users/uuid-2":
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

// winLoseVirtualReport A 队（uuid-1）胜 B 队（uuid-2），各 1 人满存活。
func winLoseVirtualReport() service.VirtualReportInput {
	return service.VirtualReportInput{
		MatchType: "1v1",
		Teams: []service.VirtualTeamInput{
			{TeamID: "A", Result: service.ResultWin, InitialCount: 1, AliveCount: 1,
				Members: []service.ParticipantReport{{UUID: "uuid-1"}}},
			{TeamID: "B", Result: service.ResultLose, InitialCount: 1, AliveCount: 1,
				Members: []service.ParticipantReport{{UUID: "uuid-2"}}},
		},
		StartedAt:  "2026-11-06T10:00:00Z",
		FinishedAt: "2026-11-06T10:10:00Z",
	}
}

// expectVirtualPlayerSettlement 登记单个参与者的结算 SQL 期望
//（连胜查询 → users 更新 → 参与者明细 → 排行榜）。
func expectVirtualPlayerSettlement(mock sqlmock.Sqlmock, uid int64, delta int, withStreakQuery bool) {
	if withStreakQuery {
		mock.ExpectQuery(`SELECT mp\.is_winner, mp\.is_draw`).
			WithArgs(uid).
			WillReturnRows(sqlmock.NewRows([]string{"is_winner", "is_draw"}))
	}
	mock.ExpectExec(`UPDATE users SET rank_score`).
		WithArgs(delta, uid).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO match_participants`).
		WithArgs(sqlmock.AnyArg(), uid, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), delta, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \?`).
		WithArgs(uid).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(delta))
	mock.ExpectExec(`INSERT INTO rankings`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

// TestReportVirtualWinLose 覆盖有获胜队的虚拟对局：公式与游戏上报路径
// 一致。双方初始 0 分：E=0.5，K=520，完胜生存权重 1.25，连胜权重 1.0
// → 胜者 +325、败者 -325（下限 0）。
func TestReportVirtualWinLose(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := service.NewMatchService(db, newVirtualFakeHRPAuth(t), nil)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	mock.ExpectExec(`INSERT INTO matches`).
		WithArgs("1v1", service.MatchSourceVirtual, "", "A", 1.0, 0,
			time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC),
			time.Date(2026, 11, 6, 10, 10, 0, 0, time.UTC)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	expectVirtualPlayerSettlement(mock, 7, 325, true)
	expectVirtualPlayerSettlement(mock, 8, -325, true)
	mock.ExpectCommit()

	result, err := svc.ReportVirtual(t.Context(), 1, winLoseVirtualReport())
	if err != nil {
		t.Fatalf("virtual win/lose settlement failed: %v", err)
	}
	if result.Match.Source != service.MatchSourceVirtual {
		t.Errorf("source = %q, want virtual", result.Match.Source)
	}
	if result.Match.WinnerTeamID != "A" {
		t.Errorf("winner_team_id = %q, want A", result.Match.WinnerTeamID)
	}
	if len(result.Participants) != 2 {
		t.Fatalf("participants = %d, want 2", len(result.Participants))
	}
	for _, p := range result.Participants {
		switch p.UID {
		case 7:
			if p.RankScoreDelta != 325 || p.RankScoreAfter != 325 || p.Result != service.ResultWin {
				t.Errorf("winner settlement = %+v, want delta 325 after 325 result win", p)
			}
		case 8:
			if p.RankScoreDelta != -325 || p.RankScoreAfter != 0 || p.Result != service.ResultLose {
				t.Errorf("loser settlement = %+v, want delta -325 after 0 result lose", p)
			}
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestReportVirtualDraw 覆盖全平局：S=0.5，各队对对方平均分求期望。
// uid7=1000 对 uid8=1200：E7≈0.2402 → +169；E8≈0.7598 → -169。
// 平局不查询连胜（连胜清零），不计胜场。
func TestReportVirtualDraw(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := service.NewMatchService(db, newVirtualFakeHRPAuth(t), nil)

	in := service.VirtualReportInput{
		MatchType: "1v1",
		Teams: []service.VirtualTeamInput{
			{TeamID: "A", Result: service.ResultDraw, InitialCount: 1, AliveCount: 1,
				Members: []service.ParticipantReport{{UUID: "uuid-1"}}},
			{TeamID: "B", Result: service.ResultDraw, InitialCount: 1, AliveCount: 1,
				Members: []service.ParticipantReport{{UUID: "uuid-2"}}},
		},
		StartedAt:  "2026-11-06T10:00:00Z",
		FinishedAt: "2026-11-06T10:10:00Z",
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(1000, 12))
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(1200, 12))
	mock.ExpectExec(`INSERT INTO matches`).
		WithArgs("1v1", service.MatchSourceVirtual, "", "", 0.0, 0,
			time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC),
			time.Date(2026, 11, 6, 10, 10, 0, 0, time.UTC)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	// 稳定期 K=(165+215)/2=190：delta = 190*(0.5-E)*1.25。
	expectVirtualPlayerSettlement(mock, 7, 62, false)
	expectVirtualPlayerSettlement(mock, 8, -62, false)
	mock.ExpectCommit()

	result, err := svc.ReportVirtual(t.Context(), 1, in)
	if err != nil {
		t.Fatalf("virtual draw settlement failed: %v", err)
	}
	if result.Match.WinnerTeamID != "" {
		t.Errorf("winner_team_id = %q, want empty for a draw", result.Match.WinnerTeamID)
	}
	for _, p := range result.Participants {
		if p.Result != service.ResultDraw {
			t.Errorf("participant result = %q, want draw", p.Result)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (no streak query for draws): %v", err)
	}
}

// TestReportVirtualValidation 覆盖输入校验：全部在事务与数据库交互之前
// 以 mca_invalid_request 拒绝（sqlmock 未登记任何期望，触库即失败）。
func TestReportVirtualValidation(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchService(db, newVirtualFakeHRPAuth(t), nil)

	cases := []struct {
		name   string
		mutate func(*service.VirtualReportInput)
	}{
		{"single team", func(in *service.VirtualReportInput) { in.Teams = in.Teams[:1] }},
		{"two winners", func(in *service.VirtualReportInput) { in.Teams[1].Result = service.ResultWin }},
		{"winner mixed with draw", func(in *service.VirtualReportInput) { in.Teams[1].Result = service.ResultDraw }},
		{"invalid result", func(in *service.VirtualReportInput) { in.Teams[0].Result = "victory" }},
		{"duplicate uuid", func(in *service.VirtualReportInput) { in.Teams[1].Members[0].UUID = "uuid-1" }},
		{"empty members", func(in *service.VirtualReportInput) { in.Teams[0].Members = nil }},
		{"zero initial_count", func(in *service.VirtualReportInput) { in.Teams[0].InitialCount = 0 }},
		{"alive exceeds initial", func(in *service.VirtualReportInput) { in.Teams[0].AliveCount = 5 }},
		{"bad timestamp", func(in *service.VirtualReportInput) { in.StartedAt = "yesterday" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := winLoseVirtualReport()
			tc.mutate(&in)
			_, err := svc.ReportVirtual(t.Context(), 1, in)
			if err == nil {
				t.Fatal("expected an error")
			}
			var ae *apperr.Error
			if !errors.As(err, &ae) {
				t.Fatalf("expected a typed *apperr.Error, got %T: %v", err, err)
			}
			if ae.Code != apperr.CodeMCAInvalidRequest {
				t.Fatalf("code = %q, want mca_invalid_request", ae.Code)
			}
			if ae.HTTPStatus() != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", ae.HTTPStatus())
			}
		})
	}
}
