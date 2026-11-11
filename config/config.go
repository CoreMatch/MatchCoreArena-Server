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
	"crypto/rand"
	"encoding/hex"
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
const CurrentVersion = "1.12.0"

// AppConfig 应用配置根结构。
type AppConfig struct {
	Version     string            `yaml:"version"`
	Server      ServerConfig      `yaml:"server"`
	Database    DatabaseConfig    `yaml:"database"`
	Redis       RedisConfig       `yaml:"redis"`
	Auth        AuthConfig        `yaml:"auth"`
	Matchmaking MatchmakingConfig `yaml:"matchmaking"`
	Ranking     RankingConfig     `yaml:"ranking"`
}

// RankingConfig 排行榜展示配置（纯展示，不参与结算、不引入赛季）。
type RankingConfig struct {
	// Tiers 段位阶梯（名称 + 分数下限），仅供前端展示进度条；
	// 为空时前端不显示进度条。
	Tiers []RankTier `yaml:"tiers"`
}

// RankTier 单个段位：名称与分数下限。
type RankTier struct {
	Name     string `yaml:"name" json:"name"`
	MinScore int    `yaml:"min_score" json:"min_score"`
}

// MatchmakingConfig 匹配系统配置。
// 引擎以 TickSeconds 为周期扫描 matchmaking_queue，按分差窗口贪心成局；
// 窗口随等待时长线性放宽（BaseRatingWindow + 每分钟增长，封顶 MaxRatingWindow），
// 超过 QueueTimeoutSeconds 未成局的记录被移除。
//
// 房间层（契约 1.10.0）：Servers 为子服与房间的静态清单。配置后成局
// 即预占一个房间（逻辑占位 + TTL），分配响应携带"子服地址 + 房间"；
// 清单缺省（未配置）时保持 1.9.x 行为：成局不预占房间、无 room 字段。
type MatchmakingConfig struct {
	Enabled                     bool             `yaml:"enabled"`
	TickSeconds                 int              `yaml:"tick_seconds"`
	QueueTimeoutSeconds         int              `yaml:"queue_timeout_seconds"`
	BaseRatingWindow            int              `yaml:"base_rating_window"`
	RatingWindowGrowthPerMinute int              `yaml:"rating_window_growth_per_minute"`
	MaxRatingWindow             int              `yaml:"max_rating_window"`
	// AssignmentTTLSeconds 成局分配有效期（秒，契约 1.9.0 的代理路由端点
	// GET /api/matchmaking/assignment/{uuid} 用）：formed_at 距今超过该值
	// 的成局不再视为有效分配。
	AssignmentTTLSeconds int `yaml:"assignment_ttl_seconds"`
	// RoomReservationTTLSeconds 房间预占有效期（秒，契约 1.10.0）：
	// 成局预占的房间超过该值未被结算消费即释放（引擎每轮清理）。
	RoomReservationTTLSeconds int `yaml:"room_reservation_ttl_seconds"`
	// PartyInviteTTLSeconds 组队邀请有效期（秒，契约 1.11.0 同意机制）：
	// 邀请自创建起超过该值未接受即视为过期（读取/接受/入队时惰性判定，
	// 无后台清扫）。未配置（0）时按默认 300 秒。
	PartyInviteTTLSeconds int `yaml:"party_invite_ttl_seconds"`
	// Servers 子服→房间静态清单。nil（未配置）= 不启用房间层，
	// 显式空列表视为非法配置拒绝启动。
	Servers    []MatchServer      `yaml:"servers,omitempty"`
	Modes      []MatchMode        `yaml:"modes"`
	JoinTarget JoinTargetConfig   `yaml:"join_target"`
}

// MatchServer 房间清单中的一个子服：名称、地址与其名下房间列表。
// 房间名属于该子服的命名空间（即子服侧自己的 arena/游戏名）。
type MatchServer struct {
	Name  string     `yaml:"name"`
	Host  string     `yaml:"host"`
	Port  int        `yaml:"port"`
	Rooms []RoomSpec `yaml:"rooms"`
}

// RoomSpec 单个房间：名称 + 容量（可容纳的总人数）。
type RoomSpec struct {
	Name     string `yaml:"name"`
	Capacity int    `yaml:"capacity"`
}

// RoomInventoryEnabled 是否启用房间层（清单非空）。
func (c MatchmakingConfig) RoomInventoryEnabled() bool {
	return len(c.Servers) > 0
}

// FindServer 按名称查找清单中的子服。
func (c MatchmakingConfig) FindServer(name string) (MatchServer, bool) {
	for _, s := range c.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return MatchServer{}, false
}

// JoinTargetConfig 成局后下发给客户端的游戏服务器地址（交接链路用）。
// 可空：host 为空视为未配置，成局响应不返回该字段，客户端回落本地配置。
type JoinTargetConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Configured 判断游戏服务器地址是否已配置（host 非空即启用）。
func (c JoinTargetConfig) Configured() bool {
	return c.Host != ""
}

// MatchMode 描述一种可排队的对战模式：各队人数。
// 例如 1v1 => [1, 1]，2v2 => [2, 2]。
type MatchMode struct {
	MatchType string `yaml:"match_type"`
	TeamSizes []int  `yaml:"team_sizes"`
}

// TotalPlayers 返回该模式成局所需总人数。
func (m MatchMode) TotalPlayers() int {
	total := 0
	for _, n := range m.TeamSizes {
		total += n
	}
	return total
}

// FindMode 按 match_type 查找模式；未配置返回 false。
func (c MatchmakingConfig) FindMode(matchType string) (MatchMode, bool) {
	for _, m := range c.Modes {
		if m.MatchType == matchType {
			return m, true
		}
	}
	return MatchMode{}, false
}

// AuthConfig 认证配置。
type AuthConfig struct {
	HRPAuth          HRPAuthConfig `yaml:"hrpauth"`
	MaintenanceToken string        `yaml:"maintenance_token"` // 运维超级凭据，拥有所有权限
	// TokensPath 高权限服务凭据文件 (tokens.yaml) 路径。
	// 留空时默认 "config/tokens.yaml"（相对工作目录，保持历史行为）；
	// 可用环境变量 MCA_TOKENS_PATH 覆盖（优先级最高）。
	TokensPath string `yaml:"tokens_path"`
}

// HRPAuthConfig HRPAuth IdP 配置。
type HRPAuthConfig struct {
	BaseURL        string `yaml:"base_url"`         // HRPAuth 根地址，如 http://localhost:2778
	PublicClientID string `yaml:"public_client_id"` // OAuth2 public client_id，用于 refresh_token 等第一方流程
	// ServiceClientID / ServiceClientSecret 是 ops 级服务凭据（client_credentials），
	// 用于调用 HRPAuth 服务间接点（如 GET /services/users/{uuid} 解析参与者身份）。
	// 部署时注入 HRPAuth 内置 super client 的凭据（与 Yggdrasil-API 的
	// core_api.client_id/secret 同源）。留空时服务调用会以类型化错误失败。
	ServiceClientID     string `yaml:"service_client_id"`
	ServiceClientSecret string `yaml:"service_client_secret"`
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
			// 添加 maintenance_token，如果不存在则生成随机值
			if _, ok := authRaw["maintenance_token"]; !ok {
				authRaw["maintenance_token"] = generateRandomToken(32)
			}
			in["auth"] = authRaw
			return in, nil
		},
	},
	{
		from: "1.4.0",
		to:   "1.5.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 新增 ops 级服务凭据字段（client_credentials），默认空值：
			// 留空时服务调用以类型化错误失败，不阻塞启动。
			authRaw, _ := in["auth"].(map[string]any)
			if authRaw == nil {
				authRaw = map[string]any{}
			}
			hrpauthRaw, _ := authRaw["hrpauth"].(map[string]any)
			if hrpauthRaw == nil {
				hrpauthRaw = map[string]any{}
			}
			if _, ok := hrpauthRaw["service_client_id"]; !ok {
				hrpauthRaw["service_client_id"] = ""
			}
			if _, ok := hrpauthRaw["service_client_secret"]; !ok {
				hrpauthRaw["service_client_secret"] = ""
			}
			authRaw["hrpauth"] = hrpauthRaw
			in["auth"] = authRaw
			return in, nil
		},
	},
	{
		from: "1.5.0",
		to:   "1.6.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 新增虚拟对局开关与匹配系统配置块，默认启用、参数取保守默认值。
			if _, ok := in["virtual_matches"]; !ok {
				in["virtual_matches"] = map[string]any{"enabled": true}
			}
			if _, ok := in["matchmaking"]; !ok {
				in["matchmaking"] = map[string]any{
					"enabled":                          true,
					"tick_seconds":                     1,
					"queue_timeout_seconds":            300,
					"base_rating_window":               200,
					"rating_window_growth_per_minute":  100,
					"max_rating_window":                1000,
					"modes": []any{
						map[string]any{"match_type": "1v1", "team_sizes": []any{1, 1}},
						map[string]any{"match_type": "2v2", "team_sizes": []any{2, 2}},
					},
				}
			}
			return in, nil
		},
	},
	{
		from: "1.6.0",
		to:   "1.7.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 对局上报收敛为单一入口 POST /api/matches，
			// 移除虚拟对局独立开关。
			delete(in, "virtual_matches")
			return in, nil
		},
	},
	{
		from: "1.7.0",
		to:   "1.8.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 新增 matchmaking.join_target（游戏服务器地址，可空）。
			// 缺省即"未配置"：成局响应不下发，客户端回落本地配置，
			// 无需向旧配置写入任何默认值。
			return in, nil
		},
	},
	{
		from: "1.8.0",
		to:   "1.9.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 新增 ranking.tiers（段位阶梯，纯展示，可空）。
			// 缺省即"无阶梯"：前端不显示进度条，无需写入默认值。
			return in, nil
		},
	},
	{
		from: "1.9.0",
		to:   "1.10.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 新增 matchmaking.assignment_ttl_seconds（成局分配有效期，
			// 代理路由端点用）。旧配置缺省时注入保守默认值 1800s。
			if mm, ok := in["matchmaking"].(map[string]any); ok {
				if _, ok := mm["assignment_ttl_seconds"]; !ok {
					mm["assignment_ttl_seconds"] = 1800
				}
			}
			return in, nil
		},
	},
	{
		from: "1.10.0",
		to:   "1.11.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 新增 matchmaking.room_reservation_ttl_seconds（房间预占
			// TTL，契约 1.10.0 房间层）。缺省注入默认值 900s。
			// matchmaking.servers（子服→房间静态清单）缺省即"未配置"：
			// 房间层不启用，保持 1.9.x 行为，无需写入默认值。
			if mm, ok := in["matchmaking"].(map[string]any); ok {
				if _, ok := mm["room_reservation_ttl_seconds"]; !ok {
					mm["room_reservation_ttl_seconds"] = 900
				}
			}
			return in, nil
		},
	},
	{
		from: "1.11.0",
		to:   "1.12.0",
		run: func(in rawConfig) (rawConfig, error) {
			// 新增 matchmaking.party_invite_ttl_seconds（组队邀请有效期，
			// 契约 1.11.0 同意机制）。缺省注入默认值 300s。
			if mm, ok := in["matchmaking"].(map[string]any); ok {
				if _, ok := mm["party_invite_ttl_seconds"]; !ok {
					mm["party_invite_ttl_seconds"] = 300
				}
			}
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
	// Matchmaking 校验（1.6.0+）
	// 邀请 TTL（1.11.0）：旧配置无此键，缺省按 300 秒，不拒绝启动。
	if cfg.Matchmaking.PartyInviteTTLSeconds == 0 {
		cfg.Matchmaking.PartyInviteTTLSeconds = 300
	}
	mm := cfg.Matchmaking
	if mm.PartyInviteTTLSeconds < 0 {
		return errors.New("matchmaking.party_invite_ttl_seconds 不能为负数")
	}
	if mm.TickSeconds <= 0 {
		return errors.New("matchmaking.tick_seconds 必须为正数")
	}
	if mm.QueueTimeoutSeconds <= 0 {
		return errors.New("matchmaking.queue_timeout_seconds 必须为正数")
	}
	if mm.BaseRatingWindow <= 0 || mm.MaxRatingWindow <= 0 {
		return errors.New("matchmaking.base_rating_window 与 max_rating_window 必须为正数")
	}
	if mm.RatingWindowGrowthPerMinute < 0 {
		return errors.New("matchmaking.rating_window_growth_per_minute 不能为负数")
	}
	if mm.AssignmentTTLSeconds <= 0 {
		return errors.New("matchmaking.assignment_ttl_seconds 必须为正数")
	}
	if mm.RoomReservationTTLSeconds <= 0 {
		return errors.New("matchmaking.room_reservation_ttl_seconds 必须为正数")
	}
	// 房间清单校验（1.11.0 / 契约 1.10.0）：缺省 = 不启用房间层；
	// 显式配置则逐项强校验，任一项非法直接拒绝启动。
	if mm.Servers != nil && len(mm.Servers) == 0 {
		return errors.New("matchmaking.servers 显式为空清单：要么整体不配置（不启用房间层），要么至少给出一个子服")
	}
	seenServer := make(map[string]bool, len(mm.Servers))
	for i, srv := range mm.Servers {
		if srv.Name == "" {
			return fmt.Errorf("matchmaking.servers[%d].name 必填", i)
		}
		if seenServer[srv.Name] {
			return fmt.Errorf("matchmaking.servers 中子服名重复: %s", srv.Name)
		}
		seenServer[srv.Name] = true
		if srv.Host == "" {
			return fmt.Errorf("matchmaking.servers[%s].host 必填（地址非法）", srv.Name)
		}
		if srv.Port <= 0 || srv.Port > 65535 {
			return fmt.Errorf("matchmaking.servers[%s].port 非法: %d（必须在 1-65535）", srv.Name, srv.Port)
		}
		if len(srv.Rooms) == 0 {
			return fmt.Errorf("matchmaking.servers[%s].rooms 为空清单：每个子服必须至少一个房间", srv.Name)
		}
		seenRoom := make(map[string]bool, len(srv.Rooms))
		for j, room := range srv.Rooms {
			if room.Name == "" {
				return fmt.Errorf("matchmaking.servers[%s].rooms[%d].name 必填", srv.Name, j)
			}
			if seenRoom[room.Name] {
				return fmt.Errorf("matchmaking.servers[%s] 中房间名重复: %s", srv.Name, room.Name)
			}
			seenRoom[room.Name] = true
			if room.Capacity <= 0 {
				return fmt.Errorf("matchmaking.servers[%s].rooms[%s].capacity 必须为正数", srv.Name, room.Name)
			}
		}
	}
	seen := make(map[string]bool, len(mm.Modes))
	for _, mode := range mm.Modes {
		if mode.MatchType == "" {
			return errors.New("matchmaking.modes 中存在空 match_type")
		}
		if seen[mode.MatchType] {
			return fmt.Errorf("matchmaking.modes 中 match_type 重复: %s", mode.MatchType)
		}
		seen[mode.MatchType] = true
		if len(mode.TeamSizes) == 0 {
			return fmt.Errorf("matchmaking.modes[%s].team_sizes 不能为空", mode.MatchType)
		}
		for _, n := range mode.TeamSizes {
			if n <= 0 {
				return fmt.Errorf("matchmaking.modes[%s].team_sizes 必须全部为正数", mode.MatchType)
			}
		}
	}
	// join_target 校验（1.8.0+）：host 为空视为未配置；配置了 host 则
	// port 必须为合法端口。
	if mm.JoinTarget.Configured() && (mm.JoinTarget.Port <= 0 || mm.JoinTarget.Port > 65535) {
		return fmt.Errorf("matchmaking.join_target.port 非法: %d（配置 host 时端口必须在 1-65535）", mm.JoinTarget.Port)
	}
	// ranking.tiers 校验（1.9.0+）：纯展示配置，可为空；配置则名称必填、
	// 分数下限非负。
	for i, tier := range cfg.Ranking.Tiers {
		if tier.Name == "" {
			return fmt.Errorf("ranking.tiers[%d].name 必填", i)
		}
		if tier.MinScore < 0 {
			return fmt.Errorf("ranking.tiers[%d].min_score 不能为负数", i)
		}
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
				BaseURL:        "http://localhost:2778",
				PublicClientID: "hrpauth-webui",
			},
			MaintenanceToken: generateRandomToken(32),
		},
		Matchmaking: MatchmakingConfig{
			Enabled:                     true,
			TickSeconds:                 1,
			QueueTimeoutSeconds:         300,
			BaseRatingWindow:            200,
			RatingWindowGrowthPerMinute: 100,
			MaxRatingWindow:             1000,
			AssignmentTTLSeconds:        1800,
			RoomReservationTTLSeconds:   900,
			PartyInviteTTLSeconds:       300,
			Modes: []MatchMode{
				{MatchType: "1v1", TeamSizes: []int{1, 1}},
				{MatchType: "2v2", TeamSizes: []int{2, 2}},
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

// generateRandomToken 生成指定字节长度的随机 16 进制字符串。
func generateRandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
