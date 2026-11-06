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
	if cfg.Auth.HRPAuth.BaseURL != "http://localhost:2778" {
		t.Errorf("HRPAuth BaseURL 不匹配: 期望 http://localhost:2778, 实际 %s", cfg.Auth.HRPAuth.BaseURL)
	}
	if !cfg.Matchmaking.Enabled {
		t.Error("默认配置应启用匹配系统")
	}
	if cfg.Matchmaking.TickSeconds != 1 {
		t.Errorf("匹配周期不匹配: 期望 1, 实际 %d", cfg.Matchmaking.TickSeconds)
	}
	if mode, ok := cfg.Matchmaking.FindMode("1v1"); !ok || mode.TotalPlayers() != 2 {
		t.Errorf("默认配置应包含 1v1 模式（2 人），实际 %+v", cfg.Matchmaking.Modes)
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

	// 验证配置内容（Load 会把旧版本配置迁移到 CurrentVersion）
	if cfg.Version != CurrentVersion {
		t.Errorf("版本号不匹配: 期望 %s, 实际 %s", CurrentVersion, cfg.Version)
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

// TestLoad_Migrates150To160 覆盖 1.5.0 → 1.6.0 迁移：旧配置无
// matchmaking 块时自动补齐默认值，不报错。
func TestLoad_Migrates150To160(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `version: "1.5.0"
server:
  host: "127.0.0.1"
  port: 9178
database:
  host: "localhost"
  port: 3306
  user: "u"
  password: "p"
  name: "d"
redis:
  host: "localhost"
  port: 6379
auth:
  hrpauth:
    base_url: "http://localhost:2778"
  maintenance_token: "t"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("写入测试配置文件失败: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("1.5.0 配置应能迁移加载: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("版本号不匹配: 期望 %s, 实际 %s", CurrentVersion, cfg.Version)
	}
	if cfg.Matchmaking.QueueTimeoutSeconds != 300 {
		t.Errorf("排队超时默认值不匹配: 期望 300, 实际 %d", cfg.Matchmaking.QueueTimeoutSeconds)
	}
	if len(cfg.Matchmaking.Modes) != 2 {
		t.Errorf("默认模式数不匹配: 期望 2, 实际 %d", len(cfg.Matchmaking.Modes))
	}
}

// TestLoad_Migrates160To170 覆盖 1.6.0 → 1.7.0 迁移：
// 移除虚拟对局独立开关（对局上报收敛为单一入口）。
func TestLoad_Migrates160To170(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `version: "1.6.0"
server:
  host: "127.0.0.1"
  port: 9178
database:
  host: "localhost"
  port: 3306
  user: "u"
  password: "p"
  name: "d"
redis:
  host: "localhost"
  port: 6379
auth:
  hrpauth:
    base_url: "http://localhost:2778"
  maintenance_token: "t"
virtual_matches:
  enabled: true
matchmaking:
  enabled: true
  tick_seconds: 1
  queue_timeout_seconds: 300
  base_rating_window: 200
  rating_window_growth_per_minute: 100
  max_rating_window: 1000
  modes:
    - match_type: "1v1"
      team_sizes: [1, 1]
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("写入测试配置文件失败: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("1.6.0 配置应能迁移加载: %v", err)
	}
	if cfg.Version != "1.7.0" {
		t.Errorf("版本号不匹配: 期望 1.7.0, 实际 %s", cfg.Version)
	}
	if !cfg.Matchmaking.Enabled {
		t.Error("迁移后匹配系统应保持启用")
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

func TestLoad_TokensPath(t *testing.T) {
	base := `version: "1.4.0"
server:
  host: "127.0.0.1"
  port: 8080
database:
  host: "db.example.com"
  port: 3306
  user: "u"
  password: "p"
  name: "d"
redis:
  host: "redis.example.com"
  port: 6379
auth:
  hrpauth:
    base_url: "https://auth.example.com"
  maintenance_token: "t"
`
	// 显式 tokens_path 被解析。
	tmpDir := t.TempDir()
	explicit := filepath.Join(tmpDir, "explicit.yaml")
	if err := os.WriteFile(explicit, []byte(base+"  tokens_path: /data/tokens.yaml\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(explicit)
	if err != nil {
		t.Fatalf("Load explicit: %v", err)
	}
	if cfg.Auth.TokensPath != "/data/tokens.yaml" {
		t.Errorf("tokens_path = %q, want /data/tokens.yaml", cfg.Auth.TokensPath)
	}

	// 未配置时保持空值（运行期回落到默认 config/tokens.yaml）。
	missing := filepath.Join(tmpDir, "missing.yaml")
	if err := os.WriteFile(missing, []byte(base), 0644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(missing)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if cfg2.Auth.TokensPath != "" {
		t.Errorf("tokens_path should default to empty, got %q", cfg2.Auth.TokensPath)
	}
}
