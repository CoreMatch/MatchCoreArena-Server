package migrate

// v2：补齐 2026-11-06 两批功能（统一对局上报 + 组队匹配）直接修改
// baseline 引入、而旧库（2026-11-05 由 71f45ea 二进制初始化，形状为
// eff1784 版 baseline）缺失的结构：
//
//   - matches：source / map_name / server_id / game_id / payload_hash
//     五列与 uk_game_id 唯一索引；
//   - match_participants：is_draw 列；
//   - matchmaking_queue / matchmaking_parties / matchmaking_party_members /
//     matchmaking_groups / matchmaking_group_members 五张表。
//
// 全部为增量变更（可空或带默认值的新列、新索引、新表），不改动任何既有
// 列与数据。应用后，"旧库升级"与"全新库初始化"两条路径终态 schema
// 完全一致。列定义与 baseline 逐字一致（含 AFTER 位置），保证列序也相同。

import (
	"context"
	"database/sql"
)

func applyV2(ctx context.Context, db *sql.DB) error {
	// 1. matches：统一上报字段与 game_id 幂等键
	matchColumns := []struct{ name, def string }{
		{"source", "VARCHAR(20) NOT NULL DEFAULT 'game' COMMENT '来源标记: game=游戏服务器, virtual=运维调试数据（仅标记，不分流结算）' AFTER match_type"},
		{"map_name", "VARCHAR(50) NULL COMMENT '地图标识（可选）' AFTER source"},
		{"server_id", "VARCHAR(50) NULL COMMENT '上报服务器标识（可选）' AFTER map_name"},
		{"game_id", "VARCHAR(64) NULL COMMENT '上报方稳定幂等键（可选）' AFTER server_id"},
		{"payload_hash", "CHAR(64) NULL COMMENT '上报载荷规范化哈希（game_id 幂等冲突检测）' AFTER game_id"},
	}
	for _, col := range matchColumns {
		if err := ensureColumn(ctx, db, "matches", col.name, col.def); err != nil {
			return err
		}
	}
	if err := ensureIndex(ctx, db, "matches", "uk_game_id", true, "game_id"); err != nil {
		return err
	}

	// 2. match_participants：平局标记
	if err := ensureColumn(ctx, db, "match_participants", "is_draw",
		"BOOLEAN NOT NULL DEFAULT FALSE COMMENT '平局（虚拟对局支持；游戏上报恒为 FALSE）' AFTER is_winner"); err != nil {
		return err
	}

	// 3. matchmaking_* 五张表（先建被外键引用的父表）
	tables := []struct{ name, ddl string }{
		{"matchmaking_queue", ddlMatchmakingQueue},
		{"matchmaking_parties", ddlMatchmakingParties},
		{"matchmaking_party_members", ddlMatchmakingPartyMembers},
		{"matchmaking_groups", ddlMatchmakingGroups},
		{"matchmaking_group_members", ddlMatchmakingGroupMembers},
	}
	for _, tbl := range tables {
		if err := ensureTable(ctx, db, tbl.name, tbl.ddl); err != nil {
			return err
		}
	}
	return nil
}

// 以下 DDL 与 migrations/baseline.sql（v1）中的定义逐字一致。

const ddlMatchmakingQueue = `CREATE TABLE IF NOT EXISTS matchmaking_queue (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_uid BIGINT NOT NULL,
    match_type VARCHAR(20) NOT NULL COMMENT '排队模式（与 matches.match_type 同源）',
    rank_score INT NOT NULL COMMENT '入队时评分快照',
    party_id BIGINT NULL COMMENT '组队入队时的队伍 ID（单人排队为 NULL）',
    enqueued_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_user (user_uid),
    INDEX idx_type_score (match_type, rank_score),
    INDEX idx_party (party_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

const ddlMatchmakingParties = `CREATE TABLE IF NOT EXISTS matchmaking_parties (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    leader_uid BIGINT NOT NULL,
    match_type VARCHAR(20) NOT NULL COMMENT '队伍排队的目标模式',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_leader (leader_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

const ddlMatchmakingPartyMembers = `CREATE TABLE IF NOT EXISTS matchmaking_party_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    party_id BIGINT NOT NULL,
    user_uid BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_user (user_uid),
    UNIQUE KEY uk_party_user (party_id, user_uid),
    FOREIGN KEY (party_id) REFERENCES matchmaking_parties(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

const ddlMatchmakingGroups = `CREATE TABLE IF NOT EXISTS matchmaking_groups (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    match_type VARCHAR(20) NOT NULL,
    formed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_formed_at (formed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

const ddlMatchmakingGroupMembers = `CREATE TABLE IF NOT EXISTS matchmaking_group_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    group_id BIGINT NOT NULL,
    user_uid BIGINT NOT NULL,
    team_id VARCHAR(50) NOT NULL COMMENT '成局分配的队伍标识',
    rank_score INT NOT NULL COMMENT '成局时评分',
    party_id BIGINT NULL COMMENT '组队匹配时的队伍 ID（单人为 NULL）',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_group_id (group_id),
    INDEX idx_user_uid (user_uid),
    FOREIGN KEY (group_id) REFERENCES matchmaking_groups(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
