// Package config 提供应用启动配置文件的版本化加载、严格校验与自动迁移。
//
// 设计要点：
//   - 使用 YAML 作为配置文件格式（默认路径 config/config.yaml，可由 ENV CONFIG_PATH 覆盖）。
//   - 配置文件顶层必须包含 version 字段，遵循 SemVer（语义化版本）。
//   - 加载时严格按照【当前代码内置的最新版本】进行强校验：
//   - 版本号一致：直接使用。
//   - 版本号落后：按顺序应用内置迁移规则升级到当前版本。
//   - 版本号超前：返回错误，避免未知字段。
//   - 缺失或非法 version：返回错误，不做宽松降级。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// CurrentVersion 是当前代码内置支持的最新配置版本。
// 任何升级配置结构时，必须同步：
//  1. 将 CurrentVersion 提升到新版本；
//  2. 在 migrations 列表中追加对应的迁移函数；
//  3. 在 config.example.yaml 中同步更新。
const CurrentVersion = "1.4.0"

// AppConfig 应用配置根结构。
type AppConfig struct {
	Version  string         `yaml:"version"`
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	Auth     AuthConfig     `yaml:"auth"`
}

// AuthConfig 认证配置。
type AuthConfig struct {
	HRPAuth          HRPAuthConfig `yaml:"hrpauth"`
	MaintenanceToken string        `yaml:"maintenance_token"` // 运维超级凭据，拥有所有权限
}

// HRPAuthConfig HRPAuth IdP 配置。
type HRPAuthConfig struct {
	BaseURL        string `yaml:"base_url"`         // HRPAuth 根地址，如 http://localhost:8080
	PublicClientID string `yaml:"public_client_id"` // OAuth2 public client_id，用于 refresh_token 等第一方流程
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	Host                string `yaml:"host"`
	Port                int    `yaml:"port"`
	ReadTimeoutSeconds  int    `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `yaml:"write_timeout_seconds"`
}

// DatabaseConfig 数据库连接配置。
type DatabaseConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	User         string `yaml:"user"`
	Password     string `yaml:"password"`
	Name         string `yaml:"name"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

// RedisConfig Redis 连接配置。
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// rawConfig 用于在迁移前灵活读取未知字段。
type rawConfig map[string]any

// migration 单个版本迁移函数：从 in（上一版本的结构）就地升级为下一版本。
type migration struct {
	from string
	to   string
	run  func(in rawConfig) (rawConfig, error)
}

// migrations 按版本顺序注册的迁移链。每一项描述 from -> to 的变更。
// 添加新版本时，从前一个版本依次 append 即可。
var migrations = []migration{
	{
		from: "1.0.0",
		to:   "1.1.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 新增 auth 块（空默认值），由 validate 在配置文件层面要求必填。
			if _, ok := in["auth"]; !ok {
				in["auth"] = map[string]any{
					"hrpauth": map[string]any{
						"issuer":                 "",
						"authorization_endpoint": "",
						"token_endpoint":         "",
						"userinfo_endpoint":      "",
						"revocation_endpoint":    "",
						"client_id":              "",
						"client_secret":          "",
						"redirect_uri":           "",
						"scopes":                 []any{"openid", "profile"},
						"cookie_encryption_key":  "",
					},
				}
			}
			return in, nil
		},
	},
	{
		from: "1.1.0",
		to:   "1.2.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 重构 auth.hrpauth：移除 OIDC 端点字段，改为 base_url 单字段。
			// 从旧 issuer 字段提取 base_url（去掉尾部斜杠和 /oauth2 路径）。
			authRaw, _ := in["auth"].(map[string]any)
			if authRaw == nil {
				authRaw = map[string]any{}
			}
			hrpauthRaw, _ := authRaw["hrpauth"].(map[string]any)
			if hrpauthRaw == nil {
				hrpauthRaw = map[string]any{}
			}

			// 从 issuer 字段提取 base_url
			baseURL := ""
			if issuer, ok := hrpauthRaw["issuer"].(string); ok && issuer != "" {
				baseURL = strings.TrimRight(issuer, "/")
				// 移除常见的路径后缀
				baseURL = strings.TrimSuffix(baseURL, "/oauth2")
				baseURL = strings.TrimSuffix(baseURL, "/.well-known/openid-configuration")
			}

			authRaw["hrpauth"] = map[string]any{
				"base_url": baseURL,
			}
			in["auth"] = authRaw
			return in, nil
		},
	},
	{
		from: "1.2.0",
		to:   "1.3.0",
		run: func(in rawConfig) (rawConfig, error) {
			authRaw, _ := in["auth"].(map[string]any)
			if authRaw == nil {
				authRaw = map[string]any{}
			}
			hrpauthRaw, _ := authRaw["hrpauth"].(map[string]any)
			if hrpauthRaw == nil {
				hrpauthRaw = map[string]any{}
			}
			// 添加 public_client_id，默认值与 HRPAuth 配置对齐
			if _, ok := hrpauthRaw["public_client_id"]; !ok {
				hrpauthRaw["public_client_id"] = "hrpauth-webui"
			}
			authRaw["hrpauth"] = hrpauthRaw
			in["auth"] = authRaw
			return in, nil
		},
	},
	{
		from: "1.3.0",
		to:   "1.4.0",
		run: func(in rawConfig) (rawConfig, error) {
			authRaw, _ := in["auth"].(map[string]any)
			if authRaw == nil {
				authRaw = map[string]any{}
			}
			// 添加 maintenance_token，默认空
			if _, ok := authRaw["maintenance_token"]; !ok {
				authRaw["maintenance_token"] = ""
			}
			in["auth"] = authRaw
			return in, nil
		},
	},
}

// Load 从指定路径加载配置文件，并完成版本校验/迁移，最终反序列化为 AppConfig。
// 当 path 为空时使用 ENV CONFIG_PATH 或默认值 "config/config.yaml"。
// 如果配置文件不存在，将自动创建默认配置文件。
func Load(path string) (*AppConfig, error) {
	if path == "" {
		path = getEnv("CONFIG_PATH", "config/config.yaml")
	}

	rawBytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 配置文件不存在，创建默认配置文件
			if err := createDefaultConfig(path); err != nil {
				return nil, fmt.Errorf("创建默认配置文件失败 (%s): %w", path, err)
			}
			// 重新读取刚创建的配置文件
			rawBytes, err = os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("读取新创建的配置文件失败 (%s): %w", path, err)
			}
		} else {
			return nil, fmt.Errorf("读取配置文件失败 (%s): %w", path, err)
		}
	}

	// 第一次解析为通用 map，用于执行迁移。
	var interim rawConfig
	if err := yaml.Unmarshal(rawBytes, &interim); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if interim == nil {
		return nil, errors.New("配置文件为空或格式非法")
	}

	migrated, err := applyMigrations(interim)
	if err != nil {
		return nil, err
	}

	// 迁移后二次校验版本号。
	gotVersion, _ := migrated["version"].(string)
	if gotVersion != CurrentVersion {
		return nil, fmt.Errorf("迁移后版本仍为 %s，期望 %s", gotVersion, CurrentVersion)
	}

	// 重新序列化为 YAML 后再反序列化为强类型结构，便于发现未知字段。
	finalBytes, err := yaml.Marshal(migrated)
	if err != nil {
		return nil, fmt.Errorf("重新序列化配置失败: %w", err)
	}

	cfg := &AppConfig{}
	if err := yaml.Unmarshal(finalBytes, cfg); err != nil {
		return nil, fmt.Errorf("强类型反序列化失败: %w", err)
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyMigrations 将任意版本（<= CurrentVersion）逐步升级到 CurrentVersion。
func applyMigrations(in rawConfig) (rawConfig, error) {
	curVer, _ := in["version"].(string)
	if curVer == "" {
		return nil, errors.New("配置文件缺少 version 字段")
	}
	if !isValidSemVer(curVer) {
		return nil, fmt.Errorf("配置文件 version 非法: %s", curVer)
	}

	// 不允许超前版本，避免引入未知字段。
	if compareSemVer(curVer, CurrentVersion) > 0 {
		return nil, fmt.Errorf("配置文件 version (%s) 高于当前支持的版本 (%s)，请升级程序", curVer, CurrentVersion)
	}

	// 已匹配则原样返回（仍会进行字段强校验）。
	if curVer == CurrentVersion {
		return in, nil
	}

	// 沿迁移链逐步升级。
	for _, m := range migrations {
		if compareSemVer(curVer, m.from) < 0 {
			return nil, fmt.Errorf("找不到从 %s 到 %s 的迁移路径", curVer, m.to)
		}
		if curVer == m.from {
			next, err := m.run(in)
			if err != nil {
				return nil, fmt.Errorf("执行迁移 %s -> %s 失败: %w", m.from, m.to, err)
			}
			next["version"] = m.to
			in = next
			curVer = m.to
			if curVer == CurrentVersion {
				return in, nil
			}
		}
	}

	return nil, fmt.Errorf("未能将配置从 %s 迁移到 %s：迁移链不完整", curVer, CurrentVersion)
}

// validate 对解析后的强类型配置进行最终业务校验。
func validate(cfg *AppConfig) error {
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port 非法: %d", cfg.Server.Port)
	}
	if cfg.Database.Host == "" || cfg.Database.Name == "" {
		return errors.New("database.host 与 database.name 必填")
	}
	if cfg.Database.MaxOpenConns < 0 || cfg.Database.MaxIdleConns < 0 {
		return errors.New("database 连接池参数不能为负数")
	}
	// Auth 校验（1.2.0+）
	if cfg.Auth.HRPAuth.BaseURL == "" {
		return errors.New("auth.hrpauth.base_url 必填")
	}
	return nil
}

// getEnv 读取环境变量，提供默认值。
func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// createDefaultConfig 创建默认配置文件。
// 使用内置的默认值生成配置文件，确保目录存在。
func createDefaultConfig(path string) error {
	// 确保目录存在
	dir := "."
	if idx := strings.LastIndex(path, "/"); idx > 0 {
		dir = path[:idx]
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	defaultConfig := &AppConfig{
		Version: CurrentVersion,
		Server: ServerConfig{
			Host:                "0.0.0.0",
			Port:                9178,
			ReadTimeoutSeconds:  15,
			WriteTimeoutSeconds: 15,
		},
		Database: DatabaseConfig{
			Host:         "localhost",
			Port:         3306,
			User:         "root",
			Password:     "",
			Name:         "matchcorearena",
			MaxOpenConns: 50,
			MaxIdleConns: 10,
		},
		Redis: RedisConfig{
			Host:     "localhost",
			Port:     6379,
			Password: "",
			DB:       0,
		},
		Auth: AuthConfig{
			HRPAuth: HRPAuthConfig{
				BaseURL:        "http://localhost:8080",
				PublicClientID: "hrpauth-webui",
			},
		},
	}

	data, err := yaml.Marshal(defaultConfig)
	if err != nil {
		return fmt.Errorf("序列化默认配置失败: %w", err)
	}

	// 添加 YAML 文件头注释
	content := "# MatchCoreArena-Server 应用配置文件\n" +
		"# 此文件由程序自动生成，请按需修改配置。\n" +
		"# 版本变更由 config 包自动迁移。\n\n" +
		string(data)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return nil
}

// isValidSemVer 校验形如 "x.y.z" 的语义化版本字符串。
func isValidSemVer(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return false
		}
	}
	return true
}

// compareSemVer 比较两个 SemVer：a > b 返回 1；a == b 返回 0；a < b 返回 -1。
// 任一参数非法时返回 -2，由调用方决定如何处理。
func compareSemVer(a, b string) int {
	if !isValidSemVer(a) || !isValidSemVer(b) {
		return -2
	}
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		ai, _ := strconv.Atoi(pa[i])
		bi, _ := strconv.Atoi(pb[i])
		if ai > bi {
			return 1
		}
		if ai < bi {
			return -1
		}
	}
	return 0
}
