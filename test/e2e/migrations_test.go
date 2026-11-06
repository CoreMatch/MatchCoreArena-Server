//go:build e2e

package e2e_test

// 数据库迁移双路径验证：
//
//	(a) 旧库升级——用与生产旧库同形的 baseline 快照（eff1784，2026-11-05
//	    生产初始化所用二进制 71f45ea 的 baseline）建库并灌入示例数据，
//	    再由新代码启动迁移链升级；
//	(b) 全新空库初始化——迁移链从 v1 baseline 起建。
//
// 两者终态 schema（表、列、类型、可空性、默认值、索引、外键）必须零差异，
// 且 (a) 中既有数据不丢。另验证多实例并发启动与重复执行的行为。

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"MatchCoreArena-Server/migrate"
)

// legacyBaselineFixture 生产旧库形状快照（见 testdata 内文件头注释）。
const legacyBaselineFixture = "legacy_baseline_eff1784.sql"

// e2eMigrateConfig 测试库迁移配置（与 setupEnv 相同的库与环境变量覆盖）。
func e2eMigrateConfig() *migrate.Config {
	cfg := &migrate.Config{DBHost: "127.0.0.1", DBPort: "13306", DBUser: "mca", DBPassword: "mca-e2e-pass", DBName: "mca_e2e"}
	if v := os.Getenv("MCA_E2E_DB_USER"); v != "" {
		cfg.DBUser = v
	}
	if v := os.Getenv("MCA_E2E_DB_PASSWORD"); v != "" {
		cfg.DBPassword = v
	}
	return cfg
}

// migrationTestDSN 测试库 DSN（与 e2e_test.go 相同的环境变量覆盖）。
func migrationTestDSN() string {
	if v := os.Getenv("MCA_E2E_MYSQL_DSN"); v != "" {
		return v
	}
	return e2eDSNDefault
}

func withMultiStatements(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "multiStatements=true"
}

func openMigrationTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("数据库 Ping 失败: %v", err)
	}
	return db
}

// loadLegacyFixture 执行旧 baseline 快照，得到与生产旧库同形的结构。
func loadLegacyFixture(t *testing.T, dsn string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", legacyBaselineFixture))
	if err != nil {
		t.Fatalf("读取旧 baseline 快照失败: %v", err)
	}
	db, err := sql.Open("mysql", withMultiStatements(dsn))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(string(content)); err != nil {
		t.Fatalf("执行旧 baseline 快照失败: %v", err)
	}
}

// seedLegacyData 在旧形状库中灌入示例数据（覆盖全部既有业务表）。
func seedLegacyData(t *testing.T, db *sql.DB) {
	t.Helper()
	exec := func(query string, args ...any) {
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("灌入示例数据失败 (%s): %v", query, err)
		}
	}
	exec(`INSERT INTO users (uid, level, experience, rank_score, wins_count) VALUES
		(101, 5, 1200, 1500, 12), (102, 3, 400, 1400, 7)`)
	exec(`INSERT INTO matches (match_type, winner_uid, loser_uid, winner_team_id, survival_rate,
		winner_score, loser_score, winner_perf, loser_perf, duration_seconds, started_at, finished_at)
		VALUES ('1v1', 101, 102, 'A', 0.5, 1500, 1400, 1.0, 1.0, 615,
		'2026-11-05 10:00:00', '2026-11-05 10:10:15')`)
	var matchID int64
	if err := db.QueryRow(`SELECT id FROM matches WHERE winner_uid = 101`).Scan(&matchID); err != nil {
		t.Fatalf("读取示例对局失败: %v", err)
	}
	exec(`INSERT INTO match_participants (match_id, user_uid, team_id, is_winner,
		rank_score_before, rank_score_delta, survival_time_seconds, kills, deaths) VALUES
		(?, 101, 'A', TRUE, 1480, 20, 600, 3, 1),
		(?, 102, 'B', FALSE, 1420, -20, 300, 1, 3)`, matchID, matchID)
	exec(`INSERT INTO teams (name, leader_uid) VALUES ('e2e-legacy-team', 101)`)
	exec(`INSERT INTO team_members (team_id, user_uid, role) VALUES (1, 101, 2), (1, 102, 0)`)
	exec(`INSERT INTO friends (user_uid, friend_uid, status) VALUES (101, 102, 1)`)
	exec(`INSERT INTO rankings (user_uid, rank_type, score, rank_position, season) VALUES
		(101, 'arena', 1500, 1, 1)`)
}

// assertAppliedVersions 校验簿记中恰好记录了给定版本（各一条、按序）。
func assertAppliedVersions(t *testing.T, db *sql.DB, want ...int) {
	t.Helper()
	rows, err := db.Query(
		"SELECT version FROM schema_migrations WHERE service = ? ORDER BY version", migrate.ServiceName)
	if err != nil {
		t.Fatalf("读取 schema_migrations 失败: %v", err)
	}
	defer rows.Close()
	var got []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("解析簿记版本失败: %v", err)
		}
		got = append(got, v)
	}
	if len(got) != len(want) {
		t.Fatalf("簿记版本 = %v, 期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("簿记版本 = %v, 期望 %v", got, want)
		}
	}
}

// assertLegacyDataPreserved 升级后既有数据完整、取值未变，新列取默认值。
func assertLegacyDataPreserved(t *testing.T, db *sql.DB) {
	t.Helper()
	count := func(table string) int {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM `" + table + "`").Scan(&n); err != nil {
			t.Fatalf("统计 %s 失败: %v", table, err)
		}
		return n
	}
	for table, want := range map[string]int{
		"users": 2, "matches": 1, "match_participants": 2,
		"teams": 1, "team_members": 2, "friends": 1, "rankings": 1,
	} {
		if got := count(table); got != want {
			t.Errorf("表 %s 行数 = %d, 期望 %d（既有数据丢失）", table, got, want)
		}
	}

	var winnerScore, loserScore, duration int
	var source string
	var gameID sql.NullString
	err := db.QueryRow(
		`SELECT winner_score, loser_score, duration_seconds, source, game_id FROM matches WHERE winner_uid = 101`).
		Scan(&winnerScore, &loserScore, &duration, &source, &gameID)
	if err != nil {
		t.Fatalf("读取升级后的 matches 失败: %v", err)
	}
	if winnerScore != 1500 || loserScore != 1400 || duration != 615 {
		t.Errorf("matches 既有值被改动: winner=%d loser=%d duration=%d", winnerScore, loserScore, duration)
	}
	if source != "game" {
		t.Errorf("matches.source 默认值 = %q, 期望 game", source)
	}
	if gameID.Valid {
		t.Errorf("matches.game_id 应为 NULL, 实际 %q", gameID.String)
	}

	var notDraws int
	if err := db.QueryRow(`SELECT COUNT(*) FROM match_participants WHERE is_draw = FALSE`).Scan(&notDraws); err != nil {
		t.Fatalf("读取 match_participants.is_draw 失败: %v", err)
	}
	if notDraws != 2 {
		t.Errorf("match_participants.is_draw 取默认 FALSE 的行数 = %d, 期望 2", notDraws)
	}
}

// schemaFingerprint 从 information_schema 提取库的完整结构指纹：
// 表（名称/引擎/排序规则）、列（名称/位置/类型/可空/默认/附加/注释）、
// 索引（名称/列序/唯一性/类型）与外键（语义与级联规则，不含自动生成的
// 约束名）。两条初始化路径的指纹必须逐字相同。
func schemaFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	dump := func(header, query string) {
		b.WriteString(header + "\n")
		rows, err := db.Query(query)
		if err != nil {
			t.Fatalf("提取结构指纹失败 (%s): %v", header, err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			t.Fatalf("读取列名失败: %v", err)
		}
		b.WriteString(strings.Join(cols, "|") + "\n")
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("扫描结构行失败: %v", err)
			}
			parts := make([]string, len(cols))
			for i, v := range vals {
				if v.Valid {
					parts[i] = v.String
				} else {
					parts[i] = "<NULL>"
				}
			}
			b.WriteString(strings.Join(parts, "|") + "\n")
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("遍历结构行失败: %v", err)
		}
	}
	dump("[tables]",
		"SELECT TABLE_NAME, ENGINE, TABLE_COLLATION FROM information_schema.TABLES "+
			"WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME")
	dump("[columns]",
		"SELECT TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION, COLUMN_TYPE, IS_NULLABLE, "+
			"COLUMN_DEFAULT, EXTRA, COLUMN_COMMENT FROM information_schema.COLUMNS "+
			"WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, ORDINAL_POSITION")
	dump("[indexes]",
		"SELECT TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX, COLUMN_NAME, NON_UNIQUE, INDEX_TYPE "+
			"FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() "+
			"ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX")
	dump("[foreign_keys]",
		"SELECT rc.TABLE_NAME, rc.REFERENCED_TABLE_NAME, rc.DELETE_RULE, rc.UPDATE_RULE "+
			"FROM information_schema.REFERENTIAL_CONSTRAINTS rc "+
			"WHERE rc.CONSTRAINT_SCHEMA = DATABASE() ORDER BY rc.TABLE_NAME")
	return b.String()
}

// TestMigrationDualPathSchemaParity 双路径终态 schema 零差异 + 数据不丢。
func TestMigrationDualPathSchemaParity(t *testing.T) {
	dsn := migrationTestDSN()
	cfg := e2eMigrateConfig()

	// 路径 (a)：旧库升级
	resetDatabase(t, dsn)
	loadLegacyFixture(t, dsn)
	db := openMigrationTestDB(t, dsn)
	seedLegacyData(t, db)
	if err := migrate.RunMigrations(cfg); err != nil {
		t.Fatalf("旧库升级迁移失败: %v", err)
	}
	assertAppliedVersions(t, db, 1, 2)
	assertLegacyDataPreserved(t, db)
	upgradedFP := schemaFingerprint(t, db)
	db.Close()

	// 路径 (b)：全新空库初始化
	resetDatabase(t, dsn)
	if err := migrate.RunMigrations(cfg); err != nil {
		t.Fatalf("全新库初始化迁移失败: %v", err)
	}
	db = openMigrationTestDB(t, dsn)
	assertAppliedVersions(t, db, 1, 2)
	freshFP := schemaFingerprint(t, db)
	db.Close()

	if upgradedFP != freshFP {
		t.Errorf("两条路径的终态 schema 存在差异\n--- 旧库升级 ---\n%s\n--- 全新初始化 ---\n%s",
			upgradedFP, freshFP)
	}
}

// TestMigrationConcurrentInstances 多实例并发启动：咨询锁串行化，
// 每个版本恰好落一条簿记，终态结构完整。
func TestMigrationConcurrentInstances(t *testing.T) {
	dsn := migrationTestDSN()
	resetDatabase(t, dsn)
	loadLegacyFixture(t, dsn) // 旧库形状，无簿记

	const instances = 5
	var wg sync.WaitGroup
	errs := make([]error, instances)
	for i := 0; i < instances; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = migrate.RunMigrations(e2eMigrateConfig())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("实例 %d 迁移失败: %v", i, err)
		}
	}

	db := openMigrationTestDB(t, dsn)
	defer db.Close()
	assertAppliedVersions(t, db, 1, 2)

	var duplicated int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM (SELECT version FROM schema_migrations WHERE service = ? "+
			"GROUP BY version HAVING COUNT(*) > 1) d", migrate.ServiceName).Scan(&duplicated)
	if err != nil {
		t.Fatalf("检查重复簿记失败: %v", err)
	}
	if duplicated != 0 {
		t.Errorf("存在重复记录的迁移版本数: %d", duplicated)
	}

	var tables int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() "+
			"AND TABLE_NAME LIKE 'matchmaking\\_%'").Scan(&tables); err != nil {
		t.Fatalf("统计 matchmaking 表失败: %v", err)
	}
	if tables != 5 {
		t.Errorf("matchmaking_* 表数量 = %d, 期望 5", tables)
	}
}

// TestMigrationRerunIsNoop 重复执行迁移为幂等空操作。
func TestMigrationRerunIsNoop(t *testing.T) {
	dsn := migrationTestDSN()
	resetDatabase(t, dsn)
	cfg := e2eMigrateConfig()
	if err := migrate.RunMigrations(cfg); err != nil {
		t.Fatalf("首次迁移失败: %v", err)
	}
	if err := migrate.RunMigrations(cfg); err != nil {
		t.Fatalf("重复迁移应成功: %v", err)
	}
	db := openMigrationTestDB(t, dsn)
	defer db.Close()
	assertAppliedVersions(t, db, 1, 2)
}
