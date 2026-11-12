package service_test

import (
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
// 重复入队 409、离队（单人/整队并清理队伍）、不在队 404 与状态查询。
func TestMatchmakingJoinLeaveStatus(t *testing.T) {
	t.Run("join with existing users row", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

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

		entries, err := svc.Join(t.Context(), 7, "1v1", nil)
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
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

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

		if _, err := svc.Join(t.Context(), 9, "1v1", nil); err != nil {
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
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}).AddRow(5))

		_, err = svc.Join(t.Context(), 7, "1v1", nil)
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
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1500))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(7), "1v1", 1500, nil).
			WillReturnError(&gomysql.MySQLError{Number: 1062, Message: "Duplicate entry"})

		_, err = svc.Join(t.Context(), 7, "1v1", nil)
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
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		_, err = svc.Join(t.Context(), 7, "4v4", nil)
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
		svc := service.NewMatchmakingService(db, cfg, nil, nil)

		_, err = svc.Join(t.Context(), 7, "1v1", nil)
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
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

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

	t.Run("leave removes whole party queue and cleans up party", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		mock.ExpectQuery(`SELECT party_id FROM matchmaking_queue WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}).AddRow(5))
		mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE party_id = \?`).
			WithArgs(int64(5)).
			WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectExec(`DELETE FROM matchmaking_parties WHERE id = \?`).
			WithArgs(int64(5)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		if err := svc.Leave(t.Context(), 7); err != nil {
			t.Fatalf("Leave: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

// TestPartyJoin 覆盖 party_members 整队入队：UUID 解析、临时队伍建立、
// 全队写队列，以及各类类型化拒绝。
func TestPartyJoin(t *testing.T) {
	// partyJoinFlow 登记整队入队的完整 SQL 期望（2v2，调用者 7 携成员 8，
	// 成员 8 已接受邀请 42；入队事务内消费该同意）。
	partyJoinFlow := func(mock sqlmock.Sqlmock) {
		mock.ExpectQuery(`SELECT id FROM matchmaking_party_invites`).
			WithArgs(int64(7), int64(8), "2v2", service.InviteStatusAccepted).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(8)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectBegin()
		mock.ExpectExec(`INSERT INTO matchmaking_parties \(leader_uid, match_type\) VALUES \(\?, \?\)`).
			WithArgs(int64(7), "2v2").
			WillReturnResult(sqlmock.NewResult(5, 1))
		mock.ExpectExec(`INSERT INTO matchmaking_party_members \(party_id, user_uid\) VALUES \(\?, \?\)`).
			WithArgs(int64(5), int64(7)).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1500))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(7), "2v2", 1500, int64(5)).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(`INSERT INTO matchmaking_party_members \(party_id, user_uid\) VALUES \(\?, \?\)`).
			WithArgs(int64(5), int64(8)).
			WillReturnResult(sqlmock.NewResult(2, 1))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(8)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1600))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(8), "2v2", 1600, int64(5)).
			WillReturnResult(sqlmock.NewResult(2, 1))
		mock.ExpectExec(`UPDATE matchmaking_party_invites SET consumed_at`).
			WithArgs(int64(42)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}

	t.Run("party members enqueue as a unit", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		partyJoinFlow(mock)

		entries, err := svc.Join(t.Context(), 7, "2v2", []string{"uuid-1", "uuid-2"})
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
		if entries[0].UID != 7 || entries[1].UID != 8 {
			t.Errorf("entry uids = %d/%d, want 7/8 (roster order)", entries[0].UID, entries[1].UID)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("unknown member uuid answers 404", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		_, err = svc.Join(t.Context(), 1, "2v2", []string{"uuid-1", "uuid-unknown"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAUserNotFound {
			t.Fatalf("err = %v, want mca_user_not_found", err)
		}
		if ae.HTTPStatus() != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", ae.HTTPStatus())
		}
	})

	t.Run("single member rejected", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		_, err = svc.Join(t.Context(), 1, "2v2", []string{"uuid-1"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
			t.Fatalf("err = %v, want mca_invalid_request", err)
		}
	})

	t.Run("oversized party rejected", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		_, err = svc.Join(t.Context(), 1, "2v2", []string{"uuid-1", "uuid-2", "uuid-3"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAPartyFull {
			t.Fatalf("err = %v, want mca_party_full", err)
		}
	})

	t.Run("duplicate uuid rejected", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		_, err = svc.Join(t.Context(), 1, "2v2", []string{"uuid-1", "uuid-1"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
			t.Fatalf("err = %v, want mca_invalid_request", err)
		}
	})

	// 同意机制（契约 1.11.0）：调用者不在名单内 → 400；成员未接受
	// 邀请 → 409（默认拒绝）。
	t.Run("caller not in roster rejected", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		// 调用者 uid=1 不在解析出的 {7,8} 内。
		_, err = svc.Join(t.Context(), 1, "2v2", []string{"uuid-1", "uuid-2"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCACallerNotInRoster {
			t.Fatalf("err = %v, want mca_caller_not_in_roster", err)
		}
		if ae.HTTPStatus() != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", ae.HTTPStatus())
		}
	})

	t.Run("member without accepted invite rejected", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		mock.ExpectQuery(`SELECT id FROM matchmaking_party_invites`).
			WithArgs(int64(7), int64(8), "2v2", service.InviteStatusAccepted).
			WillReturnRows(sqlmock.NewRows([]string{"id"})) // 无已接受邀请

		_, err = svc.Join(t.Context(), 7, "2v2", []string{"uuid-1", "uuid-2"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAConsentMissing {
			t.Fatalf("err = %v, want mca_consent_missing", err)
		}
		if ae.HTTPStatus() != http.StatusConflict {
			t.Fatalf("status = %d, want 409", ae.HTTPStatus())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("member already in another party answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		mock.ExpectQuery(`SELECT id FROM matchmaking_party_invites`).
			WithArgs(int64(7), int64(8), "2v2", service.InviteStatusAccepted).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}).AddRow(3))

		_, err = svc.Join(t.Context(), 7, "2v2", []string{"uuid-1", "uuid-2"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAPartyAlreadyInParty {
			t.Fatalf("err = %v, want mca_party_already_in_party", err)
		}
	})

	t.Run("member already queued answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		mock.ExpectQuery(`SELECT id FROM matchmaking_party_invites`).
			WithArgs(int64(7), int64(8), "2v2", service.InviteStatusAccepted).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectQuery(`SELECT party_id FROM matchmaking_party_members WHERE user_uid = \?`).
			WithArgs(int64(8)).
			WillReturnRows(sqlmock.NewRows([]string{"party_id"}))
		mock.ExpectBegin()
		mock.ExpectExec(`INSERT INTO matchmaking_parties`).
			WithArgs(int64(7), "2v2").
			WillReturnResult(sqlmock.NewResult(5, 1))
		mock.ExpectExec(`INSERT INTO matchmaking_party_members`).
			WithArgs(int64(5), int64(7)).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1500))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(7), "2v2", 1500, int64(5)).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(`INSERT INTO matchmaking_party_members`).
			WithArgs(int64(5), int64(8)).
			WillReturnResult(sqlmock.NewResult(2, 1))
		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(8)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1600))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(8), "2v2", 1600, int64(5)).
			WillReturnError(&gomysql.MySQLError{Number: 1062, Message: "Duplicate entry"})
		mock.ExpectRollback()

		_, err = svc.Join(t.Context(), 7, "2v2", []string{"uuid-1", "uuid-2"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAQueueAlreadyJoined {
			t.Fatalf("err = %v, want mca_queue_already_joined", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

// TestStatusQueueStats 覆盖匹配状态附带的队列规模统计（只读展示，
// 不影响入队/成局）：玩家数、队伍数（按 party 计）、平均等待秒数。
func TestStatusQueueStats(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

	mock.ExpectQuery(`SELECT user_uid, match_type, rank_score, party_id, enqueued_at FROM matchmaking_queue WHERE user_uid = \?`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"user_uid", "match_type", "rank_score", "party_id", "enqueued_at"}))
	mock.ExpectQuery(`SELECT g\.id, g\.match_type, g\.formed_at`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "match_type", "formed_at"}))
	mock.ExpectQuery(`SELECT COUNT\(\*\), COUNT\(DISTINCT party_id\), COALESCE\(AVG\(TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\)\), 0\)`).
		WillReturnRows(sqlmock.NewRows([]string{"players", "parties", "avg_wait"}).AddRow(3, 1, 12.6))

	st, err := svc.Status(t.Context(), 7)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.QueueStats.Players != 3 || st.QueueStats.Parties != 1 {
		t.Errorf("queue stats = %+v, want players 3 parties 1", st.QueueStats)
	}
	if st.QueueStats.AverageWaitSeconds != 13 {
		t.Errorf("average wait = %d, want 13 (12.6 四舍五入)", st.QueueStats.AverageWaitSeconds)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
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
