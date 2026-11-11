package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

// ServiceName 是本服务在 schema_migrations 簿记表中的服务名。
const ServiceName = "MatchCoreArena-Server"

// 咨询锁：多实例并发启动时将迁移执行串行化。
// 锁绑定在专用连接上，进程退出或连接断开时由服务端自动释放。
const (
	advisoryLockName    = "MatchCoreArena-Server.schema_migrations"
	advisoryLockTimeout = 60 // 秒
)

// Config 数据库迁移配置
type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
}

// LoadConfig 从环境变量加载配置
func LoadConfig() *Config {
	return &Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "3306"),
		DBUser:     getEnv("DB_USER", "root"),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "matchcorearena"),
	}
}

// Migration 是一条编号迁移。Version 从 1 起严格递增；
// Run 必须幂等可重入（失败后下次启动会重跑同一版本）。
type Migration struct {
	Version int
	Name    string
	Run     func(ctx context.Context, db *sql.DB) error
}

// migrations 是编号递增的迁移集。
// v1 为冻结的 baseline（全新库的起点），此后不再直接改动；
// 结构变更一律追加新版本迁移（v2、v3……）。
var migrations = []Migration{
	{Version: 1, Name: "baseline", Run: runBaseline},
	{Version: 2, Name: "matchmaking_and_unified_reporting", Run: applyV2},
	{Version: 3, Name: "match_assignment_link", Run: applyV3},
	{Version: 4, Name: "room_reservations", Run: applyV4},
	{Version: 5, Name: "party_invites", Run: applyV5},
}

// RunMigrations 连接数据库并执行迁移，启动时调用。
// 返回错误即表示数据库未达到预期结构，调用方应中止启动。
func RunMigrations(cfg *Config) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("数据库 ping 失败: %w", err)
	}

	return ApplyMigrations(context.Background(), db)
}

// ApplyMigrations 在已打开的库上按序只执行未应用的迁移。语义：
//
//   - schema_migrations(service, version, name, applied_at) 记录已应用版本，
//     成功应用后才写入；
//   - GET_LOCK 咨询锁串行化并发实例，拿到锁后重读已应用集合，
//     多实例同时启动不会重复执行；
//   - 未记录的版本一律重放，依赖每条迁移自身的幂等性：v1（baseline）全部
//     是 CREATE TABLE IF NOT EXISTS，在既有库上逐句空操作，在半途建成的库上
//     补齐缺失的表；v2 起的增量迁移每条 DDL 前查 information_schema；
//   - 任一版本失败即返回错误（调用方中止启动，不得继续提供写服务）；
//     下次启动重跑该版本，可安全重入。
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	return apply(ctx, db, migrations)
}

func apply(ctx context.Context, db *sql.DB, migs []Migration) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移专用连接失败: %w", err)
	}
	defer conn.Close()

	if err := acquireAdvisoryLock(ctx, conn); err != nil {
		return err
	}
	defer releaseAdvisoryLock(ctx, conn)

	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	known := make(map[int]bool, len(migs))
	for _, m := range migs {
		known[m.Version] = true
		if applied[m.Version] {
			continue
		}
		log.Printf("应用数据库迁移 v%d (%s)...", m.Version, m.Name)
		if err := m.Run(ctx, db); err != nil {
			return fmt.Errorf("数据库迁移 v%d (%s) 失败: %w", m.Version, m.Name, err)
		}
		if err := recordVersion(ctx, conn, m); err != nil {
			return fmt.Errorf("数据库迁移 v%d (%s) 已执行但写入簿记失败: %w", m.Version, m.Name, err)
		}
		log.Printf("数据库迁移 v%d (%s) 完成", m.Version, m.Name)
	}

	// 簿记中出现比当前二进制迁移集更高的版本（降级运行）时给出警告。
	for v := range applied {
		if !known[v] {
			log.Printf("警告: schema_migrations 中存在当前二进制未知的迁移版本 v%d，可能发生了降级", v)
		}
	}
	return nil
}

// acquireAdvisoryLock 在当前连接上获取迁移咨询锁（阻塞至多 lockTimeout 秒）。
func acquireAdvisoryLock(ctx context.Context, conn *sql.Conn) error {
	var res sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", advisoryLockName, advisoryLockTimeout).Scan(&res); err != nil {
		return fmt.Errorf("获取迁移咨询锁失败: %w", err)
	}
	if !res.Valid || res.Int64 != 1 {
		return fmt.Errorf("获取迁移咨询锁超时（等待 %ds）：另一实例可能正在执行迁移", advisoryLockTimeout)
	}
	return nil
}

// releaseAdvisoryLock 释放迁移咨询锁；连接关闭时服务端也会自动释放。
func releaseAdvisoryLock(ctx context.Context, conn *sql.Conn) {
	var res sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", advisoryLockName).Scan(&res); err != nil {
		log.Printf("警告: 释放迁移咨询锁失败: %v", err)
	}
}

// ensureMigrationsTable 创建迁移簿记表（不存在才创建）。
func ensureMigrationsTable(ctx context.Context, conn *sql.Conn) error {
	const stmt = "CREATE TABLE IF NOT EXISTS schema_migrations (" +
		"service VARCHAR(64) NOT NULL," +
		"version INT NOT NULL," +
		"name VARCHAR(191) NOT NULL DEFAULT ''," +
		"applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
		"PRIMARY KEY (service, version)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"
	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("创建 schema_migrations 簿记表失败: %w", err)
	}
	return nil
}

// appliedVersions 读取本服务已应用的迁移版本集合。
func appliedVersions(ctx context.Context, conn *sql.Conn) (map[int]bool, error) {
	rows, err := conn.QueryContext(ctx, "SELECT version FROM schema_migrations WHERE service = ?", ServiceName)
	if err != nil {
		return nil, fmt.Errorf("读取 schema_migrations 失败: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("解析 schema_migrations 记录失败: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// recordVersion 将一条迁移写入簿记（applied_at 取数据库当前时间）。
func recordVersion(ctx context.Context, conn *sql.Conn, m Migration) error {
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO schema_migrations (service, version, name) VALUES (?, ?, ?)",
		ServiceName, m.Version, m.Name); err != nil {
		return fmt.Errorf("写入 schema_migrations (v%d) 失败: %w", m.Version, err)
	}
	return nil
}

// getEnv 获取环境变量，带默认值
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// splitSQL 将 SQL 文件按分号拆分为独立语句，跳过空语句和纯注释行。
func splitSQL(content string) []string {
	var stmts []string
	var current strings.Builder
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		// 跳过空行和纯注释行
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
		// 遇到分号结尾则提交一条语句
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSpace(current.String())
			stmt = strings.TrimSuffix(stmt, ";")
			stmt = strings.TrimSpace(stmt)
			if stmt != "" {
				stmts = append(stmts, stmt)
			}
			current.Reset()
		}
	}
	// 处理最后一条没有分号结尾的语句
	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}
