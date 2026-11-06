package service_test

import (
	"database/sql/driver"
	"errors"
	"net/http"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	gomysql "github.com/go-sql-driver/mysql"

	"MatchCoreArena-Server/config"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/service"
)

// testMatchmakingConfig 1v1/2v2 双模式匹配配置（窗口 200 起步、
// 每分钟 +100、封顶 1000，超时 300s）。
func testMatchmakingConfig() config.MatchmakingConfig {
	return config.MatchmakingConfig{
		Enabled:                     true,
		TickSeconds:                 1,
		QueueTimeoutSeconds:         300,
		BaseRatingWindow:            200,
		RatingWindowGrowthPerMinute: 100,
		MaxRatingWindow:             1000,
		Modes: []config.MatchMode{
			{MatchType: "1v1", TeamSizes: []int{1, 1}},
			{MatchType: "2v2", TeamSizes: []int{2, 2}},
		},
	}
}

// TestMatchmakingJoinLeaveStatus 覆盖入队（懒建行取评分快照）、
// 重复入队 409、离队（单人/整队）、不在队 404 与状态查询。
func TestMatchmakingJoinLeaveStatus(t *testing.T) {
	t.Run("join with existing users row", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1500))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(7), "1v1", 1500, nil).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT enqueued_at FROM matchmaking_queue WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"enqueued_at"}).AddRow("2026-11-06 10:00:00"))

		entries, err := svc.Join(t.Context(), 7, "1v1", 0)
		if err != nil {
			t.Fatalf("Join: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("entries = %d, want 1", len(entries))
		}
		if entries[0].RankScore != 1500 || entries[0].MatchType != "1v1" {
			t.Errorf("unexpected entry: %+v", entries[0])
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("join lazily provisions missing users row", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(9)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(9)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}))
		mock.ExpectExec(`INSERT IGNORE INTO users \(uid\) VALUES \(\?\)`).
			WithArgs(int64(9)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(9)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(0))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(9), "1v1", 0, nil).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT enqueued_at FROM matchmaking_queue WHERE user_uid = \?`).
			WithArgs(int64(9)).
			WillReturnRows(sqlmock.NewRows([]string{"enqueued_at"}).AddRow("2026-11-06 10:00:00"))

		if _, err := svc.Join(t.Context(), 9, "1v1", 0); err != nil {
			t.Fatalf("Join: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("solo join while in party rejected", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}).AddRow(5))

		_, err = svc.Join(t.Context(), 7, "1v1", 0)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAPartyAlreadyInParty {
			t.Fatalf("err = %v, want mca_party_already_in_party", err)
		}
	})

	t.Run("duplicate join answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1500))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(7), "1v1", 1500, nil).
			WillReturnError(&gomysql.MySQLError{Number: 1062, Message: "Duplicate entry"})

		_, err = svc.Join(t.Context(), 7, "1v1", 0)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAQueueAlreadyJoined {
			t.Fatalf("err = %v, want mca_queue_already_joined", err)
		}
		if ae.HTTPStatus() != http.StatusConflict {
			t.Fatalf("status = %d, want 409", ae.HTTPStatus())
		}
	})

	t.Run("unknown mode rejected", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		_, err = svc.Join(t.Context(), 7, "4v4", 0)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
			t.Fatalf("err = %v, want mca_invalid_request", err)
		}
	})

	t.Run("disabled matchmaking rejected", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		cfg := testMatchmakingConfig()
		cfg.Enabled = false
		svc := service.NewMatchmakingService(db, cfg)

		_, err = svc.Join(t.Context(), 7, "1v1", 0)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
			t.Fatalf("err = %v, want mca_invalid_request", err)
		}
	})

	t.Run("leave without queue entry answers 404", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_queue WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))

		err = svc.Leave(t.Context(), 7)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAQueueNotJoined {
			t.Fatalf("err = %v, want mca_queue_not_joined", err)
		}
		if ae.HTTPStatus() != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", ae.HTTPStatus())
		}
	})

	t.Run("leave removes whole party queue", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_queue WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}).AddRow(5))
		mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE party_id = \?`).
			WithArgs(int64(5)).
			WillReturnResult(sqlmock.NewResult(0, 2))

		if err := svc.Leave(t.Context(), 7); err != nil {
			t.Fatalf("Leave: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

// partyRows 队伍查询期望：parties 主行 + 成员评分快照。
func partyRows(mock sqlmock.Sqlmock, partyID, leaderUID int64, matchType string, members ...[]driver.Value) {
	mock.ExpectQuery(`SELECT id, leader_uid, match_type, created_at FROM matchmaking_parties WHERE id = \?`).
		WithArgs(partyID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "leader_uid", "match_type", "created_at"}).
			AddRow(partyID, leaderUID, matchType, "2026-11-06 10:00:00"))
	rows := sqlmock.NewRows([]string{"user_uid", "rank_score", "joined_at"})
	for _, m := range members {
		rows.AddRow(m...)
	}
	mock.ExpectQuery(`SELECT pm\.user_uid, COALESCE\(u\.rank_score, 0\), pm\.created_at`).
		WithArgs(partyID).
		WillReturnRows(rows)
}

// TestPartyLifecycle 覆盖队伍创建/加入/离开/解散与类型化错误。
func TestPartyLifecycle(t *testing.T) {
	t.Run("create party", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectExec(`INSERT INTO matchmaking_parties \(leader_uid, match_type\) VALUES \(\?, \?\)`).
			WithArgs(int64(7), "2v2").
			WillReturnResult(sqlmock.NewResult(5, 1))
		mock.ExpectExec(`INSERT INTO matchmaking_party_members \(party_id, user_uid\) VALUES \(\?, \?\)`).
			WithArgs(int64(5), int64(7)).
			WillReturnResult(sqlmock.NewResult(1, 1))
		partyRows(mock, 5, 7, "2v2", []driver.Value{int64(7), 1500, "2026-11-06 10:00:00"})

		party, err := svc.CreateParty(t.Context(), 7, "2v2")
		if err != nil {
			t.Fatalf("CreateParty: %v", err)
		}
		if party.ID != 5 || party.LeaderUID != 7 || len(party.Members) != 1 {
			t.Errorf("unexpected party: %+v", party)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("create party while already in one answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}).AddRow(3))

		_, err = svc.CreateParty(t.Context(), 7, "2v2")
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAPartyAlreadyInParty {
			t.Fatalf("err = %v, want mca_party_already_in_party", err)
		}
	})

	t.Run("join party", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		partyRows(mock, 5, 7, "2v2", []driver.Value{int64(7), 1500, "2026-11-06 10:00:00"})
		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(8)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectExec(`INSERT INTO matchmaking_party_members \(party_id, user_uid\) VALUES \(\?, \?\)`).
			WithArgs(int64(5), int64(8)).
			WillReturnResult(sqlmock.NewResult(2, 1))
		partyRows(mock, 5, 7, "2v2",
			[]driver.Value{int64(7), 1500, "2026-11-06 10:00:00"},
			[]driver.Value{int64(8), 1600, "2026-11-06 10:01:00"})

		party, err := svc.JoinParty(t.Context(), 8, 5)
		if err != nil {
			t.Fatalf("JoinParty: %v", err)
		}
		if len(party.Members) != 2 {
			t.Errorf("members = %d, want 2", len(party.Members))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("join party full answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		// 2v2 单队上限 2 人：已有 2 名成员时第三人加入被拒。
		partyRows(mock, 5, 7, "2v2",
			[]driver.Value{int64(7), 1500, "2026-11-06 10:00:00"},
			[]driver.Value{int64(8), 1600, "2026-11-06 10:01:00"})
		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(9)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))

		_, err = svc.JoinParty(t.Context(), 9, 5)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAPartyFull {
			t.Fatalf("err = %v, want mca_party_full", err)
		}
	})

	t.Run("leader leave disbands party", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		partyRows(mock, 5, 7, "2v2",
			[]driver.Value{int64(7), 1500, "2026-11-06 10:00:00"},
			[]driver.Value{int64(8), 1600, "2026-11-06 10:01:00"})
		mock.ExpectExec(`DELETE FROM matchmaking_parties WHERE id = \?`).
			WithArgs(int64(5)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		if err := svc.LeaveParty(t.Context(), 7, 5); err != nil {
			t.Fatalf("LeaveParty: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("leave party as non-member answers 404", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		partyRows(mock, 5, 7, "2v2", []driver.Value{int64(7), 1500, "2026-11-06 10:00:00"})

		err = svc.LeaveParty(t.Context(), 9, 5)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAPartyNotFound {
			t.Fatalf("err = %v, want mca_party_not_found", err)
		}
	})

	t.Run("get missing party answers 404", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT id, leader_uid, match_type, created_at FROM matchmaking_parties WHERE id = \?`).
			WithArgs(int64(99)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "leader_uid", "match_type", "created_at"}))

		_, err = svc.GetParty(t.Context(), 99)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAPartyNotFound {
			t.Fatalf("err = %v, want mca_party_not_found", err)
		}
		if ae.HTTPStatus() != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", ae.HTTPStatus())
		}
	})
}

// TestPartyQueueJoin 覆盖以队伍为单位的整体入队。
func TestPartyQueueJoin(t *testing.T) {
	t.Run("leader enqueues whole party", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		partyRows(mock, 5, 7, "2v2",
			[]driver.Value{int64(7), 1500, "2026-11-06 10:00:00"},
			[]driver.Value{int64(8), 1600, "2026-11-06 10:01:00"})
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1500))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(7), "2v2", 1500, int64(5)).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(8)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1600))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(8), "2v2", 1600, int64(5)).
			WillReturnResult(sqlmock.NewResult(2, 1))
		mock.ExpectCommit()

		entries, err := svc.Join(t.Context(), 7, "2v2", 5)
		if err != nil {
			t.Fatalf("Join: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("entries = %d, want 2 (whole party)", len(entries))
		}
		for _, e := range entries {
			if e.PartyID != 5 {
				t.Errorf("entry party_id = %d, want 5", e.PartyID)
			}
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("non-leader cannot enqueue party", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		partyRows(mock, 5, 7, "2v2",
			[]driver.Value{int64(7), 1500, "2026-11-06 10:00:00"},
			[]driver.Value{int64(8), 1600, "2026-11-06 10:01:00"})

		_, err = svc.Join(t.Context(), 8, "2v2", 5)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCANotPartyLeader {
			t.Fatalf("err = %v, want mca_not_party_leader", err)
		}
		if ae.HTTPStatus() != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", ae.HTTPStatus())
		}
	})

	t.Run("party mode mismatch rejected", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		partyRows(mock, 5, 7, "2v2", []driver.Value{int64(7), 1500, "2026-11-06 10:00:00"})

		_, err = svc.Join(t.Context(), 7, "1v1", 5)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
			t.Fatalf("err = %v, want mca_invalid_request", err)
		}
	})
}

// engineConfig1v1 / engineConfig2v2 引擎测试用单模式配置
//（Tick 遍历全部模式，单模式便于登记 SQL 期望）。
func engineConfig1v1() config.MatchmakingConfig {
	cfg := testMatchmakingConfig()
	cfg.Modes = []config.MatchMode{{MatchType: "1v1", TeamSizes: []int{1, 1}}}
	return cfg
}

func engineConfig2v2() config.MatchmakingConfig {
	cfg := testMatchmakingConfig()
	cfg.Modes = []config.MatchMode{{MatchType: "2v2", TeamSizes: []int{2, 2}}}
	return cfg
}

// queueColumns 引擎队列查询的结果列（party_id 由 COALESCE 归零、
// 等待时长由数据库侧计算）。
var queueColumns = []string{"id", "user_uid", "rank_score", "party_id", "waited_seconds"}

// TestMatchmakingEngineTick 覆盖一轮完整匹配：移除超时条目 → 查询队列 →
// 事务成局（写 groups/members、移出队列）→ 复查队列无剩余。
func TestMatchmakingEngineTick(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := service.NewMatchmakingEngine(db, engineConfig1v1())

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\) >= \?`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, COALESCE`).
		WithArgs("1v1").
		WillReturnRows(sqlmock.NewRows(queueColumns).
			AddRow(11, 7, 1500, 0, 30).
			AddRow(12, 8, 1600, 0, 30))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO matchmaking_groups`).
		WithArgs("1v1").
		WillReturnResult(sqlmock.NewResult(99, 1))
	// 贪心装箱：等待相同取评分更高者先装 → 1600→A，1500→B。
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(8), "A", 1600, nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(7), "B", 1500, nil).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE id IN \(\?,\?\)`).
		WithArgs(int64(12), int64(11)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	// 成局后复查队列：已空。
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, COALESCE`).
		WithArgs("1v1").
		WillReturnRows(sqlmock.NewRows(queueColumns))

	if err := engine.Tick(t.Context()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestMatchmakingEngineTickParty 覆盖组队成局：2v2 模式中一队两人
//（party 5）与两名单人凑成一局；队伍整队装入同一 team，成局后
// 清理已成交的 party。
func TestMatchmakingEngineTickParty(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := service.NewMatchmakingEngine(db, engineConfig2v2())

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\) >= \?`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, COALESCE`).
		WithArgs("2v2").
		WillReturnRows(sqlmock.NewRows(queueColumns).
			AddRow(21, 11, 1000, 5, 40).
			AddRow(22, 12, 1100, 5, 40).
			AddRow(23, 13, 1050, 0, 40).
			AddRow(24, 14, 1050, 0, 40))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO matchmaking_groups`).
		WithArgs("2v2").
		WillReturnResult(sqlmock.NewResult(99, 1))
	// party 5 整队装入 team A；两名单人装入 team B。
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(11), "A", 1000, int64(5)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(12), "A", 1100, int64(5)).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(13), "B", 1050, nil).
		WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(14), "B", 1050, nil).
		WillReturnResult(sqlmock.NewResult(4, 1))
	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE id IN \(\?,\?,\?,\?\)`).
		WithArgs(int64(21), int64(22), int64(23), int64(24)).
		WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectExec(`DELETE FROM matchmaking_parties WHERE id = \?`).
		WithArgs(int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, COALESCE`).
		WithArgs("2v2").
		WillReturnRows(sqlmock.NewRows(queueColumns))

	if err := engine.Tick(t.Context()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestMatchmakingEngineTickNotEnoughPlayers 覆盖人数不足一局时不成局：
// 只有超时移除与一次队列查询，无任何写入。
func TestMatchmakingEngineTickNotEnoughPlayers(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := service.NewMatchmakingEngine(db, engineConfig1v1())

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\) >= \?`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, COALESCE`).
		WithArgs("1v1").
		WillReturnRows(sqlmock.NewRows(queueColumns).
			AddRow(11, 7, 1500, 0, 10))

	if err := engine.Tick(t.Context()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (no group must be formed): %v", err)
	}
}

// TestMatchmakingEngineTickWindowTooNarrow 覆盖分差超出窗口时不成局：
// 两人分差 500 > 等待 30s 对应的窗口 250（200 + 100*0.5）。
func TestMatchmakingEngineTickWindowTooNarrow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := service.NewMatchmakingEngine(db, engineConfig1v1())

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\) >= \?`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, COALESCE`).
		WithArgs("1v1").
		WillReturnRows(sqlmock.NewRows(queueColumns).
			AddRow(11, 7, 1000, 0, 30).
			AddRow(12, 8, 1500, 0, 30))

	if err := engine.Tick(t.Context()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (window too narrow, no group): %v", err)
	}
}
