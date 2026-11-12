package service_test

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	gomysql "github.com/go-sql-driver/mysql"

	"MatchCoreArena-Server/config"
	"MatchCoreArena-Server/internal/service"
)

// roomInventoryConfig 在给定模式配置上叠加房间层：一个子服 sub-1
//（10.0.0.1:25501）带两间房（room-small 容量 2、room-big 容量 4），
// 预占 TTL 900s。
func roomInventoryConfig(base config.MatchmakingConfig) config.MatchmakingConfig {
	base.AssignmentTTLSeconds = 1800
	base.RoomReservationTTLSeconds = 900
	base.Servers = []config.MatchServer{
		{Name: "sub-1", Host: "10.0.0.1", Port: 25501, Rooms: []config.RoomSpec{
			{Name: "room-small", Capacity: 2},
			{Name: "room-big", Capacity: 4},
		}},
	}
	return base
}

// dupKeyError MySQL 1062（唯一键冲突）。
func dupKeyError() error {
	return &gomysql.MySQLError{Number: 1062, Message: "Duplicate entry"}
}

// TestEngineTickReservesRoomOnFormation 房间层成局：事务内按清单顺序
// 预占第一间容量足够的空闲房间。
func TestEngineTickReservesRoomOnFormation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := service.NewMatchmakingEngine(db, roomInventoryConfig(engineConfig1v1()))

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\) >= \?`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM matchmaking_room_bindings WHERE TIMESTAMPDIFF\(SECOND, reserved_at, NOW\(\)\) >= \?`).
		WithArgs(900).
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
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(8), "A", 1600, nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(7), "B", 1500, nil).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE id IN \(\?,\?\)`).
		WithArgs(int64(12), int64(11)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	// 预占：1v1 需 2 人，第一间容量够的房间 room-small。
	mock.ExpectExec(`INSERT INTO matchmaking_room_bindings \(server_name, room_name, group_id\) VALUES \(\?, \?, \?\)`).
		WithArgs("sub-1", "room-small", int64(99)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
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

// TestEngineTickRoomTakenTriesNext 首选房间被占（1062）→ 顺序尝试
// 下一间容量足够的房间。
func TestEngineTickRoomTakenTriesNext(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := service.NewMatchmakingEngine(db, roomInventoryConfig(engineConfig1v1()))

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM matchmaking_room_bindings WHERE TIMESTAMPDIFF`).
		WithArgs(900).
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
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(8), "A", 1600, nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(7), "B", 1500, nil).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE id IN \(\?,\?\)`).
		WithArgs(int64(12), int64(11)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	// room-small 已被占 → 落到 room-big。
	mock.ExpectExec(`INSERT INTO matchmaking_room_bindings \(server_name, room_name, group_id\) VALUES \(\?, \?, \?\)`).
		WithArgs("sub-1", "room-small", int64(99)).
		WillReturnError(dupKeyError())
	mock.ExpectExec(`INSERT INTO matchmaking_room_bindings \(server_name, room_name, group_id\) VALUES \(\?, \?, \?\)`).
		WithArgs("sub-1", "room-big", int64(99)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
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

// TestEngineTickNoRoomRollsBackFormation 无可用房间：整局回滚，
// 队列行不被消费（玩家留待下轮），Tick 本身不报错。
func TestEngineTickNoRoomRollsBackFormation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// 2v2 需 4 人：只有 room-big 容量够，且已被占。
	engine := service.NewMatchmakingEngine(db, roomInventoryConfig(engineConfig2v2()))

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM matchmaking_room_bindings WHERE TIMESTAMPDIFF`).
		WithArgs(900).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, user_uid, rank_score, COALESCE`).
		WithArgs("2v2").
		WillReturnRows(sqlmock.NewRows(queueColumns).
			AddRow(21, 11, 1000, 0, 40).
			AddRow(22, 12, 1100, 0, 40).
			AddRow(23, 13, 1050, 0, 40).
			AddRow(24, 14, 1050, 0, 40))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO matchmaking_groups`).
		WithArgs("2v2").
		WillReturnResult(sqlmock.NewResult(99, 1))
	// 装箱顺序：等待相同取评分更高者 → A 队 12(1100)+13(1050)，
	// B 队 14(1050)+11(1000)。
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(12), "A", 1100, nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(13), "A", 1050, nil).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(14), "B", 1050, nil).
		WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectExec(`INSERT INTO matchmaking_group_members`).
		WithArgs(int64(99), int64(11), "B", 1000, nil).
		WillReturnResult(sqlmock.NewResult(4, 1))
	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE id IN \(\?,\?,\?,\?\)`).
		WithArgs(int64(22), int64(23), int64(24), int64(21)).
		WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectExec(`INSERT INTO matchmaking_room_bindings \(server_name, room_name, group_id\) VALUES \(\?, \?, \?\)`).
		WithArgs("sub-1", "room-big", int64(99)).
		WillReturnError(dupKeyError())
	mock.ExpectRollback()

	if err := engine.Tick(t.Context()); err != nil {
		t.Fatalf("Tick: %v（无房应静默等待而非报错）", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestEngineTickCleansExpiredReservations Tick 每轮释放超过 TTL 的预占。
func TestEngineTickCleansExpiredReservations(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := service.NewMatchmakingEngine(db, roomInventoryConfig(engineConfig1v1()))

	mock.ExpectExec(`DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF`).
		WithArgs(300).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM matchmaking_room_bindings WHERE TIMESTAMPDIFF`).
		WithArgs(900).
		WillReturnResult(sqlmock.NewResult(0, 2))
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

// assignmentRoomFlow 登记"有效成局 + 房间层"的前三条查询期望
//（最近成局、未结算、未过期），返回后由用例登记绑定查询。
func assignmentRoomFlow(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT g\.id, g\.match_type, g\.formed_at, gm\.team_id`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "match_type", "formed_at", "team_id"}).
			AddRow(5, "2v2", "2026-11-08 10:00:00", "A"))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matches WHERE matchmaking_group_id = \?`).
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM matchmaking_groups`).
		WithArgs(int64(5), 1800).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
}

// TestAssignmentRoomLayer 房间层有效分配：携带房间名与清单内子服地址。
func TestAssignmentRoomLayer(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchmakingService(db, roomInventoryConfig(testMatchmakingConfig()), newReportFakeHRPAuth(t), nil)

	assignmentRoomFlow(mock)
	mock.ExpectQuery(`SELECT server_name, room_name FROM matchmaking_room_bindings`).
		WithArgs(int64(5), 900).
		WillReturnRows(sqlmock.NewRows([]string{"server_name", "room_name"}).AddRow("sub-1", "room-small"))

	asn, err := svc.Assignment(t.Context(), "uuid-1")
	if err != nil {
		t.Fatalf("Assignment: %v", err)
	}
	if !asn.Assigned {
		t.Fatal("assigned = false, want true")
	}
	if asn.Room != "room-small" {
		t.Errorf("room = %q, want room-small", asn.Room)
	}
	if asn.JoinTarget == nil || asn.JoinTarget.Host != "10.0.0.1" || asn.JoinTarget.Port != 25501 {
		t.Errorf("join_target = %+v, want 清单内子服地址 10.0.0.1:25501", asn.JoinTarget)
	}
	if asn.Reason != "" {
		t.Errorf("reason = %q, want 空", asn.Reason)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestAssignmentRoomUnavailable 成局有效但预占缺失/过期 → 如实回
// no_room_available，绝不给出虚构房间。
func TestAssignmentRoomUnavailable(t *testing.T) {
	t.Run("reservation expired or missing", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, roomInventoryConfig(testMatchmakingConfig()), newReportFakeHRPAuth(t), nil)

		assignmentRoomFlow(mock)
		mock.ExpectQuery(`SELECT server_name, room_name FROM matchmaking_room_bindings`).
			WithArgs(int64(5), 900).
			WillReturnRows(sqlmock.NewRows([]string{"server_name", "room_name"}))

		asn, err := svc.Assignment(t.Context(), "uuid-1")
		if err != nil {
			t.Fatalf("Assignment: %v", err)
		}
		if asn.Assigned {
			t.Error("assigned = true, want false（无可用房间）")
		}
		if asn.Reason != service.AssignmentReasonNoRoom {
			t.Errorf("reason = %q, want %q", asn.Reason, service.AssignmentReasonNoRoom)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("bound server removed from inventory", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		cfg := roomInventoryConfig(testMatchmakingConfig())
		cfg.Servers = []config.MatchServer{{Name: "other-sub", Host: "10.0.0.9", Port: 25599,
			Rooms: []config.RoomSpec{{Name: "room-x", Capacity: 2}}}}
		svc := service.NewMatchmakingService(db, cfg, newReportFakeHRPAuth(t), nil)

		assignmentRoomFlow(mock)
		mock.ExpectQuery(`SELECT server_name, room_name FROM matchmaking_room_bindings`).
			WithArgs(int64(5), 900).
			WillReturnRows(sqlmock.NewRows([]string{"server_name", "room_name"}).AddRow("sub-1", "room-small"))

		asn, err := svc.Assignment(t.Context(), "uuid-1")
		if err != nil {
			t.Fatalf("Assignment: %v", err)
		}
		if asn.Assigned {
			t.Error("assigned = true, want false（绑定子服已移出清单）")
		}
		if asn.Reason != service.AssignmentReasonNoRoom {
			t.Errorf("reason = %q, want %q", asn.Reason, service.AssignmentReasonNoRoom)
		}
	})
}

// TestStatusAttachesRoomAndInventoryAddress 状态接口的成局记录携带
// 房间与清单子服地址（活跃预占）。
func TestStatusAttachesRoomAndInventoryAddress(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchmakingService(db, roomInventoryConfig(testMatchmakingConfig()), nil, nil)

	mock.ExpectQuery(`SELECT user_uid, match_type, rank_score, party_id, enqueued_at FROM matchmaking_queue WHERE user_uid = \?`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"user_uid", "match_type", "rank_score", "party_id", "enqueued_at"}))
	mock.ExpectQuery(`SELECT g\.id, g\.match_type, g\.formed_at`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "match_type", "formed_at"}).AddRow(5, "2v2", "2026-11-08 10:00:00"))
	mock.ExpectQuery(`SELECT user_uid, team_id, rank_score, party_id FROM matchmaking_group_members WHERE group_id = \? ORDER BY id`).
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"user_uid", "team_id", "rank_score", "party_id"}).
			AddRow(7, "A", 1500, nil))
	mock.ExpectQuery(`SELECT group_id, server_name, room_name FROM matchmaking_room_bindings`).
		WithArgs(int64(5), 900).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "server_name", "room_name"}).
			AddRow(5, "sub-1", "room-big"))
	mock.ExpectQuery(`SELECT COUNT\(\*\), COUNT\(DISTINCT party_id\), COALESCE\(AVG\(TIMESTAMPDIFF\(SECOND, enqueued_at, NOW\(\)\)\), 0\)`).
		WillReturnRows(sqlmock.NewRows([]string{"players", "parties", "avg_wait"}).AddRow(0, 0, 0))

	st, err := svc.Status(t.Context(), 7)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.RecentGroups) != 1 {
		t.Fatalf("recent groups = %d, want 1", len(st.RecentGroups))
	}
	g := st.RecentGroups[0]
	if g.Room != "room-big" {
		t.Errorf("room = %q, want room-big", g.Room)
	}
	if g.JoinTarget == nil || g.JoinTarget.Host != "10.0.0.1" || g.JoinTarget.Port != 25501 {
		t.Errorf("join_target = %+v, want 清单子服地址 10.0.0.1:25501", g.JoinTarget)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
