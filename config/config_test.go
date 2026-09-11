package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_CreatesDefaultConfigWhenMissing(t *testing.T) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")

	// 确保配置文件不存在
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("测试前配置文件不应存在")
	}

	// 加载配置 - 应该自动创建默认配置文件
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	// 验证配置文件已创建
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("配置文件应该已被创建")
	}

	// 验证配置内容
	if cfg.Version != CurrentVersion {
		t.Errorf("版本号不匹配: 期望 %s, 实际 %s", CurrentVersion, cfg.Version)
	}
	if cfg.Server.Port != 9178 {
		t.Errorf("服务器端口不匹配: 期望 9178, 实际 %d", cfg.Server.Port)
	}
	if cfg.Database.Host != "localhost" {
		t.Errorf("数据库主机不匹配: 期望 localhost, 实际 %s", cfg.Database.Host)
	}
	if cfg.Database.Name != "matchcorearena" {
		t.Errorf("数据库名称不匹配: 期望 matchcorearena, 实际 %s", cfg.Database.Name)
	}
	if cfg.Migrate.Path != "migrations" {
		t.Errorf("迁移路径不匹配: 期望 migrations, 实际 %s", cfg.Migrate.Path)
	}
	if cfg.Auth.HRPAuth.BaseURL != "http://localhost:8080" {
		t.Errorf("HRPAuth BaseURL 不匹配: 期望 http://localhost:8080, 实际 %s", cfg.Auth.HRPAuth.BaseURL)
	}
}

func TestLoad_LoadsExistingConfig(t *testing.T) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")

	// 创建测试配置文件
	configContent := `version: "1.2.0"
server:
  host: "127.0.0.1"
  port: 8080
  read_timeout_seconds: 30
  write_timeout_seconds: 30
database:
  host: "db.example.com"
  port: 5432
  user: "testuser"
  password: "testpass"
  name: "testdb"
  max_open_conns: 100
  max_idle_conns: 20
redis:
  host: "redis.example.com"
  port: 6380
  password: "redispass"
  db: 1
migrate:
  path: "db/migrations"
auth:
  hrpauth:
    base_url: "https://auth.example.com"
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("写入测试配置文件失败: %v", err)
	}

	// 加载配置
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	// 验证配置内容
	if cfg.Version != "1.2.0" {
		t.Errorf("版本号不匹配: 期望 1.2.0, 实际 %s", cfg.Version)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("服务器主机不匹配: 期望 127.0.0.1, 实际 %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("服务器端口不匹配: 期望 8080, 实际 %d", cfg.Server.Port)
	}
	if cfg.Database.Host != "db.example.com" {
		t.Errorf("数据库主机不匹配: 期望 db.example.com, 实际 %s", cfg.Database.Host)
	}
	if cfg.Database.Name != "testdb" {
		t.Errorf("数据库名称不匹配: 期望 testdb, 实际 %s", cfg.Database.Name)
	}
	if cfg.Auth.HRPAuth.BaseURL != "https://auth.example.com" {
		t.Errorf("HRPAuth BaseURL 不匹配: 期望 https://auth.example.com, 实际 %s", cfg.Auth.HRPAuth.BaseURL)
	}
}

func TestLoad_CreatesDirectoryIfMissing(t *testing.T) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 使用不存在的子目录
	configPath := filepath.Join(tmpDir, "subdir", "config.yaml")

	// 加载配置 - 应该自动创建目录和配置文件
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	// 验证目录已创建
	dirPath := filepath.Join(tmpDir, "subdir")
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		t.Fatal("子目录应该已被创建")
	}

	// 验证配置文件已创建
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("配置文件应该已被创建")
	}

	// 验证基本配置
	if cfg.Version != CurrentVersion {
		t.Errorf("版本号不匹配: 期望 %s, 实际 %s", CurrentVersion, cfg.Version)
	}
}