package migrate

// 幂等 DDL 辅助。MySQL/MariaDB 的 DDL 不可回滚，迁移失败后下次启动会
// 重跑同一版本，因此每条 DDL 执行前先查 information_schema，已存在即
// 跳过，保证单条迁移可重入。所有调用都在迁移咨询锁保护下进行。

import (
	"context"
	"database/sql"
	"fmt"
)

// tableExists 查 information_schema 判断表是否已存在。
func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
		table).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("查询表 %s 是否存在失败: %w", table, err)
	}
	return n > 0, nil
}

// columnExists 查 information_schema 判断列是否已存在。
func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?",
		table, column).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("查询列 %s.%s 是否存在失败: %w", table, column, err)
	}
	return n > 0, nil
}

// indexExists 查 information_schema 判断索引是否已存在。
func indexExists(ctx context.Context, db *sql.DB, table, index string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?",
		table, index).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("查询索引 %s.%s 是否存在失败: %w", table, index, err)
	}
	return n > 0, nil
}

// ensureColumn 在列不存在时执行 ALTER TABLE ADD COLUMN。
// def 为完整列定义（类型、可空性、默认值、注释与 AFTER 位置），
// 与 baseline 中的列定义逐字一致，保证两条初始化路径终态 schema 相同。
func ensureColumn(ctx context.Context, db *sql.DB, table, column, def string) error {
	exists, err := columnExists(ctx, db, table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	stmt := fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN `%s` %s", table, column, def)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("为表 %s 添加列 %s 失败: %w", table, column, err)
	}
	return nil
}

// ensureIndex 在索引不存在时创建索引。
func ensureIndex(ctx context.Context, db *sql.DB, table, index string, unique bool, columns string) error {
	exists, err := indexExists(ctx, db, table, index)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	kind := "INDEX"
	if unique {
		kind = "UNIQUE INDEX"
	}
	stmt := fmt.Sprintf("CREATE %s `%s` ON `%s` (%s)", kind, index, table, columns)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("为表 %s 创建索引 %s 失败: %w", table, index, err)
	}
	return nil
}

// ensureTable 在表不存在时执行给定的 CREATE TABLE 语句。
func ensureTable(ctx context.Context, db *sql.DB, table, createStmt string) error {
	exists, err := tableExists(ctx, db, table)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, createStmt); err != nil {
		return fmt.Errorf("创建表 %s 失败: %w", table, err)
	}
	return nil
}
