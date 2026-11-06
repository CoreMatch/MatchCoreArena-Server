package service_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/reportlog"
	"MatchCoreArena-Server/internal/service"
)

// rejectionsSnapshot 读取进程级拒绝环快照。
func rejectionsSnapshot(t *testing.T) []reportlog.Record {
	t.Helper()
	return reportlog.Default.Snapshot()
}

// matchRowColumns List/详情查询的主表列。
var matchRowColumns = []string{"id", "match_type", "source", "map_name", "server_id", "game_id", "winner_team_id", "survival_rate", "duration_seconds", "started_at", "finished_at", "created_at"}

// participantColumns 参与者明细列。
var participantColumns = []string{"user_uid", "team_id", "is_winner", "is_draw", "rank_score_before", "rank_score_delta", "survival_time_seconds", "kills", "deaths"}

// TestListFilters 覆盖全局查询的过滤条件拼装与参与者明细加载。
func TestListFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchService(db, nil, nil)

	filter := service.MatchFilter{
		Source:       "virtual",
		MapName:      "arena-1",
		GameID:       "g-1",
		UID:          7,
		FinishedFrom: "2026-11-01T00:00:00Z",
		FinishedTo:   "2026-11-30T00:00:00Z",
	}

	mock.ExpectQuery(`SELECT m\.id, m\.match_type, m\.source, m\.map_name, m\.server_id, m\.game_id, m\.winner_team_id, m\.survival_rate, m\.duration_seconds, m\.started_at, m\.finished_at, m\.created_at`).
		WithArgs(
			"virtual", "arena-1", "g-1", int64(7),
			time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC),
			20, 0,
		).
		WillReturnRows(sqlmock.NewRows(matchRowColumns).
			AddRow(1, "1v1", "virtual", "arena-1", "srv-1", "g-1", "A", 1.0, 600,
				"2026-11-06 10:00:00", "2026-11-06 10:10:00", "2026-11-06 10:11:00"))
	mock.ExpectQuery(`SELECT user_uid, team_id, is_winner, is_draw, rank_score_before, rank_score_delta, survival_time_seconds, kills, deaths`).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows(participantColumns).
			AddRow(7, "A", true, false, 0, 325, 600, 3, 0))

	matches, err := svc.List(t.Context(), filter, service.Page{PageNum: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	if matches[0].GameID != "g-1" || len(matches[0].Participants) != 1 {
		t.Errorf("unexpected match: %+v", matches[0])
	}
	if matches[0].Participants[0].RankScoreAfter != 325 {
		t.Errorf("participant after = %d, want 325", matches[0].Participants[0].RankScoreAfter)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestListNoFilter 覆盖无过滤条件的全量查询。
func TestListNoFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchService(db, nil, nil)

	mock.ExpectQuery(`FROM matches m ORDER BY m\.finished_at DESC, m\.id DESC LIMIT \? OFFSET \?`).
		WithArgs(20, 0).
		WillReturnRows(sqlmock.NewRows(matchRowColumns))

	matches, err := svc.List(t.Context(), service.MatchFilter{}, service.Page{PageNum: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if matches == nil || len(matches) != 0 {
		t.Fatalf("matches = %v, want empty non-nil slice", matches)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestListBadTimeFilter 覆盖非法时间过滤参数以类型化错误拒绝。
func TestListBadTimeFilter(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchService(db, nil, nil)

	_, err = svc.List(t.Context(), service.MatchFilter{FinishedFrom: "yesterday"}, service.Page{PageNum: 1, PageSize: 20})
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
		t.Fatalf("err = %v, want mca_invalid_request", err)
	}
}

// TestListByUserSelfSummary 覆盖战绩列表项的本人视角摘要：本人所在
// 队伍、胜负、分数变化（after = max(0, before+delta)）。
func TestListByUserSelfSummary(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchService(db, nil, nil)

	mock.ExpectQuery(`SELECT m\.id, m\.match_type, m\.source`).
		WithArgs(int64(7), 20, 0).
		WillReturnRows(sqlmock.NewRows(append(matchRowColumns, "team_id", "is_winner", "is_draw", "rank_score_before", "rank_score_delta")).
			AddRow(1, "1v1", "game", "", "", "", "A", 1.0, 600,
				"2026-11-06 10:00:00", "2026-11-06 10:10:00", "2026-11-06 10:11:00",
				"A", true, false, 0, 325).
			AddRow(2, "1v1", "game", "", "", "", "B", 1.0, 600,
				"2026-11-06 11:00:00", "2026-11-06 11:10:00", "2026-11-06 11:11:00",
				"B", false, false, 100, -325))

	matches, err := svc.ListByUser(t.Context(), 7, service.Page{PageNum: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(matches))
	}
	win := matches[0].SelfSummary
	if win == nil || win.TeamID != "A" || !win.IsWinner || win.RankScoreDelta != 325 || win.RankScoreAfter != 325 {
		t.Errorf("win self_summary = %+v, want team A winner delta 325 after 325", win)
	}
	lose := matches[1].SelfSummary
	if lose == nil || lose.TeamID != "B" || lose.IsWinner || lose.RankScoreDelta != -325 || lose.RankScoreAfter != 0 {
		t.Errorf("lose self_summary = %+v, want team B loser delta -325 after 0 (下限)", lose)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestGetByGameID 覆盖按 game_id 查询：命中返回详情，未命中 404。
func TestGetByGameID(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchService(db, nil, nil)

		mock.ExpectQuery(`SELECT id, payload_hash FROM matches WHERE game_id = \?`).
			WithArgs("g-1").
			WillReturnRows(sqlmock.NewRows([]string{"id", "payload_hash"}).AddRow(1, "hash"))
		mock.ExpectQuery(`SELECT id, match_type, source, map_name, server_id, game_id, winner_team_id, survival_rate, duration_seconds, started_at, finished_at, created_at`).
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows(matchRowColumns).
				AddRow(1, "1v1", "game", nil, nil, "g-1", "A", 1.0, 0,
					"2026-11-06 10:00:00", "2026-11-06 10:10:00", "2026-11-06 10:11:00"))
		mock.ExpectQuery(`SELECT user_uid, team_id, is_winner, is_draw, rank_score_before, rank_score_delta, survival_time_seconds, kills, deaths`).
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows(participantColumns))

		m, err := svc.GetByGameID(t.Context(), "g-1")
		if err != nil {
			t.Fatalf("GetByGameID: %v", err)
		}
		if m.ID != 1 || m.GameID != "g-1" {
			t.Errorf("unexpected match: %+v", m)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("not found answers 404", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchService(db, nil, nil)

		mock.ExpectQuery(`SELECT id, payload_hash FROM matches WHERE game_id = \?`).
			WithArgs("missing").
			WillReturnRows(sqlmock.NewRows([]string{"id", "payload_hash"}))

		_, err = svc.GetByGameID(t.Context(), "missing")
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAMatchNotFound {
			t.Fatalf("err = %v, want mca_match_not_found", err)
		}
		if ae.HTTPStatus() != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", ae.HTTPStatus())
		}
	})

	t.Run("empty game_id rejected", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchService(db, nil, nil)

		_, err = svc.GetByGameID(t.Context(), "")
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
			t.Fatalf("err = %v, want mca_invalid_request", err)
		}
	})
}

// TestReportRejectionsAreLogged 覆盖两类拒绝进入只读拒绝环（脱敏后）。
func TestReportRejectionsAreLogged(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchService(db, newReportFakeHRPAuth(t), nil)

	before := len(rejectionsSnapshot(t))

	// 矛盾拒绝：两个获胜队伍。
	in := winLoseReport()
	in.Teams[1].Result = service.ResultWin
	if _, _, err := svc.Report(t.Context(), 1, in); err == nil {
		t.Fatal("expected an error")
	}
	// game_id 冲突：同键异载荷。
	conflict := winLoseReport()
	conflict.GameID = "log-test-1"
	db2, mock2, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	svc2 := service.NewMatchService(db2, newReportFakeHRPAuth(t), nil)
	mock2.ExpectQuery(`SELECT id, payload_hash FROM matches WHERE game_id = \?`).
		WithArgs("log-test-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "payload_hash"}).AddRow(1, "other-hash"))
	mock2.ExpectQuery(`SELECT id, match_type, source`).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows(matchRowColumns).
			AddRow(1, "1v1", "game", nil, nil, "log-test-1", "A", 1.0, 0,
				"2026-11-06 10:00:00", "2026-11-06 10:10:00", "2026-11-06 10:11:00"))
	mock2.ExpectQuery(`SELECT user_uid, team_id`).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows(participantColumns))
	if _, _, err := svc2.Report(t.Context(), 1, conflict); err == nil {
		t.Fatal("expected a conflict error")
	}

	snap := rejectionsSnapshot(t)
	if len(snap) != before+2 {
		t.Fatalf("rejections = %d, want %d (+2)", len(snap), before+2)
	}
	last2 := snap[len(snap)-2:]
	if last2[0].Code != string(apperr.CodeMCAReportInconsistent) {
		t.Errorf("first code = %q, want mca_report_inconsistent", last2[0].Code)
	}
	if last2[1].Code != string(apperr.CodeMCAGameIDConflict) || last2[1].GameID != "log-test-1" {
		t.Errorf("second record = %+v, want conflict with game_id log-test-1", last2[1])
	}
}
