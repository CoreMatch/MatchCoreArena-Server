package migrate

// v4：房间层（契约 1.10.0）——成局与房间的逻辑预占绑定表。
//
//   - matchmaking_room_bindings：成局时按静态清单（config
//     matchmaking.servers）为 group 预占一个房间，(server_name,
//     room_name) 主键从结构上保证"同一房间同一时刻至多一个活跃
//     绑定"；释放走两条路——TTL 到期由引擎每轮清理，或结算上报携带
//     同一 group id 时删除。游戏服侧不做物理锁（作者决策 3C：MCA
//     逻辑占位）。
//
// 纯增量新表，不改动任何既有结构；未配置房间清单的部署该表恒为空。

import (
	"context"
	"database/sql"
)

const ddlMatchmakingRoomBindings = `CREATE TABLE IF NOT EXISTS matchmaking_room_bindings (
    server_name VARCHAR(50) NOT NULL COMMENT '清单中的子服名',
    room_name VARCHAR(50) NOT NULL COMMENT '房间标识（属于该子服命名空间）',
    group_id BIGINT NOT NULL COMMENT '预占该房间的成局 id',
    reserved_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (server_name, room_name),
    INDEX idx_group (group_id),
    INDEX idx_reserved (reserved_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

func applyV4(ctx context.Context, db *sql.DB) error {
	return ensureTable(ctx, db, "matchmaking_room_bindings", ddlMatchmakingRoomBindings)
}
