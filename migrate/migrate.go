package migrate

import (
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Config 数据库迁移配置
type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
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

// RunMigrations 执行数据库迁移
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

	// 从本地文件系统加载迁移文件
	sourceDriver, err := loadMigrationsFromFS(cfg.MigrationsPath)
	if err != nil {
		return fmt.Errorf("加载迁移文件失败: %w", err)
	}

	// 创建 migrate 实例
	m, err := migrate.NewWithSourceInstance("iofs", sourceDriver, "mysql://"+dsn)
	if err != nil {
		return fmt.Errorf("创建 migrate 实例失败: %w", err)
	}
	defer m.Close()

	// 执行迁移
	log.Println("开始执行数据库迁移...")
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("执行迁移失败: %w", err)
	}

	log.Println("数据库迁移完成")
	return nil
}

// loadMigrationsFromFS 从文件系统加载迁移
func loadMigrationsFromFS(path string) (source.Driver, error) {
	// 创建子文件系统
	subFS, err := fs.Sub(os.DirFS(path), ".")
	if err != nil {
		return nil, fmt.Errorf("创建子文件系统失败: %w", err)
	}

	return iofs.New(subFS, "migrations")
}

// getEnv 获取环境变量，带默认值
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// GetMigrationFiles 获取迁移文件列表
func GetMigrationFiles(migrationsPath string) ([]string, error) {
	var files []string

	err := filepath.Walk(migrationsPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".up.sql") {
			files = append(files, filepath.Base(path))
		}
		return nil
	})

	return files, err
}

// GetCurrentVersion 获取当前迁移版本
func GetCurrentVersion(cfg *Config) (uint, bool, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return 0, false, err
	}
	defer db.Close()

	source, err := loadMigrationsFromFS(cfg.MigrationsPath)
	if err != nil {
		return 0, false, err
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, "mysql://"+dsn)
	if err != nil {
		return 0, false, err
	}
	defer m.Close()

	version, dirty, err := m.Version()
	if err != nil {
		if err == migrate.ErrNilVersion {
			return 0, false, nil
		}
		return 0, false, err
	}

	return version, dirty, nil
}
