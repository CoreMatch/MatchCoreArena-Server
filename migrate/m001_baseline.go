package migrate

// v1：冻结的 baseline —— 全新库的起点。
//
// 内容即 migrations/baseline.sql，全部为 CREATE TABLE IF NOT EXISTS，
// 因此对已有表逐句空操作：在既有库上重放是安全的（也能补齐半途建库时
// 缺失的表）。自 v2 起不再直接改动该文件；该文件必须永远只包含可重入
// 语句，结构变更一律追加新版本迁移。

import (
	"context"
	"database/sql"
	"fmt"
)

func runBaseline(ctx context.Context, db *sql.DB) error {
	content, err := MigrationsFS.ReadFile("migrations/baseline.sql")
	if err != nil {
		return fmt.Errorf("读取 baseline 失败: %w", err)
	}
	for i, stmt := range splitSQL(string(content)) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("baseline 第 %d 条语句执行失败: %w", i+1, err)
		}
	}
	return nil
}
