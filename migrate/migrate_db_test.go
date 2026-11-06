//go:build e2e

package migrate

// 需要真实数据库的框架级测试（与 test/e2e 共用同一临时 MariaDB，
// 运行方式见 test/e2e/e2e_test.go 头部说明）：
//   - 迁移半途失败不得写入簿记；
//   - 修复后重跑同一版本必须可重入（information_schema 前置检查）。

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

func e2eDSN() string {
	if v := os.Getenv("MCA_E2E_MYSQL_DSN"); v != "" {
		return v
	}
	return "mca:mca-e2e-pass@tcp(127.0.0.1:13306)/mca_e2e?parseTime=true&charset=utf8mb4"
}

func openE2EDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", e2eDSN())
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("数据库 Ping 失败: %v", err)
	}
	resetTables(t, db)
	return db
}

func resetTables(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()")
	if err != nil {
		t.Fatalf("查询表清单失败: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("解析表名失败: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		t.Fatalf("关闭外键检查失败: %v", err)
	}
	for _, tbl := range tables {
		if _, err := db.Exec("DROP TABLE IF EXISTS `" + tbl + "`"); err != nil {
			t.Fatalf("删除表 %s 失败: %v", tbl, err)
		}
	}
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		t.Fatalf("恢复外键检查失败: %v", err)
	}
}

func appliedSet(t *testing.T, db *sql.DB) map[int]bool {
	t.Helper()
	rows, err := db.Query("SELECT version FROM schema_migrations WHERE service = ?", ServiceName)
	if err != nil {
		t.Fatalf("读取簿记失败: %v", err)
	}
	defer rows.Close()
	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("解析簿记版本失败: %v", err)
		}
		applied[v] = true
	}
	return applied
}

// TestApplyFailureNotRecorded 迁移半途失败：已执行部分不回滚（DDL 特性），
// 但该版本不得落簿记；修复后重跑时已存在的结构被跳过（可重入）。
func TestApplyFailureNotRecorded(t *testing.T) {
	db := openE2EDB(t)
	ctx := context.Background()

	failing := Migration{Version: 2, Name: "broken", Run: func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, "ALTER TABLE users ADD COLUMN tmp_mig_col INT NULL"); err != nil {
			return err
		}
		return errors.New("模拟迁移失败")
	}}
	if err := apply(ctx, db, []Migration{migrations[0], failing}); err == nil {
		t.Fatal("预期迁移失败，实际成功")
	}

	applied := appliedSet(t, db)
	if !applied[1] {
		t.Errorf("v1 应已记录: %v", applied)
	}
	if applied[2] {
		t.Errorf("失败的 v2 不得写入簿记: %v", applied)
	}

	// 修复后重跑：tmp_mig_col 已存在，ensureColumn 查 information_schema 后跳过。
	fixed := Migration{Version: 2, Name: "fixed", Run: func(ctx context.Context, db *sql.DB) error {
		return ensureColumn(ctx, db, "users", "tmp_mig_col", "INT NULL")
	}}
	if err := apply(ctx, db, []Migration{migrations[0], fixed}); err != nil {
		t.Fatalf("修复后重跑应成功（可重入）: %v", err)
	}
	applied = appliedSet(t, db)
	if !applied[1] || !applied[2] {
		t.Errorf("重跑成功后 v1/v2 均应记录: %v", applied)
	}
}
