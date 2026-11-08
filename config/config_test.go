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
	if cfg.Version != CurrentVersion {
		t.Errorf("版本号不匹配: 期望 %s, 实际 %s", CurrentVersion, cfg.Version)
	}
	if !cfg.Matchmaking.Enabled {
		t.Error("迁移后匹配系统应保持启用")
	}
}

// TestLoad_Migrates170To180 覆盖 1.7.0 → 1.8.0 迁移：新增可空的
// matchmaking.join_target；旧配置无该块时视为未配置，加载成功。
func TestLoad_Migrates170To180(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `version: "1.7.0"
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
		t.Fatalf("1.7.0 配置应能迁移加载: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("版本号不匹配: 期望 %s, 实际 %s", CurrentVersion, cfg.Version)
	}
	if cfg.Matchmaking.JoinTarget.Configured() {
		t.Error("未配置 join_target 时 Configured 应为 false")
	}
}

// TestLoad_JoinTargetValidation 覆盖 join_target 校验：配置 host 时
// port 必须合法；未配置 host 时 port 任意值均视为未配置。
func TestLoad_JoinTargetValidation(t *testing.T) {
	base := `version: "1.8.0"
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
	t.Run("host with valid port loads", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		content := base + "  join_target:\n    host: \"play.example.com\"\n    port: 25565\n"
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(configPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.Matchmaking.JoinTarget.Configured() ||
			cfg.Matchmaking.JoinTarget.Host != "play.example.com" ||
			cfg.Matchmaking.JoinTarget.Port != 25565 {
			t.Errorf("join_target = %+v, want play.example.com:25565", cfg.Matchmaking.JoinTarget)
		}
	})

	t.Run("host with invalid port rejected", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		content := base + "  join_target:\n    host: \"play.example.com\"\n    port: 0\n"
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(configPath); err == nil {
			t.Fatal("host 已配置而 port 非法时应报错")
		}
	})

	t.Run("empty host stays unconfigured regardless of port", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		content := base + "  join_target:\n    host: \"\"\n    port: 25565\n"
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(configPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Matchmaking.JoinTarget.Configured() {
			t.Error("host 为空时应视为未配置")
		}
	})
}

// TestLoad_Migrates180To190 覆盖 1.8.0 → 1.9.0 迁移：新增可空的
// ranking.tiers（段位阶梯，纯展示）；旧配置无该块时为空阶梯。
func TestLoad_Migrates180To190(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `version: "1.8.0"
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
		t.Fatalf("1.8.0 配置应能迁移加载: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("版本号不匹配: 期望 %s, 实际 %s", CurrentVersion, cfg.Version)
	}
	if len(cfg.Ranking.Tiers) != 0 {
		t.Errorf("未配置 tiers 时应为空阶梯, 实际 %+v", cfg.Ranking.Tiers)
	}
}

// TestLoad_RankingTiers 覆盖 ranking.tiers 的解析与校验。
func TestLoad_RankingTiers(t *testing.T) {
	base := `version: "1.9.0"
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
	t.Run("tiers parsed in order", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		content := base + "ranking:\n  tiers:\n    - name: \"青铜\"\n      min_score: 0\n    - name: \"白银\"\n      min_score: 1000\n"
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(configPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(cfg.Ranking.Tiers) != 2 ||
			cfg.Ranking.Tiers[0].Name != "青铜" || cfg.Ranking.Tiers[0].MinScore != 0 ||
			cfg.Ranking.Tiers[1].Name != "白银" || cfg.Ranking.Tiers[1].MinScore != 1000 {
			t.Errorf("tiers = %+v, want 青铜(0) 白银(1000)", cfg.Ranking.Tiers)
		}
	})

	t.Run("empty tier name rejected", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		content := base + "ranking:\n  tiers:\n    - name: \"\"\n      min_score: 0\n"
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(configPath); err == nil {
			t.Fatal("空 tier 名称应报错")
		}
	})

	t.Run("negative min_score rejected", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		content := base + "ranking:\n  tiers:\n    - name: \"青铜\"\n      min_score: -1\n"
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(configPath); err == nil {
			t.Fatal("负分下限应报错")
		}
	})
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

// TestLoad_Migrates190To1100 覆盖 1.9.0 → 1.10.0：旧配置缺
// assignment_ttl_seconds 时注入默认 1800；显式值保留；非法值拒绝。
func TestLoad_Migrates190To1100(t *testing.T) {
	base := `version: "1.9.0"
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
	// 缺 assignment_ttl_seconds → 迁移注入默认 1800。
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(path, []byte(base), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("1.9.0 配置应能迁移加载: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("版本号不匹配: 期望 %s, 实际 %s", CurrentVersion, cfg.Version)
	}
	if cfg.Matchmaking.AssignmentTTLSeconds != 1800 {
		t.Errorf("assignment_ttl_seconds = %d, want 1800（迁移默认值）", cfg.Matchmaking.AssignmentTTLSeconds)
	}

	// 显式值保留。
	explicit := filepath.Join(tmpDir, "explicit.yaml")
	if err := os.WriteFile(explicit, []byte(base+"  assignment_ttl_seconds: 900\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(explicit)
	if err != nil {
		t.Fatalf("显式 assignment_ttl_seconds 应能加载: %v", err)
	}
	if cfg2.Matchmaking.AssignmentTTLSeconds != 900 {
		t.Errorf("assignment_ttl_seconds = %d, want 900（显式值）", cfg2.Matchmaking.AssignmentTTLSeconds)
	}

	// 非法值（0）拒绝。
	invalid := filepath.Join(tmpDir, "invalid.yaml")
	if err := os.WriteFile(invalid, []byte(base+"  assignment_ttl_seconds: 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(invalid); err == nil {
		t.Fatal("assignment_ttl_seconds = 0 应拒绝加载")
	}
}

// TestLoad_Migrates1100To1110 覆盖 1.10.0 → 1.11.0：缺
// room_reservation_ttl_seconds 注入默认 900；servers 缺省保持不启用。
func TestLoad_Migrates1100To1110(t *testing.T) {
	base := `version: "1.10.0"
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
matchmaking:
  enabled: true
  tick_seconds: 1
  queue_timeout_seconds: 300
  base_rating_window: 200
  rating_window_growth_per_minute: 100
  max_rating_window: 1000
  assignment_ttl_seconds: 1800
  modes:
    - match_type: "1v1"
      team_sizes: [1, 1]
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(path, []byte(base), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("1.10.0 配置应能迁移加载: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("版本号不匹配: 期望 %s, 实际 %s", CurrentVersion, cfg.Version)
	}
	if cfg.Matchmaking.RoomReservationTTLSeconds != 900 {
		t.Errorf("room_reservation_ttl_seconds = %d, want 900（迁移默认值）",
			cfg.Matchmaking.RoomReservationTTLSeconds)
	}
	if cfg.Matchmaking.RoomInventoryEnabled() {
		t.Error("servers 缺省应不启用房间层")
	}
}

// TestLoad_RoomInventoryValidation 覆盖房间清单的启动校验：
// 合法清单通过；空清单/重复子服/重复房间/空房间列表/非法地址一律拒绝。
func TestLoad_RoomInventoryValidation(t *testing.T) {
	base := `version: "1.11.0"
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
matchmaking:
  enabled: true
  tick_seconds: 1
  queue_timeout_seconds: 300
  base_rating_window: 200
  rating_window_growth_per_minute: 100
  max_rating_window: 1000
  assignment_ttl_seconds: 1800
  room_reservation_ttl_seconds: 900
  modes:
    - match_type: "1v1"
      team_sizes: [1, 1]
`
	load := func(t *testing.T, serversYaml string) (*AppConfig, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(base+serversYaml), 0644); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}

	t.Run("valid inventory accepted", func(t *testing.T) {
		cfg, err := load(t, `  servers:
    - name: "sub-1"
      host: "10.0.0.1"
      port: 25501
      rooms:
        - name: "room-a"
          capacity: 2
        - name: "room-b"
          capacity: 4
`)
		if err != nil {
			t.Fatalf("合法清单应能加载: %v", err)
		}
		if !cfg.Matchmaking.RoomInventoryEnabled() || len(cfg.Matchmaking.Servers) != 1 {
			t.Fatalf("清单解析异常: %+v", cfg.Matchmaking.Servers)
		}
		if len(cfg.Matchmaking.Servers[0].Rooms) != 2 {
			t.Fatalf("房间解析异常: %+v", cfg.Matchmaking.Servers[0].Rooms)
		}
	})

	t.Run("rejections", func(t *testing.T) {
		cases := map[string]string{
			"显式空清单": `  servers: []
`,
			"子服名为空": `  servers:
    - name: ""
      host: "10.0.0.1"
      port: 25501
      rooms: [{name: "r", capacity: 2}]
`,
			"子服名重复": `  servers:
    - name: "sub-1"
      host: "10.0.0.1"
      port: 25501
      rooms: [{name: "r", capacity: 2}]
    - name: "sub-1"
      host: "10.0.0.2"
      port: 25501
      rooms: [{name: "r", capacity: 2}]
`,
			"地址为空": `  servers:
    - name: "sub-1"
      host: ""
      port: 25501
      rooms: [{name: "r", capacity: 2}]
`,
			"端口非法": `  servers:
    - name: "sub-1"
      host: "10.0.0.1"
      port: 70000
      rooms: [{name: "r", capacity: 2}]
`,
			"房间列表为空": `  servers:
    - name: "sub-1"
      host: "10.0.0.1"
      port: 25501
      rooms: []
`,
			"房间名重复": `  servers:
    - name: "sub-1"
      host: "10.0.0.1"
      port: 25501
      rooms:
        - {name: "r", capacity: 2}
        - {name: "r", capacity: 4}
`,
			"容量非正": `  servers:
    - name: "sub-1"
      host: "10.0.0.1"
      port: 25501
      rooms: [{name: "r", capacity: 0}]
`,
		}
		for name, yaml := range cases {
			if _, err := load(t, yaml); err == nil {
				t.Errorf("%s: 应拒绝启动", name)
			}
		}
	})
}
