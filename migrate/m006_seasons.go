package migrate

// v6：赛季手动更迭（契约 1.12.0，knowledge/0007 2026-11-12 决策）：
//
//   - seasons：赛季簿记表。更季只能由运维端点手动触发（无任何按时间
//     的自动更季），每次更季记录赛季号、是否重置评分、操作凭据与
//     备注；当前赛季 = 表中最大赛季号。
//   - 种子数据：赛季 1（存量 rankings 全部为 season=1，与历史一致）。
//
// 纯增量：仅新增表与一行种子数据，不改动任何既有结构与数据。

import (
	"context"
	"database/sql"
)

func applyV6(ctx context.Context, db *sql.DB) error {
	if err := ensureTable(ctx, db, "seasons", ddlSeasons); err != nil {
		return err
	}
	// 种子：赛季 1。INSERT IGNORE 保证重放幂等（uk_season 冲突即跳过）。
	if _, err := db.ExecContext(ctx,
		`INSERT IGNORE INTO seasons (season, opened_by, note)
		 VALUES (1, 'migration-v6', '基线赛季：更季机制引入前的全部历史')`); err != nil {
		return err
	}
	return nil
}

const ddlSeasons = `CREATE TABLE IF NOT EXISTS seasons (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    season INT NOT NULL COMMENT '赛季编号，从 1 起，每次更季严格 +1',
    reset_scores BOOLEAN NOT NULL DEFAULT FALSE COMMENT '本次更季是否重置了全部 users.rank_score（运维显式指定）',
    opened_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    opened_by VARCHAR(50) NOT NULL DEFAULT '' COMMENT '执行更季的 OPS 凭据标识（可追溯）',
    note VARCHAR(200) NULL COMMENT '运维备注（可选）',
    UNIQUE KEY uk_season (season)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
