package migrate

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/go-sql-driver/mysql"
)

// Config 数据库迁移配置
type Config struct {
	DBHost         string
	DBPort         string
	DBUser         string
	DBPassword     string
	DBName         string
	MigrationsPath string
}

// LoadConfig 从环境变量加载配置
func LoadConfig() *Config {
	return &Config{
		DBHost:         getEnv("DB_HOST", "localhost"),
		DBPort:         getEnv("DB_PORT", "3306"),
		DBUser:         getEnv("DB_USER", "root"),
		DBPassword:     getEnv("DB_PASSWORD", ""),
		DBName:         getEnv("DB_NAME", "matchcorearena"),
		MigrationsPath: getEnv("MIGRATIONS_PATH", "migrations"),
	}
}

// RunMigrations 执行数据库 baseline 迁移
func RunMigrations(cfg *Config) error {
	// 构建 MySQL 连接字符串
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)

	// 打开数据库连接
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer db.Close()

	// 测试数据库连接
	if err := db.Ping(); err != nil {
		return fmt.Errorf("数据库 ping 失败: %w", err)
	}

	// 检查是否已初始化（检查是否有任何表）
	if isInitialized(db) {
		log.Println("数据库已初始化，跳过 baseline")
		return nil
	}

	// 执行 baseline SQL
	baselinePath := filepath.Join(cfg.MigrationsPath, "baseline.sql")
	log.Printf("执行 baseline: %s", baselinePath)

	sqlContent, err := os.ReadFile(baselinePath)
	if err != nil {
		return fmt.Errorf("读取 baseline 文件失败: %w", err)
	}

	if _, err := db.Exec(string(sqlContent)); err != nil {
		return fmt.Errorf("执行 baseline 失败: %w", err)
	}

	log.Println("数据库 baseline 完成")
	return nil
}

// isInitialized 检查数据库是否已初始化
func isInitialized(db *sql.DB) bool {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()").Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// getEnv 获取环境变量，带默认值
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
