package migrate

import (
	"strings"
	"testing"
)

func TestSplitSQL(t *testing.T) {
	content := `-- 注释行
CREATE TABLE a (id INT);

CREATE TABLE b (
  id INT
);
-- 另一条注释
`
	stmts := splitSQL(content)
	if len(stmts) != 2 {
		t.Fatalf("语句数 = %d, 期望 2: %q", len(stmts), stmts)
	}
	if !strings.HasPrefix(stmts[0], "CREATE TABLE a") || strings.HasSuffix(stmts[0], ";") {
		t.Errorf("第 1 条语句拆分异常: %q", stmts[0])
	}
	if !strings.Contains(stmts[1], "id INT") || strings.HasSuffix(stmts[1], ";") {
		t.Errorf("第 2 条语句拆分异常: %q", stmts[1])
	}
}

func TestSplitSQLTrailingStatementWithoutSemicolon(t *testing.T) {
	stmts := splitSQL("CREATE TABLE a (id INT)")
	if len(stmts) != 1 || stmts[0] != "CREATE TABLE a (id INT)" {
		t.Fatalf("无分号结尾语句处理异常: %q", stmts)
	}
}

// TestMigrationRegistryInvariants 迁移集必须从 1 起连续递增且可执行。
func TestMigrationRegistryInvariants(t *testing.T) {
	if len(migrations) < 2 {
		t.Fatalf("迁移集至少应包含 baseline 与一条增量迁移: %d", len(migrations))
	}
	for i, m := range migrations {
		if m.Version != i+1 {
			t.Errorf("迁移版本必须从 1 连续递增: 第 %d 条版本为 %d", i, m.Version)
		}
		if m.Name == "" {
			t.Errorf("迁移 v%d 缺少名称", m.Version)
		}
		if m.Run == nil {
			t.Errorf("迁移 v%d (%s) 缺少执行函数", m.Version, m.Name)
		}
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_NAME", "mca_test")
	cfg := LoadConfig()
	if cfg.DBName != "mca_test" {
		t.Errorf("DB_NAME = %q, 期望 mca_test", cfg.DBName)
	}
	if cfg.DBPort != "3306" {
		t.Errorf("DB_PORT 默认值 = %q, 期望 3306", cfg.DBPort)
	}
}
