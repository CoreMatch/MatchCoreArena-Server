package migrate

import (
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
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

// RunMigrations 执行所有未完成的数据库迁移（基线 + 增量）。
// 通过 schema_migrations 表追踪已执行的迁移文件，确保幂等。
// 任何单个迁移失败都会立即返回错误，不静默跳过。
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

	// 1. 确保 schema_migrations 追踪表存在
	if err := ensureMigrationTable(db); err != nil {
		return fmt.Errorf("创建迁移追踪表失败: %w", err)
	}

	// 2. 列出 migrationsFS 中所有 .sql 文件，按文件名排序执行
	entries, err := fs.ReadDir(MigrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("读取迁移文件目录失败: %w", err)
	}

	sqlFiles := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".sql") {
			sqlFiles = append(sqlFiles, name)
		}
	}
	// 文件名排序：baseline.sql 在前，之后按 YYYYMMDD_ 日期前缀递增
	sort.Strings(sqlFiles)

	// 3. 查询已执行的迁移
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		return fmt.Errorf("查询已执行迁移失败: %w", err)
	}

	for _, name := range sqlFiles {
		if applied[name] {
			log.Printf("迁移已执行，跳过: %s", name)
			continue
		}

		log.Printf("执行迁移: %s", name)
		if err := runSingleMigration(db, name); err != nil {
			return fmt.Errorf("执行迁移 %s 失败: %w", name, err)
		}
		log.Printf("迁移完成: %s", name)
	}

	log.Println("所有数据库迁移已完成")
	return nil
}

// ensureMigrationTable 创建 schema_migrations 追踪表。
func ensureMigrationTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename VARCHAR(255) PRIMARY KEY,
			executed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
	`)
	return err
}

// loadAppliedMigrations 返回已执行的迁移文件集合。
func loadAppliedMigrations(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`SELECT filename FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = true
	}
	return applied, rows.Err()
}

// runSingleMigration 执行单个迁移文件（支持多语句），成功后登记到 schema_migrations。
func runSingleMigration(db *sql.DB, filename string) error {
	sqlPath := path.Join("migrations", filename)
	content, err := MigrationsFS.ReadFile(sqlPath)
	if err != nil {
		return fmt.Errorf("读取文件失败: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}

	for i, stmt := range splitSQL(string(content)) {
		if _, err := tx.Exec(stmt); err != nil {
			tx.Rollback()
			return fmt.Errorf("第 %d 条语句执行失败: %w (stmt: %.120s)", i+1, err, stmt)
		}
	}

	// 登记执行
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (filename, executed_at) VALUES (?, ?)`,
		filename, time.Now(),
	); err != nil {
		tx.Rollback()
		return fmt.Errorf("登记迁移失败: %w", err)
	}

	return tx.Commit()
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
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
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
	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}
