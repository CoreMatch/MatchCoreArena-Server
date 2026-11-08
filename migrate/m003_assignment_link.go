package migrate

// v3：为"成局分配"链路补齐 matches 与匹配成局之间的关联：
//
//   - matches.matchmaking_group_id：上报携带 matchmaking_group_id 时落库，
//     使已成局（matchmaking_groups）与已结算对局可互查。代理路由端点
//     （GET /api/matchmaking/assignment/{uuid}，契约 1.9.0）据此判断
//     成局是否已被结算消费；此前该字段只存在于上报入参（TeamReportInput），
//     服务端不留存。
//
// 纯增量：可空新列 + 新索引，不改动任何既有列与数据；未携带
// matchmaking_group_id 的上报（如游戏服侧无法感知成局 id）写 NULL，
// 既有行为完全不变。

import (
	"context"
	"database/sql"
)

func applyV3(ctx context.Context, db *sql.DB) error {
	if err := ensureColumn(ctx, db, "matches", "matchmaking_group_id",
		"BIGINT NULL COMMENT '关联的匹配成局 id（上报携带时落库，用于成局是否已结算的判定）' AFTER payload_hash"); err != nil {
		return err
	}
	return ensureIndex(ctx, db, "matches", "idx_mm_group", false, "matchmaking_group_id")
}
