package service_test

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"MatchCoreArena-Server/config"
	"MatchCoreArena-Server/internal/service"
)

// assignmentConfig 带 join_target 与 TTL 的匹配配置（assignment 用）。
func assignmentConfig() config.MatchmakingConfig {
	cfg := testMatchmakingConfig()
	cfg.AssignmentTTLSeconds = 1800
	cfg.JoinTarget = config.JoinTargetConfig{Host: "game.example.com", Port: 25565}
	return cfg
}

// expectLatestGroupQuery 登记"最近成局"查询期望。
func expectLatestGroupQuery(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectQuery(`SELECT g\.id, g\.match_type, g\.formed_at, gm\.team_id`).
		WithArgs(int64(7)).
		WillReturnRows(rows)
}

// TestAssignmentValid 覆盖有效分配：最近成局未结算且未过期 →
// assigned=true，携带最小路由信息与 join_target。
func TestAssignmentValid(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchmakingService(db, assignmentConfig(), newReportFakeHRPAuth(t), nil)

	expectLatestGroupQuery(mock, sqlmock.NewRows([]string{"id", "match_type", "formed_at", "team_id"}).
		AddRow(5, "2v2", "2026-11-08 10:00:00", "A"))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matches WHERE matchmaking_group_id = \?`).
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matchmaking_groups`).
		WithArgs(int64(5), 1800).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	asn, err := svc.Assignment(t.Context(), "uuid-1")
	if err != nil {
		t.Fatalf("Assignment: %v", err)
	}
	if !asn.Assigned {
		t.Fatal("assigned = false, want true")
	}
	if asn.GroupID != 5 || asn.MatchType != "2v2" || asn.TeamID != "A" || asn.FormedAt != "2026-11-08 10:00:00" {
		t.Errorf("unexpected assignment: %+v", asn)
	}
	if asn.JoinTarget == nil || asn.JoinTarget.Host != "game.example.com" || asn.JoinTarget.Port != 25565 {
		t.Errorf("join_target = %+v, want game.example.com:25565", asn.JoinTarget)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestAssignmentOmitsJoinTargetWhenUnconfigured join_target 未配置时省略。
func TestAssignmentOmitsJoinTargetWhenUnconfigured(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := assignmentConfig()
	cfg.JoinTarget = config.JoinTargetConfig{} // host 空 = 未配置
	svc := service.NewMatchmakingService(db, cfg, newReportFakeHRPAuth(t), nil)

	expectLatestGroupQuery(mock, sqlmock.NewRows([]string{"id", "match_type", "formed_at", "team_id"}).
		AddRow(5, "2v2", "2026-11-08 10:00:00", "A"))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matches WHERE matchmaking_group_id = \?`).
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matchmaking_groups`).
		WithArgs(int64(5), 1800).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	asn, err := svc.Assignment(t.Context(), "uuid-1")
	if err != nil {
		t.Fatalf("Assignment: %v", err)
	}
	if !asn.Assigned {
		t.Fatal("assigned = false, want true")
	}
	if asn.JoinTarget != nil {
		t.Errorf("join_target = %+v, want 省略（未配置）", asn.JoinTarget)
	}
}

// TestAssignmentNone 覆盖各类"无有效分配"路径：无成局、已结算、已过期。
func TestAssignmentNone(t *testing.T) {
	t.Run("no formed group", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, assignmentConfig(), newReportFakeHRPAuth(t), nil)

		expectLatestGroupQuery(mock, sqlmock.NewRows([]string{"id", "match_type", "formed_at", "team_id"}))

		asn, err := svc.Assignment(t.Context(), "uuid-1")
		if err != nil {
			t.Fatalf("Assignment: %v", err)
		}
		if asn.Assigned {
			t.Errorf("assigned = true, want false（无成局）")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("group already settled", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, assignmentConfig(), newReportFakeHRPAuth(t), nil)

		expectLatestGroupQuery(mock, sqlmock.NewRows([]string{"id", "match_type", "formed_at", "team_id"}).
			AddRow(5, "2v2", "2026-11-08 10:00:00", "A"))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matches WHERE matchmaking_group_id = \?`).
			WithArgs(int64(5)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		asn, err := svc.Assignment(t.Context(), "uuid-1")
		if err != nil {
			t.Fatalf("Assignment: %v", err)
		}
		if asn.Assigned {
			t.Errorf("assigned = true, want false（已结算消费）")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("group expired", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, assignmentConfig(), newReportFakeHRPAuth(t), nil)

		expectLatestGroupQuery(mock, sqlmock.NewRows([]string{"id", "match_type", "formed_at", "team_id"}).
			AddRow(5, "2v2", "2026-11-08 10:00:00", "A"))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matches WHERE matchmaking_group_id = \?`).
			WithArgs(int64(5)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matchmaking_groups`).
			WithArgs(int64(5), 1800).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		asn, err := svc.Assignment(t.Context(), "uuid-1")
		if err != nil {
			t.Fatalf("Assignment: %v", err)
		}
		if asn.Assigned {
			t.Errorf("assigned = true, want false（已过 TTL）")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

// TestAssignmentUnknownUUID 未知 UUID 与"无分配"路由等价（契约 1.9.1）：
// 回 assigned=false，不报 404，且不触达数据库。
func TestAssignmentUnknownUUID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchmakingService(db, assignmentConfig(), newReportFakeHRPAuth(t), nil)

	asn, err := svc.Assignment(t.Context(), "uuid-unknown")
	if err != nil {
		t.Fatalf("Assignment: %v（未知 UUID 应为无分配而非错误）", err)
	}
	if asn.Assigned {
		t.Error("assigned = true, want false（未知 UUID）")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
