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

// testMatchmakingConfig 单模式 1v1 的匹配配置（窗口 200 起步、
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
		},
	}
}

// TestMatchmakingJoinLeaveStatus 覆盖入队（懒建行取评分快照）、
// 重复入队 409、离队、不在队 404 与状态查询。
func TestMatchmakingJoinLeaveStatus(t *testing.T) {
	t.Run("join with existing users row", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1500))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(7), "1v1", 1500).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT enqueued_at FROM matchmaking_queue WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"enqueued_at"}).AddRow("2026-11-06 10:00:00"))

		entry, err := svc.Join(t.Context(), 7, "1v1")
		if err != nil {
			t.Fatalf("Join: %v", err)
		}
		if entry.RankScore != 1500 || entry.MatchType != "1v1" {
			t.Errorf("unexpected entry: %+v", entry)
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
			WithArgs(int64(9), "1v1", 0).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(`SELECT enqueued_at FROM matchmaking_queue WHERE user_uid = \?`).
			WithArgs(int64(9)).
			WillReturnRows(sqlmock.NewRows([]string{"enqueued_at"}).AddRow("2026-11-06 10:00:00"))

		if _, err := svc.Join(t.Context(), 9, "1v1"); err != nil {
			t.Fatalf("Join: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("duplicate join answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig())

		mock.ExpectQuery(`SELECT rank_score FROM users WHERE uid = \? AND deleted_at IS NULL`).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"rank_score"}).AddRow(1500))
		mock.ExpectExec(`INSERT INTO matchmaking_queue`).
			WithArgs(int64(7), "1v1", 1500).
			WillReturnError(&gomysql.MySQLError{Number: 1062, Message: "Duplicate entry"})

		_, err = svc.Join(t.Context(), 7, "1v1")
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

		_, err = svc.Join(t.Context(), 7, "4v4")
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

		_, err = svc.Join(t.Context(), 7, "1v1")
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

		mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE user_uid = \?`).
			WithArgs(int64(7)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err = svc.Leave(t.Context(), 7)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAQueueNotJoined {
			t.Fatalf("err = %v, want mca_queue_not_joined", err)
		}
		if ae.HTTPStatus() != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", ae.HTTPStatus())
		}
	})
}

// queueColumns 引擎队列查询的结果列（等待时长由数据库侧计算）。
var queueColumns = []string{"id", "user_uid", "rank_score", "waited_seconds"}

// TestMatchmakingEngineTick 覆盖一轮完整匹配：移除超时条目 → 查询队列 →
// 事务成局（写 groups/members、移出队列）→ 复查队列无剩余。
func TestMatchmakingEngineTick(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := service.NewMatchmakingEngine(db, testMatchmakingConfig())

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\) >= \?`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, TIMESTAMPDIFF`).
		WithArgs("1v1").
		WillReturnRows(sqlmock.NewRows(queueColumns).
			AddRow(11, 7, 1500, 30).
			AddRow(12, 8, 1600, 30))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO matchmaking_groups`).
		WithArgs("1v1").
		WillReturnResult(sqlmock.NewResult(99, 1))
	// 蛇形分队：1600→A，1500→B。
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(8), "A", 1600).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(7), "B", 1500).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE id IN \(\?,\?\)`).
		WithArgs(int64(11), int64(12)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	// 成局后复查队列：已空。
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, TIMESTAMPDIFF`).
		WithArgs("1v1").
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

	engine := service.NewMatchmakingEngine(db, testMatchmakingConfig())

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\) >= \?`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, TIMESTAMPDIFF`).
		WithArgs("1v1").
		WillReturnRows(sqlmock.NewRows(queueColumns).
			AddRow(11, 7, 1500, 10))

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

	engine := service.NewMatchmakingEngine(db, testMatchmakingConfig())

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\) >= \?`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, TIMESTAMPDIFF`).
		WithArgs("1v1").
		WillReturnRows(sqlmock.NewRows(queueColumns).
			AddRow(11, 7, 1000, 30).
			AddRow(12, 8, 1500, 30))

	if err := engine.Tick(t.Context()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (window too narrow, no group): %v", err)
	}
}


