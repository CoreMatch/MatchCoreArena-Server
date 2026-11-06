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

// newReportFakeHRPAuth 把 uuid-1/2/3 分别解析为 uid 7/8/9。
func newReportFakeHRPAuth(t *testing.T) *auth.Client {
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
		case "/services/users/uuid-3":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"uid":9,"uuid":"uuid-3","email":"c@b.c","username":"carol"}}`))
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

// winLoseReport A 队（uuid-1）胜 B 队（uuid-2），各 1 人满存活。
func winLoseReport() service.TeamReportInput {
	return service.TeamReportInput{
		MatchType: "1v1",
		Teams: []service.TeamInfo{
			{TeamID: "A", Result: service.ResultWin, InitialCount: 1, AliveCount: 1,
				Members: []service.ParticipantReport{{UUID: "uuid-1"}}},
			{TeamID: "B", Result: service.ResultLose, InitialCount: 1, AliveCount: 1,
				Members: []service.ParticipantReport{{UUID: "uuid-2"}}},
		},
		StartedAt:  "2026-11-06T10:00:00Z",
		FinishedAt: "2026-11-06T10:10:00Z",
	}
}

// expectStreak 登记单个参与者的连胜历史查询期望（全部发生在写入之前）。
func expectStreak(mock sqlmock.Sqlmock, uid int64) {
	mock.ExpectQuery(`SELECT mp\.is_winner, mp\.is_draw`).
		WithArgs(uid).
		WillReturnRows(sqlmock.NewRows([]string{"is_winner", "is_draw"}))
}

// expectPlayerWrites 登记单个参与者的结算写入期望
//（users 更新 → 参与者明细 → 排行榜）。
func expectPlayerWrites(mock sqlmock.Sqlmock, uid int64, delta int) {
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

// TestReportWinLoseUnified 覆盖统一 teams 形状的胜负局：双方初始 0 分，
// E=0.5，K=520，完胜生存权重 1.25，连胜权重 1.0 → 胜者 +325、败者 -325
//（下限 0）。INSERT matches 含 server_id/game_id/payload_hash 新列
//（无 game_id 时为 NULL）。
func TestReportWinLoseUnified(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := service.NewMatchService(db, newReportFakeHRPAuth(t), nil)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	mock.ExpectExec(`INSERT INTO matches`).
		WithArgs("1v1", service.MatchSourceGame, "", "", nil, nil, "A", 1.0, 0,
			time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC),
			time.Date(2026, 11, 6, 10, 10, 0, 0, time.UTC)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	// 先算后写：全部连胜查询完成后才逐个落库。
	expectStreak(mock, 7)
	expectStreak(mock, 8)
	expectPlayerWrites(mock, 7, 325)
	expectPlayerWrites(mock, 8, -325)
	mock.ExpectCommit()

	m, replayed, err := svc.Report(t.Context(), 1, winLoseReport())
	if err != nil {
		t.Fatalf("win/lose settlement failed: %v", err)
	}
	if replayed {
		t.Error("first report must not be a replay")
	}
	if m.Source != service.MatchSourceGame {
		t.Errorf("source = %q, want game (default)", m.Source)
	}
	if m.WinnerTeamID != "A" {
		t.Errorf("winner_team_id = %q, want A", m.WinnerTeamID)
	}
	if len(m.Participants) != 2 {
		t.Fatalf("participants = %d, want 2", len(m.Participants))
	}
	for _, p := range m.Participants {
		switch p.UID {
		case 7:
			if p.RankScoreDelta != 325 || p.RankScoreAfter != 325 || !p.IsWinner {
				t.Errorf("winner settlement = %+v, want delta 325 after 325 is_winner", p)
			}
		case 8:
			if p.RankScoreDelta != -325 || p.RankScoreAfter != 0 || p.IsWinner {
				t.Errorf("loser settlement = %+v, want delta -325 after 0", p)
			}
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestReportCompatShape 覆盖兼容形状（winner_team + loser_teams）与
// teams 形状等价结算。
func TestReportCompatShape(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := service.NewMatchService(db, newReportFakeHRPAuth(t), nil)

	in := service.TeamReportInput{
		MatchType: "1v1",
		WinnerTeam: service.TeamInfo{TeamID: "A", InitialCount: 1, AliveCount: 1,
			Members: []service.ParticipantReport{{UUID: "uuid-1"}}},
		LoserTeams: []service.TeamInfo{{TeamID: "B", InitialCount: 1, AliveCount: 1,
			Members: []service.ParticipantReport{{UUID: "uuid-2"}}}},
		StartedAt:  "2026-11-06T10:00:00Z",
		FinishedAt: "2026-11-06T10:10:00Z",
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	mock.ExpectExec(`INSERT INTO matches`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), "A", 1.0, 0, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	expectStreak(mock, 7)
	expectStreak(mock, 8)
	expectPlayerWrites(mock, 7, 325)
	expectPlayerWrites(mock, 8, -325)
	mock.ExpectCommit()

	m, _, err := svc.Report(t.Context(), 1, in)
	if err != nil {
		t.Fatalf("compat shape settlement failed: %v", err)
	}
	if m.WinnerTeamID != "A" {
		t.Errorf("winner_team_id = %q, want A", m.WinnerTeamID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestReportDrawUnified 覆盖全平局：S=0.5，各队对对方平均分求期望。
// uid7=1000 对 uid8=1200：E7≈0.2402，稳定期 K=190，
// delta = 190*(0.5-E)*1.25 → +62 / -62。平局不查询连胜（连胜清零）。
func TestReportDrawUnified(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := service.NewMatchService(db, newReportFakeHRPAuth(t), nil)

	in := winLoseReport()
	in.Teams[0].Result = service.ResultDraw
	in.Teams[1].Result = service.ResultDraw

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(1000, 12))
	mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(1200, 12))
	mock.ExpectExec(`INSERT INTO matches`).
		WithArgs("1v1", service.MatchSourceGame, "", "", nil, nil, "", 0.0, 0,
			time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC),
			time.Date(2026, 11, 6, 10, 10, 0, 0, time.UTC)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	// 平局不查询连胜（连胜清零），直接写入。
	expectPlayerWrites(mock, 7, 62)
	expectPlayerWrites(mock, 8, -62)
	mock.ExpectCommit()

	m, _, err := svc.Report(t.Context(), 1, in)
	if err != nil {
		t.Fatalf("draw settlement failed: %v", err)
	}
	if m.WinnerTeamID != "" {
		t.Errorf("winner_team_id = %q, want empty for a draw", m.WinnerTeamID)
	}
	for _, p := range m.Participants {
		if !p.IsDraw {
			t.Errorf("participant %d is_draw = false, want true", p.UID)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (no streak query for draws): %v", err)
	}
}

// TestReportValidation 覆盖输入校验：全部在事务与数据库交互之前以
// 类型化错误拒绝（sqlmock 未登记任何期望，触库即失败）。
// 结构性缺失 → mca_invalid_request；自相矛盾 → mca_report_inconsistent。
func TestReportValidation(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchService(db, newReportFakeHRPAuth(t), nil)

	cases := []struct {
		name     string
		mutate   func(*service.TeamReportInput)
		wantCode apperr.Code
	}{
		{"single team", func(in *service.TeamReportInput) { in.Teams = in.Teams[:1] }, apperr.CodeMCAInvalidRequest},
		{"two winners", func(in *service.TeamReportInput) { in.Teams[1].Result = service.ResultWin }, apperr.CodeMCAReportInconsistent},
		{"winner mixed with draw", func(in *service.TeamReportInput) { in.Teams[1].Result = service.ResultDraw }, apperr.CodeMCAReportInconsistent},
		{"invalid result", func(in *service.TeamReportInput) { in.Teams[0].Result = "victory" }, apperr.CodeMCAInvalidRequest},
		{"duplicate uuid", func(in *service.TeamReportInput) { in.Teams[1].Members[0].UUID = "uuid-1" }, apperr.CodeMCAReportInconsistent},
		{"empty members", func(in *service.TeamReportInput) { in.Teams[0].Members = nil }, apperr.CodeMCAInvalidRequest},
		{"zero initial_count", func(in *service.TeamReportInput) { in.Teams[0].InitialCount = 0 }, apperr.CodeMCAReportInconsistent},
		{"alive exceeds initial", func(in *service.TeamReportInput) { in.Teams[0].AliveCount = 5 }, apperr.CodeMCAReportInconsistent},
		{"negative kills", func(in *service.TeamReportInput) { in.Teams[0].Members[0].Kills = -1 }, apperr.CodeMCAReportInconsistent},
		{"negative duration", func(in *service.TeamReportInput) { in.DurationSeconds = -5 }, apperr.CodeMCAReportInconsistent},
		{"perf_tweak above range", func(in *service.TeamReportInput) { in.Teams[0].Members[0].PerfTweak = 1.2 }, apperr.CodeMCAReportInconsistent},
		{"perf_tweak below range", func(in *service.TeamReportInput) { in.Teams[0].Members[0].PerfTweak = 0.5 }, apperr.CodeMCAReportInconsistent},
		{"duration mismatches timestamps", func(in *service.TeamReportInput) { in.DurationSeconds = 999 }, apperr.CodeMCAReportInconsistent},
		{"bad timestamp", func(in *service.TeamReportInput) { in.StartedAt = "yesterday" }, apperr.CodeMCAInvalidRequest},
		{"finished before started", func(in *service.TeamReportInput) { in.FinishedAt = "2026-11-06T09:00:00Z" }, apperr.CodeMCAReportInconsistent},
		{"invalid source", func(in *service.TeamReportInput) { in.Source = "replay" }, apperr.CodeMCAInvalidRequest},
		{"negative matchmaking_group_id", func(in *service.TeamReportInput) { in.MatchmakingGroupID = -1 }, apperr.CodeMCAInvalidRequest},
		{"missing teams and winner", func(in *service.TeamReportInput) { in.Teams = nil; in.WinnerTeam = service.TeamInfo{} }, apperr.CodeMCAInvalidRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := winLoseReport()
			tc.mutate(&in)
			_, replayed, err := svc.Report(t.Context(), 1, in)
			if err == nil {
				t.Fatal("expected an error")
			}
			if replayed {
				t.Error("rejected report must not be a replay")
			}
			var ae *apperr.Error
			if !errors.As(err, &ae) {
				t.Fatalf("expected a typed *apperr.Error, got %T: %v", err, err)
			}
			if ae.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", ae.Code, tc.wantCode)
			}
			if ae.HTTPStatus() != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", ae.HTTPStatus())
			}
		})
	}
}

// TestReportGameIDConflict 覆盖 game_id 幂等冲突：同一 game_id 已存在
// 且载荷哈希不一致 → 409 mca_game_id_conflict，不进入结算事务。
func TestReportGameIDConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchService(db, newReportFakeHRPAuth(t), nil)

	in := winLoseReport()
	in.GameID = "game-1"

	mock.ExpectQuery(`SELECT id, payload_hash FROM matches WHERE game_id = \?`).
		WithArgs("game-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "payload_hash"}).AddRow(1, "other-hash"))
	// findByGameID 命中后回读既有记录（GetByID）。
	mock.ExpectQuery(`SELECT id, match_type, source, map_name, server_id, game_id, winner_team_id, survival_rate, duration_seconds, started_at, finished_at, created_at`).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "match_type", "source", "map_name", "server_id", "game_id", "winner_team_id", "survival_rate", "duration_seconds", "started_at", "finished_at", "created_at"}).
			AddRow(1, "1v1", "game", nil, nil, "game-1", "A", 1.0, 0,
				"2026-11-06 10:00:00", "2026-11-06 10:10:00", "2026-11-06 10:11:00"))
	mock.ExpectQuery(`SELECT user_uid, team_id, is_winner, is_draw, rank_score_before, rank_score_delta, survival_time_seconds, kills, deaths`).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"user_uid", "team_id", "is_winner", "is_draw", "rank_score_before", "rank_score_delta", "survival_time_seconds", "kills", "deaths"}))

	_, replayed, err := svc.Report(t.Context(), 1, in)
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	if replayed {
		t.Error("conflicting report must not be a replay")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAGameIDConflict {
		t.Fatalf("err = %v, want mca_game_id_conflict", err)
	}
	if ae.HTTPStatus() != http.StatusConflict {
		t.Fatalf("status = %d, want 409", ae.HTTPStatus())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (no settlement must happen): %v", err)
	}
}

// TestReportPartySharing 覆盖队伍（party）共享结算：关联匹配成局后，
// 同 party 的两名胜方成员（perf 1.05/0.95 → 原始 delta 341/309）
// 共享平均值 325。
func TestReportPartySharing(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchService(db, newReportFakeHRPAuth(t), nil)

	in := service.TeamReportInput{
		MatchType: "2v2",
		Teams: []service.TeamInfo{
			{TeamID: "A", Result: service.ResultWin, InitialCount: 2, AliveCount: 2,
				Members: []service.ParticipantReport{
					{UUID: "uuid-1", PerfTweak: 1.05},
					{UUID: "uuid-2", PerfTweak: 0.95},
				}},
			{TeamID: "B", Result: service.ResultLose, InitialCount: 1, AliveCount: 1,
				Members: []service.ParticipantReport{{UUID: "uuid-3"}}},
		},
		MatchmakingGroupID: 99,
		StartedAt:          "2026-11-06T10:00:00Z",
		FinishedAt:         "2026-11-06T10:10:00Z",
	}

	mock.ExpectBegin()
	for _, uid := range []int64{7, 8, 9} {
		mock.ExpectQuery(`SELECT rank_score, wins_count FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(uid).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score", "wins_count"}).AddRow(0, 0))
	}
	mock.ExpectExec(`INSERT INTO matches`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), "A", 1.0, 0, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	for _, uid := range []int64{7, 8, 9} {
		mock.ExpectQuery(`SELECT mp\.is_winner, mp\.is_draw`).
			WithArgs(uid).
			WillReturnRows(sqlmock.NewRows([]string{"is_winner", "is_draw"}))
	}
	// party 共享：成局存在，成员 7/8 同属 party 5。
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matchmaking_groups WHERE id = \?`).
		WithArgs(int64(99)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT user_uid, party_id FROM matchmaking_group_members WHERE group_id = \? AND party_id IS NOT NULL`).
		WithArgs(int64(99)).
		WillReturnRows(sqlmock.NewRows([]string{"user_uid", "party_id"}).
			AddRow(7, 5).AddRow(8, 5))
	// 共享后：7 与 8 均为 325；败方 9 为 -325。
	expectPlayerWrites(mock, 7, 325)
	expectPlayerWrites(mock, 8, 325)
	expectPlayerWrites(mock, 9, -325)
	mock.ExpectCommit()

	m, _, err := svc.Report(t.Context(), 1, in)
	if err != nil {
		t.Fatalf("party sharing settlement failed: %v", err)
	}
	deltaOf := map[int64]int{}
	for _, p := range m.Participants {
		deltaOf[p.UID] = p.RankScoreDelta
	}
	if deltaOf[7] != 325 || deltaOf[8] != 325 {
		t.Errorf("party members delta = %d/%d, want shared 325/325 (raw 341/309)", deltaOf[7], deltaOf[8])
	}
	if deltaOf[9] != -325 {
		t.Errorf("loser delta = %d, want -325", deltaOf[9])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
