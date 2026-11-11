//go:build e2e

// Package e2e_test 是不依赖真实游戏的端到端验证：真实 MariaDB +
// 完整路由/中间件/结算事务 + 内嵌 HRPAuth stub。
//
// 对局上报只有一个入口 POST /api/matches（仅 OPS 级凭据）；"虚拟对局"
// 即运维方经该端点提交的 source=virtual 假数据，服务端不区分结算。
//
// 运行方式（需本地可用的 MySQL/MariaDB）：
//
//	MCA_E2E_MYSQL_DSN='mca:mca-e2e-pass@tcp(127.0.0.1:13306)/mca_e2e?parseTime=true&charset=utf8mb4' \
//	go test -tags e2e -race -count=1 ./test/e2e/
//
// 测试会清空并重建 mca_e2e 库中的全部表，请勿指向有数据的库。
package e2e_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api"
	"MatchCoreArena-Server/config"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/service"
	"MatchCoreArena-Server/migrate"
)

const (
	reporterToken    = "e2e-report-token"
	maintenanceToken = "e2e-maintenance-token"
	e2eDSNDefault    = "mca:mca-e2e-pass@tcp(127.0.0.1:13306)/mca_e2e?parseTime=true&charset=utf8mb4"
)

// uuid→uid 映射（HRPAuth stub 解析用）。
// 101/102 评分结算与批量；103/104 平局与兼容形状；105/106 单人匹配
// 与 game_id 幂等；107-110 组队匹配；111-114 房间预占竞争
//（全部 0 分起步，分差在基础窗口内）。
var e2eUsers = map[string]int64{
	"e2e-uuid-1":  101,
	"e2e-uuid-2":  102,
	"e2e-uuid-3":  103,
	"e2e-uuid-4":  104,
	"e2e-uuid-5":  105,
	"e2e-uuid-6":  106,
	"e2e-uuid-7":  107,
	"e2e-uuid-8":  108,
	"e2e-uuid-9":  109,
	"e2e-uuid-10": 110,
	"e2e-uuid-11": 111,
	"e2e-uuid-12": 112,
	"e2e-uuid-13": 113,
	"e2e-uuid-14": 114,
	"e2e-uuid-15": 115,
	"e2e-uuid-16": 116,
	"e2e-uuid-17": 117,
	"e2e-uuid-18": 118,
}

// userToken 返回 uid 对应的玩家令牌（stub /user 识别）。
func userToken(uid int64) string { return fmt.Sprintf("user-token-%d", uid) }

// e2eEnv 端到端环境：真实数据库 + 完整路由 + HRPAuth stub + 匹配引擎。
type e2eEnv struct {
	server *httptest.Server
	db     *sql.DB
	cancel context.CancelFunc
}

// startStubHRPAuth 启动最小 HRPAuth：/status、/oauth/token（client_credentials）、
// /services/users/{uuid}（服务令牌鉴权）、/user（玩家令牌→uid）。
func startStubHRPAuth(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/status":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/oauth/token":
			if r.FormValue("grant_type") != "client_credentials" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"svc-token","expires_in":3600},"access_token":"svc-token","expires_in":3600}`))
		case strings.HasPrefix(r.URL.Path, "/services/users/"):
			if r.Header.Get("Authorization") != "Bearer svc-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			uuid := strings.TrimPrefix(r.URL.Path, "/services/users/")
			uid, ok := e2eUsers[uuid]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"success":true,"data":{"uid":%d,"uuid":%q}}`, uid, uuid)))
		case r.URL.Path == "/user":
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			var uid int64
			if _, err := fmt.Sscanf(token, "user-token-%d", &uid); err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"uid":%d,"email":"e2e-%d@example.com","username":"e2e-%d"}`, uid, uid, uid)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// resetDatabase 清空 mca_e2e 中所有表（baseline 仅在空库执行）。
func resetDatabase(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	if err != nil {
		t.Fatalf("查询表清单失败: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("解析表名失败: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		t.Fatalf("关闭外键检查失败: %v", err)
	}
	for _, tbl := range tables {
		if _, err := db.Exec("DROP TABLE IF EXISTS `" + tbl + "`"); err != nil {
			t.Fatalf("删除表 %s 失败: %v", tbl, err)
		}
	}
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		t.Fatalf("恢复外键检查失败: %v", err)
	}
}

// setupEnv 装配端到端环境。
func setupEnv(t *testing.T) *e2eEnv {
	t.Helper()

	dsn := os.Getenv("MCA_E2E_MYSQL_DSN")
	if dsn == "" {
		dsn = e2eDSNDefault
	}
	resetDatabase(t, dsn)

	// 执行内嵌 baseline 迁移（空库）。
	cfg := migrate.Config{DBHost: "127.0.0.1", DBPort: "13306", DBUser: "mca", DBPassword: "mca-e2e-pass", DBName: "mca_e2e"}
	if v := os.Getenv("MCA_E2E_DB_USER"); v != "" {
		cfg.DBUser = v
	}
	if v := os.Getenv("MCA_E2E_DB_PASSWORD"); v != "" {
		cfg.DBPassword = v
	}
	if err := migrate.RunMigrations(&cfg); err != nil {
		t.Fatalf("执行 baseline 迁移失败: %v", err)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("数据库 Ping 失败: %v", err)
	}

	stub := startStubHRPAuth(t)
	authClient, err := auth.NewClient(stub.URL, "e2e-public-client", "e2e-svc-id", "e2e-svc-secret")
	if err != nil {
		t.Fatalf("初始化 HRPAuth 客户端失败: %v", err)
	}

	// tokens.yaml：match-reporter 静态服务令牌（真实部署同构，OPS 级）。
	tokensPath := filepath.Join(t.TempDir(), "tokens.yaml")
	tokensContent := "tokens:\n  match-reporter:\n    token: " + reporterToken +
		"\n    uid: -100\n    scopes:\n      - \"match:report\"\n      - \"user:read\"\n"
	if err := os.WriteFile(tokensPath, []byte(tokensContent), 0644); err != nil {
		t.Fatalf("写入 tokens.yaml 失败: %v", err)
	}
	tokenStore, err := auth.LoadTokenStore(tokensPath)
	if err != nil {
		t.Fatalf("加载 tokens.yaml 失败: %v", err)
	}
	verifier := auth.NewTokenVerifier(authClient, nil, maintenanceToken, tokenStore)

	appCfg := config.AppConfig{
		Version: config.CurrentVersion,
		Matchmaking: config.MatchmakingConfig{
			Enabled:                     true,
			TickSeconds:                 1,
			QueueTimeoutSeconds:         300,
			BaseRatingWindow:            200,
			RatingWindowGrowthPerMinute: 100,
			MaxRatingWindow:             1000,
			Modes: []config.MatchMode{
				{MatchType: "1v1", TeamSizes: []int{1, 1}},
				{MatchType: "2v2", TeamSizes: []int{2, 2}},
			},
			AssignmentTTLSeconds: 1800,
			JoinTarget:           config.JoinTargetConfig{Host: "e2e-game.example.com", Port: 25565},
			RoomReservationTTLSeconds: 1800,
			// 邀请 TTL 取 2s：主流程毫秒内完成不受影响，过期路径
			// sleep 后即可验证。
			PartyInviteTTLSeconds: 2,
			Servers: []config.MatchServer{
				{Name: "e2e-sub-1", Host: "10.99.0.1", Port: 25501, Rooms: []config.RoomSpec{
					{Name: "e2e-room-2a", Capacity: 2},
					{Name: "e2e-room-2b", Capacity: 2},
					{Name: "e2e-room-4", Capacity: 4},
				}},
			},
		},
		Ranking: config.RankingConfig{
			Tiers: []config.RankTier{
				{Name: "青铜", MinScore: 0},
				{Name: "白银", MinScore: 300},
				{Name: "黄金", MinScore: 600},
			},
		},
		// duration 容差取缺省值 2 秒（契约 1.13.0）。
		Report: config.ReportConfig{DurationToleranceSeconds: 2},
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	api.Register(r, db, authClient, verifier, nil, "e2e", appCfg)
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)

	// 匹配引擎随测试上下文运行。
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	engine := service.NewMatchmakingEngine(db, appCfg.Matchmaking)
	engine.Start(ctx)

	return &e2eEnv{server: server, db: db, cancel: cancel}
}

// doJSON 发起带令牌的 JSON 请求并解析统一信封。
func doJSON(t *testing.T, env *e2eEnv, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, env.server.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", method, path, err)
	}
	defer resp.Body.Close()
	var envelope map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("解析响应失败 (%s %s): %v", method, path, err)
	}
	return resp.StatusCode, envelope
}

func dataMap(t *testing.T, envelope map[string]any) map[string]any {
	t.Helper()
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("data 不是对象: %v", envelope)
	}
	return data
}

func dataArray(t *testing.T, envelope map[string]any) []any {
	t.Helper()
	data, ok := envelope["data"].([]any)
	if !ok {
		t.Fatalf("data 不是数组: %v", envelope)
	}
	return data
}

// matchScoreColumns 直连读取 matches 主表的记分/表现四列（这四列不在
// API 响应中暴露，e2e 层直查数据库断言真实写入）。
func matchScoreColumns(t *testing.T, env *e2eEnv, matchID int64) (winnerScore, loserScore int, winnerPerf, loserPerf float64) {
	t.Helper()
	err := env.db.QueryRow(
		"SELECT winner_score, loser_score, winner_perf, loser_perf FROM matches WHERE id = ?", matchID,
	).Scan(&winnerScore, &loserScore, &winnerPerf, &loserPerf)
	if err != nil {
		t.Fatalf("读 matches 记分/表现列失败 (id=%d): %v", matchID, err)
	}
	return
}

// inviteAndAccept 完成一轮"邀请 + 接受"（契约 1.11.0 组队同意前置）：
// 邀请人向受邀人发邀请，受邀人接受，返回邀请 id。
func inviteAndAccept(t *testing.T, env *e2eEnv, matchType string, inviterToken, inviteeToken, inviteeUUID string) int64 {
	t.Helper()
	status, envelope := doJSON(t, env, http.MethodPost, "/api/matchmaking/invites", inviterToken,
		map[string]any{"match_type": matchType, "member_uuids": []string{inviteeUUID}})
	if status != http.StatusCreated {
		t.Fatalf("发邀请 status = %d, want 201 (body=%v)", status, envelope)
	}
	invites := dataArray(t, envelope)
	if len(invites) != 1 {
		t.Fatalf("邀请数 = %d, want 1", len(invites))
	}
	inviteID := jsonInt(t, invites[0].(map[string]any), "id")
	status, envelope = doJSON(t, env, http.MethodPost,
		fmt.Sprintf("/api/matchmaking/invites/%d/accept", inviteID), inviteeToken, nil)
	if status != http.StatusOK {
		t.Fatalf("接受邀请 status = %d, want 200 (body=%v)", status, envelope)
	}
	return inviteID
}

// matchReport 构造一局 1v1 上报（统一 teams 形状）。
func matchReport(winnerUUID, loserUUID string) map[string]any {
	return map[string]any{
		"match_type": "1v1",
		"teams": []map[string]any{
			{"team_id": "A", "result": "win", "initial_count": 1, "alive_count": 1,
				"members": []map[string]any{{"uuid": winnerUUID}}},
			{"team_id": "B", "result": "lose", "initial_count": 1, "alive_count": 1,
				"members": []map[string]any{{"uuid": loserUUID}}},
		},
		"duration_seconds": 600,
		"map_name":         "e2e-arena-1",
		"started_at":       time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339),
		"finished_at":      time.Now().UTC().Format(time.RFC3339),
	}
}

// participantsFrom 提取结算响应中的参与者列表（uid → 明细）。
// 上报响应的 data 即对局对象，内含 participants。
func participantsFrom(t *testing.T, envelope map[string]any) map[int64]map[string]any {
	t.Helper()
	data := dataMap(t, envelope)
	raw, ok := data["participants"].([]any)
	if !ok {
		t.Fatalf("participants 缺失: %v", data)
	}
	out := map[int64]map[string]any{}
	for _, item := range raw {
		p, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("participant 不是对象: %v", item)
		}
		uid := int64(p["uid"].(float64))
		out[uid] = p
	}
	return out
}

// getUserProfile 通过 GET /api/users/{uid} 读取用户档案。
func getUserProfile(t *testing.T, env *e2eEnv, uid int64) map[string]any {
	t.Helper()
	status, envelope := doJSON(t, env, http.MethodGet, fmt.Sprintf("/api/users/%d", uid), userToken(uid), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/users/%d status = %d, body=%v", uid, status, envelope)
	}
	return dataMap(t, envelope)
}

func jsonInt(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("字段 %s 缺失或不是数字: %v", key, m)
	}
	return int64(v)
}

// TestMatchCoreE2E 端到端：统一上报入口（OPS 门、幂等、矛盾拒绝）→
// 定级结算 → 排行榜 → 单人/组队匹配成局 → 队伍共享结算 → 历史与指标。
func TestMatchCoreE2E(t *testing.T) {
	env := setupEnv(t)

	t.Run("status endpoint", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodGet, "/api/status", "", nil)
		if status != http.StatusOK || envelope["success"] != true {
			t.Fatalf("status = %d, envelope=%v", status, envelope)
		}
	})

	// OPS 门：玩家令牌（无 match:report scope 且非 OPS）一律 403。
	t.Run("player token cannot report", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", userToken(101), matchReport("e2e-uuid-1", "e2e-uuid-2"))
		if status != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%v)", status, envelope)
		}
		if envelope["code"] != "oauth_insufficient_scope" {
			t.Fatalf("code = %v, want oauth_insufficient_scope", envelope["code"])
		}
	})

	// 第一局：101 胜 102。双方 0 分起步：胜者 +325（K=520, E=0.5,
	// 完胜权重 1.25），败者 -325（下限 0）。
	var match1ID int64
	t.Run("first match settles ratings", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, matchReport("e2e-uuid-1", "e2e-uuid-2"))
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%v)", status, envelope)
		}
		data := dataMap(t, envelope)
		match1ID = jsonInt(t, data, "id")
		if data["source"] != "game" || data["map_name"] != "e2e-arena-1" {
			t.Errorf("match source/map = %v/%v, want game/e2e-arena-1", data["source"], data["map_name"])
		}
		parts := participantsFrom(t, envelope)
		winner, loser := parts[101], parts[102]
		if winner == nil || loser == nil {
			t.Fatalf("participants 缺 101/102: %v", parts)
		}
		if jsonInt(t, winner, "rank_score_delta") != 325 || jsonInt(t, winner, "rank_score_after") != 325 {
			t.Errorf("winner delta/after = %d/%d, want 325/325",
				jsonInt(t, winner, "rank_score_delta"), jsonInt(t, winner, "rank_score_after"))
		}
		if jsonInt(t, loser, "rank_score_delta") != -325 || jsonInt(t, loser, "rank_score_after") != 0 {
			t.Errorf("loser delta/after = %d/%d, want -325/0",
				jsonInt(t, loser, "rank_score_delta"), jsonInt(t, loser, "rank_score_after"))
		}
		// 记分/表现列真实写入：双方 0 分起步（Elo 输入 0/0），未上报
		// perf_tweak 按 1.0。
		ws, ls, wp, lp := matchScoreColumns(t, env, match1ID)
		if ws != 0 || ls != 0 || wp != 1.0 || lp != 1.0 {
			t.Errorf("记分/表现列 = %d/%d/%v/%v, want 0/0/1/1", ws, ls, wp, lp)
		}
	})

	t.Run("user profiles reflect settlement", func(t *testing.T) {
		u101 := getUserProfile(t, env, 101)
		if jsonInt(t, u101, "rank_score") != 325 || jsonInt(t, u101, "wins_count") != 1 {
			t.Errorf("uid 101 = %v, want rank_score 325 wins_count 1", u101)
		}
		u102 := getUserProfile(t, env, 102)
		if jsonInt(t, u102, "rank_score") != 0 || jsonInt(t, u102, "wins_count") != 0 {
			t.Errorf("uid 102 = %v, want rank_score 0 wins_count 0", u102)
		}
	})

	t.Run("match detail carries participants and fields", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodGet, fmt.Sprintf("/api/matches/%d", match1ID), userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		data := dataMap(t, envelope)
		if data["source"] != "game" {
			t.Errorf("source = %v, want game", data["source"])
		}
		parts, ok := data["participants"].([]any)
		if !ok || len(parts) != 2 {
			t.Fatalf("participants = %v, want 2 entries", data["participants"])
		}
	})

	// 批量灌入：再提交 4 局（交替胜负），逐局断言 before 与上一局
	// after 衔接、after = max(0, before+delta)。
	t.Run("batch matches stay consistent", func(t *testing.T) {
		tracked := map[int64]int64{101: 325, 102: 0}
		for i := 0; i < 4; i++ {
			winner, loser := "e2e-uuid-2", "e2e-uuid-1"
			if i%2 == 0 {
				winner, loser = "e2e-uuid-1", "e2e-uuid-2"
			}
			status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, matchReport(winner, loser))
			if status != http.StatusCreated {
				t.Fatalf("第 %d 局 status = %d, want 201 (body=%v)", i+1, status, envelope)
			}
			for uid, p := range participantsFrom(t, envelope) {
				before := jsonInt(t, p, "rank_score_before")
				delta := jsonInt(t, p, "rank_score_delta")
				after := jsonInt(t, p, "rank_score_after")
				if before != tracked[uid] {
					t.Errorf("第 %d 局 uid %d before = %d, want tracked %d", i+1, uid, before, tracked[uid])
				}
				wantAfter := before + delta
				if wantAfter < 0 {
					wantAfter = 0
				}
				if after != wantAfter {
					t.Errorf("第 %d 局 uid %d after = %d, want %d", i+1, uid, after, wantAfter)
				}
				tracked[uid] = after
			}
		}
		for uid, want := range tracked {
			profile := getUserProfile(t, env, uid)
			if jsonInt(t, profile, "rank_score") != want {
				t.Errorf("uid %d rank_score = %d, want %d", uid, jsonInt(t, profile, "rank_score"), want)
			}
		}
	})

	// 平局：103 vs 104，双方同分 → delta 均为 0，无获胜队。
	// source=virtual 仅作来源标记，结算逻辑与 game 完全一致。
	t.Run("draw match settles symmetrically", func(t *testing.T) {
		body := map[string]any{
			"match_type": "1v1",
			"teams": []map[string]any{
				{"team_id": "A", "result": "draw", "initial_count": 1, "alive_count": 1,
					"members": []map[string]any{{"uuid": "e2e-uuid-3"}}},
				{"team_id": "B", "result": "draw", "initial_count": 1, "alive_count": 1,
					"members": []map[string]any{{"uuid": "e2e-uuid-4"}}},
			},
			"source":      "virtual",
			"started_at":  time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339),
			"finished_at": time.Now().UTC().Format(time.RFC3339),
		}
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%v)", status, envelope)
		}
		data := dataMap(t, envelope)
		if data["winner_team_id"] != "" {
			t.Errorf("winner_team_id = %v, want empty for draw", data["winner_team_id"])
		}
		if data["source"] != "virtual" {
			t.Errorf("source = %v, want virtual (marker only)", data["source"])
		}
		for uid, p := range participantsFrom(t, envelope) {
			if p["is_draw"] != true {
				t.Errorf("uid %d is_draw = %v, want true", uid, p["is_draw"])
			}
			if jsonInt(t, p, "rank_score_delta") != 0 {
				t.Errorf("uid %d delta = %d, want 0 for equal-rating draw", uid, jsonInt(t, p, "rank_score_delta"))
			}
		}
		// 平局无胜负方：记分/表现列保持默认值。
		ws, ls, wp, lp := matchScoreColumns(t, env, jsonInt(t, data, "id"))
		if ws != 0 || ls != 0 || wp != 1.0 || lp != 1.0 {
			t.Errorf("平局记分/表现列 = %d/%d/%v/%v, want 默认值 0/0/1/1", ws, ls, wp, lp)
		}
	})

	// 兼容形状（winner_team + loser_teams）与 teams 形状等价结算；
	// maintenance 令牌（OPS）同样可上报。
	var compatMatchID int64
	t.Run("compat shape via maintenance token", func(t *testing.T) {
		body := map[string]any{
			"match_type": "1v1",
			"winner_team": map[string]any{
				"team_id": "A", "initial_count": 1, "alive_count": 1,
				"members": []map[string]any{{"uuid": "e2e-uuid-3"}},
			},
			"loser_teams": []map[string]any{{
				"team_id": "B", "initial_count": 1, "alive_count": 1,
				"members": []map[string]any{{"uuid": "e2e-uuid-4"}},
			}},
			"started_at":  time.Now().UTC().Add(-10 * time.Minute).Format("2006-01-02 15:04:05"),
			"finished_at": time.Now().UTC().Format("2006-01-02 15:04:05"),
		}
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", maintenanceToken, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%v)", status, envelope)
		}
		compatMatchID = jsonInt(t, dataMap(t, envelope), "id")
		parts := participantsFrom(t, envelope)
		if jsonInt(t, parts[103], "rank_score_delta") != 325 || jsonInt(t, parts[104], "rank_score_delta") != -325 {
			t.Errorf("compat settlement = %v/%v, want +325/-325",
				parts[103]["rank_score_delta"], parts[104]["rank_score_delta"])
		}

		status, envelope = doJSON(t, env, http.MethodGet, fmt.Sprintf("/api/matches/%d", compatMatchID), userToken(103), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		if dataMap(t, envelope)["source"] != "game" {
			t.Errorf("source = %v, want game", dataMap(t, envelope)["source"])
		}
	})

	// 自相矛盾的上报以类型化错误拒绝。
	t.Run("inconsistent reports rejected", func(t *testing.T) {
		// 结束早于开始。
		body := matchReport("e2e-uuid-1", "e2e-uuid-2")
		body["finished_at"] = time.Now().UTC().Add(-20 * time.Minute).Format(time.RFC3339)
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, body)
		if status != http.StatusBadRequest || envelope["code"] != "mca_report_inconsistent" {
			t.Fatalf("finished<started status/code = %d/%v, want 400/mca_report_inconsistent", status, envelope["code"])
		}

		// 两支获胜队伍。
		body = matchReport("e2e-uuid-1", "e2e-uuid-2")
		teams := body["teams"].([]map[string]any)
		teams[1]["result"] = "win"
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, body)
		if status != http.StatusBadRequest || envelope["code"] != "mca_report_inconsistent" {
			t.Fatalf("two winners status/code = %d/%v, want 400/mca_report_inconsistent", status, envelope["code"])
		}

		// 重复参与者 UUID（Minecraft 形态）：拒绝原因不得向调试端点泄露。
		const dupUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		body = matchReport("e2e-uuid-1", "e2e-uuid-2")
		teams = body["teams"].([]map[string]any)
		teams[0]["members"] = []map[string]any{{"uuid": dupUUID}}
		teams[1]["members"] = []map[string]any{{"uuid": dupUUID}}
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, body)
		if status != http.StatusBadRequest || envelope["code"] != "mca_report_inconsistent" {
			t.Fatalf("duplicate uuid status/code = %d/%v, want 400/mca_report_inconsistent", status, envelope["code"])
		}
	})

	// duration 容差（契约 1.13.0，e2e 配置容差 2 秒）：跨度 600 秒的
	// 上报，漂移 2 秒接受、3 秒拒绝；0 仍跳过比对。用 103/104 结算，
	// 避免扰动 101/102 的历史对局计数断言。
	t.Run("duration tolerance", func(t *testing.T) {
		within := matchReport("e2e-uuid-3", "e2e-uuid-4")
		within["duration_seconds"] = 602
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, within)
		if status != http.StatusCreated {
			t.Fatalf("容差内 duration status = %d, want 201 (body=%v)", status, envelope)
		}
		beyond := matchReport("e2e-uuid-3", "e2e-uuid-4")
		beyond["duration_seconds"] = 603
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, beyond)
		if status != http.StatusBadRequest || envelope["code"] != "mca_report_inconsistent" {
			t.Fatalf("超容差 duration status/code = %d/%v, want 400/mca_report_inconsistent", status, envelope["code"])
		}
		skipped := matchReport("e2e-uuid-3", "e2e-uuid-4")
		skipped["duration_seconds"] = 0
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, skipped)
		if status != http.StatusCreated {
			t.Fatalf("duration 0 应跳过比对 status = %d, want 201 (body=%v)", status, envelope)
		}
	})

	// 排行榜：rank_type=1v1 与 users.rank_score 一致（data 为
	// {rankings, total_count} 结构）。
	t.Run("rankings reflect settled scores", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodGet, "/api/rankings/1v1", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		data := dataMap(t, envelope)
		raw, ok := data["rankings"].([]any)
		if !ok || len(raw) == 0 {
			t.Fatalf("rankings 为空: %v", envelope)
		}
		if jsonInt(t, data, "total_count") != int64(len(raw)) {
			t.Errorf("total_count = %d, want %d（与本页行数一致）", jsonInt(t, data, "total_count"), len(raw))
		}
		want := map[int64]int64{}
		for _, uid := range []int64{101, 102, 103, 104} {
			want[uid] = jsonInt(t, getUserProfile(t, env, uid), "rank_score")
		}
		for _, item := range raw {
			row := item.(map[string]any)
			uid := jsonInt(t, row, "user_uid")
			if score := jsonInt(t, row, "score"); want[uid] != 0 && score != want[uid] {
				t.Errorf("ranking uid %d score = %d, want %d", uid, score, want[uid])
			}
		}
	})

	// 入队→主动离开（此时 1v1 队列为空，不会被引擎抢先成局）。
	t.Run("join then leave", func(t *testing.T) {
		status, _ := doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(103), map[string]any{"match_type": "1v1"})
		if status != http.StatusCreated {
			t.Fatalf("join status = %d, want 201", status)
		}
		// 队列规模统计（只读展示）：此时全局队列恰有 103 一名单人玩家。
		status, envelope := doJSON(t, env, http.MethodGet, "/api/matchmaking/status", userToken(103), nil)
		if status != http.StatusOK {
			t.Fatalf("status 查询失败: %d %v", status, envelope)
		}
		stats, ok := dataMap(t, envelope)["queue_stats"].(map[string]any)
		if !ok {
			t.Fatalf("status 缺 queue_stats: %v", envelope)
		}
		if jsonInt(t, stats, "players") != 1 || jsonInt(t, stats, "parties") != 0 {
			t.Errorf("queue_stats = %v, want players 1 parties 0", stats)
		}
		if jsonInt(t, stats, "average_wait_seconds") < 0 {
			t.Errorf("average_wait_seconds = %v, want >= 0", stats["average_wait_seconds"])
		}
		status, envelope = doJSON(t, env, http.MethodDelete, "/api/matchmaking/leave", userToken(103), nil)
		if status != http.StatusOK {
			t.Fatalf("leave status = %d, want 200 (body=%v)", status, envelope)
		}
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matchmaking/status", userToken(103), nil)
		if status != http.StatusOK || dataMap(t, envelope)["in_queue"] != false {
			t.Fatalf("离开后状态异常: %d %v", status, envelope)
		}
	})

	// 未知模式 → 400。
	t.Run("unknown mode rejected", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(103), map[string]any{"match_type": "4v4"})
		if status != http.StatusBadRequest || envelope["code"] != "mca_invalid_request" {
			t.Fatalf("status/code = %d/%v, want 400/mca_invalid_request", status, envelope["code"])
		}
	})

	// 单人匹配闭环：入队 → 引擎成局 → 成局记录可查。
	// 用 0 分的 105/106（分差在基础窗口内，首个 tick 即成局）。
	t.Run("matchmaking forms a group", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(105), map[string]any{"match_type": "1v1"})
		if status != http.StatusCreated {
			t.Fatalf("join 105 status = %d, want 201 (body=%v)", status, envelope)
		}
		if entries := dataArray(t, envelope); len(entries) != 1 {
			t.Fatalf("solo join entries = %d, want 1", len(entries))
		}
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(106), map[string]any{"match_type": "1v1"})
		if status != http.StatusCreated {
			t.Fatalf("join 106 status = %d, want 201 (body=%v)", status, envelope)
		}

		// 重复入队 → 409。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(105), map[string]any{"match_type": "1v1"})
		if status != http.StatusConflict || envelope["code"] != "mca_queue_already_joined" {
			t.Fatalf("duplicate join status/code = %d/%v, want 409/mca_queue_already_joined", status, envelope["code"])
		}

		// 轮询等待引擎成局（tick 1s，最多 20s）。
		var group map[string]any
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			status, envelope := doJSON(t, env, http.MethodGet, "/api/matchmaking/status", userToken(105), nil)
			if status != http.StatusOK {
				t.Fatalf("status 查询失败: %d %v", status, envelope)
			}
			data := dataMap(t, envelope)
			groups, _ := data["recent_groups"].([]any)
			if len(groups) > 0 {
				group = groups[0].(map[string]any)
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if group == nil {
			t.Fatal("20s 内未成局")
		}
		members, ok := group["members"].([]any)
		if !ok || len(members) != 2 {
			t.Fatalf("成局成员 = %v, want 2 人", group["members"])
		}
		teams := map[string]bool{}
		uids := map[int64]bool{}
		for _, m := range members {
			mm := m.(map[string]any)
			teams[mm["team_id"].(string)] = true
			uids[jsonInt(t, mm, "uid")] = true
		}
		if !teams["A"] || !teams["B"] {
			t.Errorf("成局队伍 = %v, want A 与 B 各一", teams)
		}
		if !uids[105] || !uids[106] {
			t.Errorf("成局成员 = %v, want 105 与 106", uids)
		}

		// 成局记录携带房间与清单子服地址（契约 1.10.0 房间层：
		// 1v1 取第一间容量足够的空闲房 e2e-room-2a）。
		if group["room"] != "e2e-room-2a" {
			t.Errorf("成局 room = %v, want e2e-room-2a", group["room"])
		}
		jt, ok := group["join_target"].(map[string]any)
		if !ok {
			t.Fatalf("成局记录缺 join_target: %v", group)
		}
		if jt["host"] != "10.99.0.1" || jsonInt(t, jt, "port") != 25501 {
			t.Errorf("join_target = %v, want 清单子服地址 10.99.0.1:25501", jt)
		}

		// 成局后双方均已出队。
		for _, uid := range []int64{105, 106} {
			status, envelope := doJSON(t, env, http.MethodGet, "/api/matchmaking/status", userToken(uid), nil)
			if status != http.StatusOK {
				t.Fatalf("status 查询失败: %d", status)
			}
			if dataMap(t, envelope)["in_queue"] != false {
				t.Errorf("uid %d 成局后仍在队列", uid)
			}
		}

		// 未在队列时离开 → 404。
		status, envelope = doJSON(t, env, http.MethodDelete, "/api/matchmaking/leave", userToken(105), nil)
		if status != http.StatusNotFound || envelope["code"] != "mca_queue_not_joined" {
			t.Fatalf("leave status/code = %d/%v, want 404/mca_queue_not_joined", status, envelope["code"])
		}
	})

	// game_id 幂等：同一 game_id 重复上报不重复结算（200 重放），
	// 载荷冲突 → 409。
	t.Run("game_id idempotency", func(t *testing.T) {
		body := matchReport("e2e-uuid-5", "e2e-uuid-6")
		body["game_id"] = "e2e-game-1"
		body["server_id"] = "e2e-server-1"

		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, body)
		if status != http.StatusCreated {
			t.Fatalf("首次上报 status = %d, want 201 (body=%v)", status, envelope)
		}
		first := dataMap(t, envelope)
		matchID := jsonInt(t, first, "id")
		if first["game_id"] != "e2e-game-1" || first["server_id"] != "e2e-server-1" {
			t.Errorf("game_id/server_id = %v/%v, want e2e-game-1/e2e-server-1", first["game_id"], first["server_id"])
		}
		scoreAfterFirst := jsonInt(t, getUserProfile(t, env, 105), "rank_score")
		if scoreAfterFirst != 325 {
			t.Fatalf("105 rank_score = %d, want 325 after first report", scoreAfterFirst)
		}

		// 完全相同的载荷重复上报 → 200 重放，同一记录，绝不重复结算。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, body)
		if status != http.StatusOK {
			t.Fatalf("重放 status = %d, want 200 (body=%v)", status, envelope)
		}
		if id := jsonInt(t, dataMap(t, envelope), "id"); id != matchID {
			t.Errorf("重放返回 id = %d, want %d（既有记录）", id, matchID)
		}
		if score := jsonInt(t, getUserProfile(t, env, 105), "rank_score"); score != scoreAfterFirst {
			t.Errorf("重放后 105 rank_score = %d, want %d（不得重复结算）", score, scoreAfterFirst)
		}

		// 同一 game_id、不同载荷 → 409 冲突。
		conflict := matchReport("e2e-uuid-6", "e2e-uuid-5")
		conflict["game_id"] = "e2e-game-1"
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, conflict)
		if status != http.StatusConflict || envelope["code"] != "mca_game_id_conflict" {
			t.Fatalf("冲突 status/code = %d/%v, want 409/mca_game_id_conflict", status, envelope["code"])
		}
	})

	// 组队匹配：邀请-接受同意（契约 1.11.0）→ join 携带 party_members
	// → 整队入队 → 成局（同队同 party 不拆分）→ 队伍共享结算 → 清理后可重组队。
	t.Run("party matchmaking end to end", func(t *testing.T) {
		// 非法输入：未知成员 → 404；2v2 超员 → 409。
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(107),
			map[string]any{"match_type": "2v2", "party_members": []string{"e2e-uuid-7", "e2e-uuid-unknown"}})
		if status != http.StatusNotFound || envelope["code"] != "mca_user_not_found" {
			t.Fatalf("未知成员 status/code = %d/%v, want 404/mca_user_not_found", status, envelope["code"])
		}
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(107),
			map[string]any{"match_type": "2v2", "party_members": []string{"e2e-uuid-5", "e2e-uuid-6", "e2e-uuid-3"}})
		if status != http.StatusConflict || envelope["code"] != "mca_party_full" {
			t.Fatalf("超员组队 status/code = %d/%v, want 409/mca_party_full", status, envelope["code"])
		}

		// 同意机制（默认拒绝）：名单不含调用者 → 400；成员未接受邀请 → 409。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(107),
			map[string]any{"match_type": "2v2", "party_members": []string{"e2e-uuid-8", "e2e-uuid-9"}})
		if status != http.StatusBadRequest || envelope["code"] != "mca_caller_not_in_roster" {
			t.Fatalf("名单缺调用者 status/code = %d/%v, want 400/mca_caller_not_in_roster", status, envelope["code"])
		}
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(107),
			map[string]any{"match_type": "2v2", "party_members": []string{"e2e-uuid-7", "e2e-uuid-8"}})
		if status != http.StatusConflict || envelope["code"] != "mca_consent_missing" {
			t.Fatalf("无同意入队 status/code = %d/%v, want 409/mca_consent_missing", status, envelope["code"])
		}

		// 邀请生命周期：发出（pending）→ 列表可见 → 接受（终态）→
		// 重复接受 409 → 拒绝路径终态 → 过期路径 409。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/invites", userToken(107),
			map[string]any{"match_type": "2v2", "member_uuids": []string{"e2e-uuid-8"}})
		if status != http.StatusCreated {
			t.Fatalf("发邀请 status = %d, want 201 (body=%v)", status, envelope)
		}
		lifecycleInviteID := jsonInt(t, dataArray(t, envelope)[0].(map[string]any), "id")
		// 受邀人视角列表可见该 pending 邀请。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matchmaking/invites", userToken(108), nil)
		if status != http.StatusOK {
			t.Fatalf("查邀请 status = %d, want 200 (body=%v)", status, envelope)
		}
		foundPending := false
		for _, item := range dataArray(t, envelope) {
			inv := item.(map[string]any)
			if jsonInt(t, inv, "id") == lifecycleInviteID && inv["status"] == "pending" {
				foundPending = true
			}
		}
		if !foundPending {
			t.Fatalf("受邀人列表未见 pending 邀请 %d: %v", lifecycleInviteID, envelope)
		}
		// 非受邀人不得接受（邀请人自己也不行）→ 403。
		status, envelope = doJSON(t, env, http.MethodPost,
			fmt.Sprintf("/api/matchmaking/invites/%d/accept", lifecycleInviteID), userToken(107), nil)
		if status != http.StatusForbidden || envelope["code"] != "mca_not_invitee" {
			t.Fatalf("非受邀人接受 status/code = %d/%v, want 403/mca_not_invitee", status, envelope["code"])
		}
		// 受邀人接受 → 200 accepted；重复接受 → 409 resolved。
		status, envelope = doJSON(t, env, http.MethodPost,
			fmt.Sprintf("/api/matchmaking/invites/%d/accept", lifecycleInviteID), userToken(108), nil)
		if status != http.StatusOK || dataMap(t, envelope)["status"] != "accepted" {
			t.Fatalf("接受邀请 status = %d, want 200 accepted (body=%v)", status, envelope)
		}
		status, envelope = doJSON(t, env, http.MethodPost,
			fmt.Sprintf("/api/matchmaking/invites/%d/accept", lifecycleInviteID), userToken(108), nil)
		if status != http.StatusConflict || envelope["code"] != "mca_invite_resolved" {
			t.Fatalf("重复接受 status/code = %d/%v, want 409/mca_invite_resolved", status, envelope["code"])
		}
		// 两队整队入队（107+108 对 109+110）：108 已接受 107 的邀请
		//（lifecycleInviteID，TTL 内接受）；110 接受 109 的新邀请。
		// 拒绝/过期路径放在两队入队之后断言——TTL 仅 2s，提前跑会
		// 把前面的同意拖过期。
		inviteAndAccept(t, env, "2v2", userToken(109), userToken(110), "e2e-uuid-10")
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(107),
			map[string]any{"match_type": "2v2", "party_members": []string{"e2e-uuid-7", "e2e-uuid-8"}})
		if status != http.StatusCreated {
			t.Fatalf("party1 入队 status = %d, want 201 (body=%v)", status, envelope)
		}
		entries := dataArray(t, envelope)
		if len(entries) != 2 {
			t.Fatalf("party1 入队条目 = %d, want 2（整队）", len(entries))
		}
		party1 := jsonInt(t, entries[0].(map[string]any), "party_id")
		// 队列统计：party1 整队在队（2v2 凑不齐不会被引擎消费）→
		// 玩家 2、队伍 1。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matchmaking/status", userToken(107), nil)
		if status != http.StatusOK {
			t.Fatalf("status 查询失败: %d %v", status, envelope)
		}
		stats, ok := dataMap(t, envelope)["queue_stats"].(map[string]any)
		if !ok {
			t.Fatalf("status 缺 queue_stats: %v", envelope)
		}
		if jsonInt(t, stats, "players") != 2 || jsonInt(t, stats, "parties") != 1 {
			t.Errorf("queue_stats = %v, want players 2 parties 1（队伍按 party 计）", stats)
		}
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(109),
			map[string]any{"match_type": "2v2", "party_members": []string{"e2e-uuid-9", "e2e-uuid-10"}})
		if status != http.StatusCreated {
			t.Fatalf("party2 入队 status = %d, want 201 (body=%v)", status, envelope)
		}
		party2 := jsonInt(t, dataArray(t, envelope)[0].(map[string]any), "party_id")
		if party1 == party2 {
			t.Fatalf("两队 party_id 相同: %d", party1)
		}

		// 已在队伍（排队中）的用户不能单人排队。（须紧跟两队入队执行：
		// 引擎 1s 一跳，拖久了队伍会被成局消费、临时队伍清理。）
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(110),
			map[string]any{"match_type": "2v2"})
		if status != http.StatusConflict || envelope["code"] != "mca_party_already_in_party" {
			t.Fatalf("队员单人排队 status/code = %d/%v, want 409/mca_party_already_in_party", status, envelope["code"])
		}

		// 拒绝路径：新邀请 → 受邀人拒绝（终态）→ 拿被拒邀请入队仍 409。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/invites", userToken(109),
			map[string]any{"match_type": "2v2", "member_uuids": []string{"e2e-uuid-8"}})
		if status != http.StatusCreated {
			t.Fatalf("发邀请(拒绝路径) status = %d, want 201 (body=%v)", status, envelope)
		}
		rejectID := jsonInt(t, dataArray(t, envelope)[0].(map[string]any), "id")
		status, envelope = doJSON(t, env, http.MethodPost,
			fmt.Sprintf("/api/matchmaking/invites/%d/reject", rejectID), userToken(108), nil)
		if status != http.StatusOK || dataMap(t, envelope)["status"] != "rejected" {
			t.Fatalf("拒绝邀请 status = %d, want 200 rejected (body=%v)", status, envelope)
		}
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(109),
			map[string]any{"match_type": "2v2", "party_members": []string{"e2e-uuid-9", "e2e-uuid-8"}})
		if status != http.StatusConflict || envelope["code"] != "mca_consent_missing" {
			t.Fatalf("被拒邀请入队 status/code = %d/%v, want 409/mca_consent_missing", status, envelope["code"])
		}
		// 过期路径：邀请（TTL 2s）→ 等待过期 → 接受 409、列表显示 expired。
		// 成局可能在此 sleep 期间发生，不影响本断言（邀请独立于队列）。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/invites", userToken(109),
			map[string]any{"match_type": "2v2", "member_uuids": []string{"e2e-uuid-8"}})
		if status != http.StatusCreated {
			t.Fatalf("发邀请(过期路径) status = %d, want 201 (body=%v)", status, envelope)
		}
		expireID := jsonInt(t, dataArray(t, envelope)[0].(map[string]any), "id")
		time.Sleep(2200 * time.Millisecond)
		status, envelope = doJSON(t, env, http.MethodPost,
			fmt.Sprintf("/api/matchmaking/invites/%d/accept", expireID), userToken(108), nil)
		if status != http.StatusConflict || envelope["code"] != "mca_invite_expired" {
			t.Fatalf("过期接受 status/code = %d/%v, want 409/mca_invite_expired", status, envelope["code"])
		}
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matchmaking/invites", userToken(108), nil)
		if status != http.StatusOK {
			t.Fatalf("查邀请(过期路径) status = %d, want 200", status)
		}
		foundExpired := false
		for _, item := range dataArray(t, envelope) {
			inv := item.(map[string]any)
			if jsonInt(t, inv, "id") == expireID && inv["status"] == "expired" {
				foundExpired = true
			}
		}
		if !foundExpired {
			t.Fatalf("列表未见 expired 邀请 %d: %v", expireID, envelope)
		}

		// 轮询等待引擎成局。
		var group map[string]any
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			status, envelope := doJSON(t, env, http.MethodGet, "/api/matchmaking/status", userToken(107), nil)
			if status != http.StatusOK {
				t.Fatalf("status 查询失败: %d %v", status, envelope)
			}
			groups, _ := dataMap(t, envelope)["recent_groups"].([]any)
			if len(groups) > 0 {
				group = groups[0].(map[string]any)
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if group == nil {
			t.Fatal("20s 内未成局")
		}
		groupID := jsonInt(t, group, "group_id")
		members, _ := group["members"].([]any)
		if len(members) != 4 {
			t.Fatalf("成局成员 = %d, want 4", len(members))
		}
		teamOf := map[int64]string{}
		partyOf := map[int64]int64{}
		for _, m := range members {
			mm := m.(map[string]any)
			uid := jsonInt(t, mm, "uid")
			teamOf[uid] = mm["team_id"].(string)
			if pid, ok := mm["party_id"].(float64); ok {
				partyOf[uid] = int64(pid)
			}
		}
		// 同 party 成员必须同队且不被拆分。
		if teamOf[107] != teamOf[108] || teamOf[109] != teamOf[110] {
			t.Errorf("队伍被拆分: %v", teamOf)
		}
		if teamOf[107] == teamOf[109] {
			t.Errorf("两队被装入同一队: %v", teamOf)
		}
		if partyOf[107] != party1 || partyOf[108] != party1 || partyOf[109] != party2 || partyOf[110] != party2 {
			t.Errorf("成局成员 party 归属错误: %v", partyOf)
		}

		// 队伍共享结算：关联成局上报 2v2 结果，107/108 表现微调不同
		//（原始 delta 341/309）→ 共享平均值 325。
		report := map[string]any{
			"match_type": "2v2",
			"teams": []map[string]any{
				{"team_id": "red", "result": "win", "initial_count": 2, "alive_count": 2,
					"members": []map[string]any{
						{"uuid": "e2e-uuid-7", "perf_tweak": 1.05},
						{"uuid": "e2e-uuid-8", "perf_tweak": 0.95},
					}},
				{"team_id": "blue", "result": "lose", "initial_count": 2, "alive_count": 2,
					"members": []map[string]any{
						{"uuid": "e2e-uuid-9"},
						{"uuid": "e2e-uuid-10"},
					}},
			},
			"matchmaking_group_id": groupID,
			"started_at":           time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339),
			"finished_at":          time.Now().UTC().Format(time.RFC3339),
		}
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, report)
		if status != http.StatusCreated {
			t.Fatalf("共享结算上报 status = %d, want 201 (body=%v)", status, envelope)
		}
		parts := participantsFrom(t, envelope)
		if jsonInt(t, parts[107], "rank_score_delta") != 325 || jsonInt(t, parts[108], "rank_score_delta") != 325 {
			t.Errorf("party 共享 delta = %d/%d, want 325/325（原始 341/309 平均）",
				jsonInt(t, parts[107], "rank_score_delta"), jsonInt(t, parts[108], "rank_score_delta"))
		}
		if jsonInt(t, parts[109], "rank_score_delta") != -325 || jsonInt(t, parts[110], "rank_score_delta") != -325 {
			t.Errorf("败方 delta = %d/%d, want -325/-325",
				jsonInt(t, parts[109], "rank_score_delta"), jsonInt(t, parts[110], "rank_score_delta"))
		}
		if score := jsonInt(t, getUserProfile(t, env, 107), "rank_score"); score != 325 {
			t.Errorf("107 rank_score = %d, want 325", score)
		}
		// 记分/表现列真实写入：双方结算前平均分 0（均为新建行 0 分），
		// 胜方生效 perf 均值 (1.05+0.95)/2 = 1.0，败方未上报按 1.0。
		ws, ls, wp, lp := matchScoreColumns(t, env, jsonInt(t, dataMap(t, envelope), "id"))
		if ws != 0 || ls != 0 || wp != 1.0 || lp != 1.0 {
			t.Errorf("2v2 记分/表现列 = %d/%d/%v/%v, want 0/0/1/1", ws, ls, wp, lp)
		}

		// 成局后临时队伍已清理：同样成员可立即重组队入队，随后任一成员
		// 离队即整队移出。上一张同意已被首次入队消费——重组队需要
		// 新一轮邀请-接受（同意一次性）。
		inviteAndAccept(t, env, "2v2", userToken(107), userToken(108), "e2e-uuid-8")
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(107),
			map[string]any{"match_type": "2v2", "party_members": []string{"e2e-uuid-7", "e2e-uuid-8"}})
		if status != http.StatusCreated {
			t.Fatalf("重组队入队 status = %d, want 201（成局后队伍应已清理）(body=%v)", status, envelope)
		}
		status, envelope = doJSON(t, env, http.MethodDelete, "/api/matchmaking/leave", userToken(108), nil)
		if status != http.StatusOK {
			t.Fatalf("整队离队 status = %d, want 200 (body=%v)", status, envelope)
		}
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matchmaking/status", userToken(107), nil)
		if status != http.StatusOK || dataMap(t, envelope)["in_queue"] != false {
			t.Fatalf("整队离队后 107 仍在队列: %d %v", status, envelope)
		}
	})

	// 成局分配查询（契约 1.9.0/1.9.1，代理路由用）：105/106 的 1v1 成局未被
	// 结算引用（幂等子测试的上报未携带 matchmaking_group_id）→ 有效；
	// 107 的 2v2 成局已被共享结算上报引用 → 已消费。玩家令牌 403，
	// 未知 UUID 等价无分配（assigned:false）。
	t.Run("assignment query for proxy routing", func(t *testing.T) {
		// 玩家令牌被拒（即使带 scope 也只认 OPS 凭据）。
		status, envelope := doJSON(t, env, http.MethodGet, "/api/matchmaking/assignment/e2e-uuid-5", userToken(105), nil)
		if status != http.StatusForbidden || envelope["code"] != "oauth_insufficient_scope" {
			t.Fatalf("玩家令牌 status/code = %d/%v, want 403/oauth_insufficient_scope", status, envelope["code"])
		}

		// OPS 凭据查询有效分配（105 的 1v1 成局未结算、未过期；
		// 房间层下携带房间与清单子服地址）。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matchmaking/assignment/e2e-uuid-5", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("assignment status = %d, want 200 (body=%v)", status, envelope)
		}
		asn := dataMap(t, envelope)
		if asn["assigned"] != true {
			t.Fatalf("assigned = %v, want true（成局未结算未过期）", asn["assigned"])
		}
		if asn["match_type"] != "1v1" || asn["team_id"] == "" {
			t.Errorf("assignment = %v, want match_type 1v1 且带 team_id", asn)
		}
		if asn["room"] != "e2e-room-2a" {
			t.Errorf("assignment room = %v, want e2e-room-2a", asn["room"])
		}
		jt, ok := asn["join_target"].(map[string]any)
		if !ok {
			t.Fatalf("assignment 缺 join_target: %v", asn)
		}
		if jt["host"] != "10.99.0.1" || jsonInt(t, jt, "port") != 25501 {
			t.Errorf("join_target = %v, want 清单子服地址 10.99.0.1:25501", jt)
		}

		// 已被结算消费的成局（2v2 共享结算上报携带 matchmaking_group_id）。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matchmaking/assignment/e2e-uuid-7", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("assignment status = %d, want 200 (body=%v)", status, envelope)
		}
		if asn = dataMap(t, envelope); asn["assigned"] != false {
			t.Fatalf("assigned = %v, want false（成局已被结算消费）", asn["assigned"])
		}

		// 未知 UUID 与"无分配"路由等价（1.9.1）：200 + assigned:false。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matchmaking/assignment/e2e-uuid-unknown", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("未知 UUID status = %d, want 200 (body=%v)", status, envelope)
		}
		if asn = dataMap(t, envelope); asn["assigned"] != false {
			t.Fatalf("未知 UUID assigned = %v, want false", asn["assigned"])
		}
	})

	// 房间预占（契约 1.10.0，决策 3C/4B）：四队 2v2 竞争唯一的 4 人房
	// e2e-room-4 —— 第一局（两队互搏）预占成局，剩余两队无法再成局
	// 只能留队等待；首局带 group id 结算释放后，等待两队预占同一房间成局。
	t.Run("room reservation contention and settlement release", func(t *testing.T) {
		parties := [][]string{
			{"e2e-uuid-11", "e2e-uuid-12"},
			{"e2e-uuid-13", "e2e-uuid-14"},
			{"e2e-uuid-15", "e2e-uuid-16"},
			{"e2e-uuid-17", "e2e-uuid-18"},
		}
		leaders := []int64{111, 113, 115, 117}
		for i, roster := range parties {
			// 同意前置：队长邀请队友并获接受（契约 1.11.0）。
			inviteAndAccept(t, env, "2v2", userToken(leaders[i]), userToken(leaders[i]+1), roster[1])
			status, envelope := doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(leaders[i]),
				map[string]any{"match_type": "2v2", "party_members": roster})
			if status != http.StatusCreated {
				t.Fatalf("队伍%d入队 status = %d, want 201 (body=%v)", i+1, status, envelope)
			}
		}

		// uid→uuid 反查（结算上报按成局实际队伍构造）。
		uuidOf := map[int64]string{}
		for uuid, uid := range e2eUsers {
			uuidOf[uid] = uuid
		}

		// 轮询直到"恰有两队成局（一局）、另两队仍在队列"。
		latestGroup := func(uid int64) (map[string]any, bool) {
			status, envelope := doJSON(t, env, http.MethodGet, "/api/matchmaking/status", userToken(uid), nil)
			if status != http.StatusOK {
				t.Fatalf("status 查询失败: %d %v", status, envelope)
			}
			data := dataMap(t, envelope)
			groups, _ := data["recent_groups"].([]any)
			if len(groups) == 0 {
				return nil, data["in_queue"] == true
			}
			return groups[0].(map[string]any), false
		}
		var formedLeader, waitingLeader int64
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			g1, inQ1 := latestGroup(111)
			g2, inQ2 := latestGroup(115)
			if g1 != nil && g2 != nil {
				t.Fatalf("两局同时成立：同一房间不得被两个 group 预占\n%v\n%v", g1, g2)
			}
			if g1 != nil && inQ2 {
				formedLeader, waitingLeader = 111, 115
				break
			}
			if g2 != nil && inQ1 {
				formedLeader, waitingLeader = 115, 111
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if formedLeader == 0 {
			t.Fatal("20s 内未出现'一局成立、另两队等待'的状态")
		}

		// 成立局的房间必须是唯一的 4 人房，且四名成员分属两队。
		formed, _ := latestGroup(formedLeader)
		if formed["room"] != "e2e-room-4" {
			t.Fatalf("成局 room = %v, want e2e-room-4（唯一容量>=4 的房间）", formed["room"])
		}
		groupID := jsonInt(t, formed, "group_id")
		members, _ := formed["members"].([]any)
		if len(members) != 4 {
			t.Fatalf("成局成员 = %d, want 4", len(members))
		}
		teamUUIDs := map[string][]string{}
		for _, m := range members {
			mm := m.(map[string]any)
			tid := mm["team_id"].(string)
			teamUUIDs[tid] = append(teamUUIDs[tid], uuidOf[jsonInt(t, mm, "uid")])
		}

		// 结算即释放：上报携带 matchmaking_group_id（按实际队伍构造胜负）。
		var winTeam, loseTeam []string
		for tid, uuids := range teamUUIDs {
			if winTeam == nil {
				winTeam = uuids
				_ = tid
			} else {
				loseTeam = uuids
			}
		}
		report := map[string]any{
			"match_type": "2v2",
			"teams": []map[string]any{
				{"team_id": "red", "result": "win", "initial_count": 2, "alive_count": 2,
					"members": []map[string]any{{"uuid": winTeam[0]}, {"uuid": winTeam[1]}}},
				{"team_id": "blue", "result": "lose", "initial_count": 2, "alive_count": 2,
					"members": []map[string]any{{"uuid": loseTeam[0]}, {"uuid": loseTeam[1]}}},
			},
			"matchmaking_group_id": groupID,
			"started_at":           time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339),
			"finished_at":          time.Now().UTC().Format(time.RFC3339),
		}
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, report)
		if status != http.StatusCreated {
			t.Fatalf("结算上报 status = %d, want 201 (body=%v)", status, envelope)
		}

		// 释放后等待两队预占同一房间成局（房间复用）。
		var second map[string]any
		deadline = time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if g, _ := latestGroup(waitingLeader); g != nil {
				second = g
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if second == nil {
			t.Fatal("释放后 20s 内等待两队未成局")
		}
		if second["room"] != "e2e-room-4" {
			t.Errorf("复用房间 room = %v, want e2e-room-4（结算释放后再预占）", second["room"])
		}
	})

	// 按玩家查历史战绩：101 参与 5 局（首局 + 批量 4 局）。
	// 列表项携带"本人视角"摘要（本人所在队伍、胜负、分数变化）。
	t.Run("user match history", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodGet, "/api/users/101/matches", userToken(102), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		if matches := dataArray(t, envelope); len(matches) != 5 {
			t.Fatalf("101 历史对局 = %d, want 5", len(matches))
		}

		// /api/matches/me 等价（本人视角）：首局 101 胜，摘要为胜方视角。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches/me?page_size=100", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		matches := dataArray(t, envelope)
		if len(matches) != 5 {
			t.Fatalf("101 历史对局 = %d, want 5", len(matches))
		}
		var first map[string]any
		for _, item := range matches {
			m := item.(map[string]any)
			if jsonInt(t, m, "id") == match1ID {
				first = m
				break
			}
		}
		if first == nil {
			t.Fatalf("历史中未找到首局 id=%d", match1ID)
		}
		self, ok := first["self_summary"].(map[string]any)
		if !ok {
			t.Fatalf("列表项缺 self_summary: %v", first)
		}
		if self["team_id"] != "A" || self["is_winner"] != true ||
			jsonInt(t, self, "rank_score_delta") != 325 || jsonInt(t, self, "rank_score_after") != 325 {
			t.Errorf("101 首局 self_summary = %v, want team A 胜 delta 325 after 325", self)
		}

		// 同一局从 102 视角查询：摘要为败方视角。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/users/102/matches?page_size=100", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		var loserView map[string]any
		for _, item := range dataArray(t, envelope) {
			m := item.(map[string]any)
			if jsonInt(t, m, "id") == match1ID {
				loserView = m
				break
			}
		}
		if loserView == nil {
			t.Fatalf("102 历史中未找到首局 id=%d", match1ID)
		}
		loserSelf, ok := loserView["self_summary"].(map[string]any)
		if !ok {
			t.Fatalf("102 列表项缺 self_summary: %v", loserView)
		}
		if loserSelf["team_id"] != "B" || loserSelf["is_winner"] != false ||
			jsonInt(t, loserSelf, "rank_score_delta") != -325 || jsonInt(t, loserSelf, "rank_score_after") != 0 {
			t.Errorf("102 首局 self_summary = %v, want team B 负 delta -325 after 0", loserSelf)
		}

		// 分页兼容：page_size 生效，结构不变。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches/me?page_num=1&page_size=3", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		if matches := dataArray(t, envelope); len(matches) != 3 {
			t.Fatalf("分页 page_size=3 返回 %d 条, want 3", len(matches))
		}
	})

	// 段位阶梯（纯展示配置）：配置了 3 档则按序返回；需要鉴权。
	t.Run("ranking tiers", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodGet, "/api/rankings/tiers", "", nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("无令牌 status = %d, want 401 (body=%v)", status, envelope)
		}

		status, envelope = doJSON(t, env, http.MethodGet, "/api/rankings/tiers", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		tiers := dataArray(t, envelope)
		if len(tiers) != 3 {
			t.Fatalf("tiers = %d, want 3", len(tiers))
		}
		wantNames := []string{"青铜", "白银", "黄金"}
		wantScores := []int64{0, 300, 600}
		for i, item := range tiers {
			tier := item.(map[string]any)
			if tier["name"] != wantNames[i] || jsonInt(t, tier, "min_score") != wantScores[i] {
				t.Errorf("tier[%d] = %v, want %s/%d", i, tier, wantNames[i], wantScores[i])
			}
		}
	})

	// 运维查询端点（仅 OPS）：全局对局列表（分页+过滤+倒序）、
	// 按 game_id 查单局、拒绝记录（脱敏）。
	t.Run("ops query endpoints", func(t *testing.T) {
		// 玩家令牌一律 403。
		for _, path := range []string{"/api/matches", "/api/matches/by-game-id/e2e-game-1", "/api/matches/rejections"} {
			status, envelope := doJSON(t, env, http.MethodGet, path, userToken(101), nil)
			if status != http.StatusForbidden || envelope["code"] != "oauth_insufficient_scope" {
				t.Fatalf("GET %s 玩家令牌 status/code = %d/%v, want 403/oauth_insufficient_scope", path, status, envelope["code"])
			}
		}

		// 全局列表：倒序、含参与者明细（与单局详情同形状）。
		status, envelope := doJSON(t, env, http.MethodGet, "/api/matches?page_size=100", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("GET /api/matches status = %d, want 200 (body=%v)", status, envelope)
		}
		all := dataArray(t, envelope)
		if len(all) < 8 {
			t.Fatalf("全局对局数 = %d, want >= 8", len(all))
		}
		var prevFinished string
		for i, item := range all {
			m := item.(map[string]any)
			finished := m["finished_at"].(string)
			if prevFinished != "" && finished > prevFinished {
				t.Fatalf("列表未按 finished_at 倒序: 第 %d 条 %s 晚于前一条 %s", i, finished, prevFinished)
			}
			prevFinished = finished
			if i == 0 {
				if _, ok := m["participants"].([]any); !ok {
					t.Errorf("列表条目缺 participants（应与单局详情同形状）: %v", m)
				}
			}
		}

		// 过滤：source=virtual 只含平局那局。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches?source=virtual", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("source 过滤 status = %d (body=%v)", status, envelope)
		}
		virtuals := dataArray(t, envelope)
		if len(virtuals) != 1 {
			t.Fatalf("source=virtual 对局数 = %d, want 1", len(virtuals))
		}
		if virtuals[0].(map[string]any)["source"] != "virtual" {
			t.Errorf("source 过滤结果错误: %v", virtuals[0])
		}

		// 过滤：game_id 精确。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches?game_id=e2e-game-1", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("game_id 过滤 status = %d (body=%v)", status, envelope)
		}
		byGame := dataArray(t, envelope)
		if len(byGame) != 1 || byGame[0].(map[string]any)["game_id"] != "e2e-game-1" {
			t.Fatalf("game_id 过滤结果 = %v, want 恰一条 e2e-game-1", byGame)
		}

		// 过滤：参与者 uid。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches?uid=101&page_size=100", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("uid 过滤 status = %d (body=%v)", status, envelope)
		}
		if n := len(dataArray(t, envelope)); n != 5 {
			t.Fatalf("uid=101 对局数 = %d, want 5", n)
		}

		// 过滤：地图。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches?map_name=e2e-arena-1&page_size=100", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("map_name 过滤 status = %d (body=%v)", status, envelope)
		}
		if n := len(dataArray(t, envelope)); n < 6 {
			t.Fatalf("map_name=e2e-arena-1 对局数 = %d, want >= 6", n)
		}

		// 过滤：时间区间（finished_at）。
		status, envelope = doJSON(t, env, http.MethodGet,
			"/api/matches?finished_from=2020-01-01T00:00:00Z&finished_to=2020-01-02T00:00:00Z", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("时间区间过滤 status = %d (body=%v)", status, envelope)
		}
		if n := len(dataArray(t, envelope)); n != 0 {
			t.Fatalf("远古时间区间对局数 = %d, want 0", n)
		}
		status, envelope = doJSON(t, env, http.MethodGet,
			"/api/matches?finished_from=2020-01-01T00:00:00Z&page_size=100", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("半开区间过滤 status = %d (body=%v)", status, envelope)
		}
		if n := len(dataArray(t, envelope)); n < 8 {
			t.Fatalf("finished_from 远过去对局数 = %d, want >= 8", n)
		}

		// 非法时间参数 → 400。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches?finished_from=yesterday", reporterToken, nil)
		if status != http.StatusBadRequest || envelope["code"] != "mca_invalid_request" {
			t.Fatalf("非法时间参数 status/code = %d/%v, want 400/mca_invalid_request", status, envelope["code"])
		}

		// 按 game_id 查单局。
		wantID := jsonInt(t, byGame[0].(map[string]any), "id")
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches/by-game-id/e2e-game-1", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("by-game-id status = %d, want 200 (body=%v)", status, envelope)
		}
		if id := jsonInt(t, dataMap(t, envelope), "id"); id != wantID {
			t.Errorf("by-game-id id = %d, want %d", id, wantID)
		}
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches/by-game-id/no-such-game", reporterToken, nil)
		if status != http.StatusNotFound || envelope["code"] != "mca_match_not_found" {
			t.Fatalf("by-game-id 未命中 status/code = %d/%v, want 404/mca_match_not_found", status, envelope["code"])
		}

		// 拒绝记录：含两类拒绝，冲突记录带 game_id，且不泄露玩家 UUID。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/matches/rejections", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("rejections status = %d, want 200 (body=%v)", status, envelope)
		}
		records := dataArray(t, envelope)
		if len(records) < 4 {
			t.Fatalf("拒绝记录数 = %d, want >= 4（3 矛盾 + 1 冲突）", len(records))
		}
		var sawInconsistent, sawConflict bool
		for _, item := range records {
			rec := item.(map[string]any)
			code := rec["code"].(string)
			reason, _ := rec["reason"].(string)
			if strings.Contains(reason, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee") {
				t.Errorf("拒绝记录泄露玩家 UUID: %v", rec)
			}
			switch code {
			case "mca_report_inconsistent":
				sawInconsistent = true
			case "mca_game_id_conflict":
				sawConflict = true
				if rec["game_id"] != "e2e-game-1" {
					t.Errorf("冲突记录 game_id = %v, want e2e-game-1", rec["game_id"])
				}
			default:
				t.Errorf("意外的拒绝码: %v", rec)
			}
			if rec["time"] == "" {
				t.Errorf("拒绝记录缺时间: %v", rec)
			}
		}
		if !sawInconsistent || !sawConflict {
			t.Errorf("拒绝记录缺类型: inconsistent=%v conflict=%v", sawInconsistent, sawConflict)
		}
	})

	// 指标：仅 OPS 可见；含成局数、拒绝原因分布、队列深度。
	t.Run("metrics endpoint", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodGet, "/api/metrics", userToken(101), nil)
		if status != http.StatusForbidden || envelope["code"] != "oauth_insufficient_scope" {
			t.Fatalf("玩家查指标 status/code = %d/%v, want 403/oauth_insufficient_scope", status, envelope["code"])
		}

		status, envelope = doJSON(t, env, http.MethodGet, "/api/metrics", reporterToken, nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		data := dataMap(t, envelope)
		counters, ok := data["counters"].(map[string]any)
		if !ok {
			t.Fatalf("counters 缺失: %v", data)
		}
		counterAtLeast := func(key string, min int64) {
			t.Helper()
			v, ok := counters[key].(float64)
			if !ok || int64(v) < min {
				t.Errorf("counter %s = %v, want >= %d", key, counters[key], min)
			}
		}
		counterAtLeast("matches_reported_total", 8)
		counterAtLeast("matches_reported_replay_total", 1)
		counterAtLeast("matches_rejected_total:mca_report_inconsistent", 3)
		counterAtLeast("matches_rejected_total:mca_game_id_conflict", 1)
		counterAtLeast("matchmaking_groups_formed_total:1v1", 1)
		counterAtLeast("matchmaking_groups_formed_total:2v2", 1)
		counterAtLeast("matchmaking_parties_created_total", 3)
		if _, ok := data["queue_depth"].(map[string]any); !ok {
			t.Errorf("queue_depth 缺失或不是对象: %v", data["queue_depth"])
		}
	})

	// 赛季手动更迭（契约 1.12.0，作者决策）：仅 OPS、逐季递增、重置
	// 与否为显式参数、全程记录在案、重复调用 409 不再推进；更季后的
	// 结算写新赛季，旧赛季榜可查，缺省 season 跟随当前赛季。放在最后：
	// 重置评分会改动全局用户分。
	t.Run("manual season rollover", func(t *testing.T) {
		// 玩家令牌无更季权限 → 403。
		status, envelope := doJSON(t, env, http.MethodPost, "/api/seasons/roll", userToken(101),
			map[string]any{"target_season": 2})
		if status != http.StatusForbidden || envelope["code"] != "oauth_insufficient_scope" {
			t.Fatalf("玩家更季 status/code = %d/%v, want 403/oauth_insufficient_scope", status, envelope["code"])
		}

		// 初始：当前赛季 = 1（迁移种子）。
		status, envelope = doJSON(t, env, http.MethodGet, "/api/seasons", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("查赛季 status = %d, want 200 (body=%v)", status, envelope)
		}
		seasons := dataArray(t, envelope)
		if len(seasons) == 0 || jsonInt(t, seasons[0].(map[string]any), "season") != 1 {
			t.Fatalf("当前赛季 = %v, want 1（迁移种子）", envelope)
		}

		// 重复/回退目标（1）→ 409 mca_season_exists；跳季（3）→ 400。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/seasons/roll", reporterToken,
			map[string]any{"target_season": 1})
		if status != http.StatusConflict || envelope["code"] != "mca_season_exists" {
			t.Fatalf("重复赛季 status/code = %d/%v, want 409/mca_season_exists", status, envelope["code"])
		}
		status, envelope = doJSON(t, env, http.MethodPost, "/api/seasons/roll", reporterToken,
			map[string]any{"target_season": 3})
		if status != http.StatusBadRequest || envelope["code"] != "mca_invalid_request" {
			t.Fatalf("跳季 status/code = %d/%v, want 400/mca_invalid_request", status, envelope["code"])
		}

		// 更季到 2：默认不重置评分，记录在案。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/seasons/roll", reporterToken,
			map[string]any{"target_season": 2, "note": "e2e season 2"})
		if status != http.StatusCreated {
			t.Fatalf("更季 status = %d, want 201 (body=%v)", status, envelope)
		}
		if rec := dataMap(t, envelope); jsonInt(t, rec, "season") != 2 || rec["reset_scores"] != false {
			t.Fatalf("更季记录 = %v, want season 2 且不重置", rec)
		}
		// 重复同一目标 → 409（重试安全：绝不重复推进）。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/seasons/roll", reporterToken,
			map[string]any{"target_season": 2})
		if status != http.StatusConflict || envelope["code"] != "mca_season_exists" {
			t.Fatalf("更季重试 status/code = %d/%v, want 409/mca_season_exists", status, envelope["code"])
		}

		// 更季后的结算写入赛季 2；赛季 1 榜保留；缺省 season 跟随当前。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, matchReport("e2e-uuid-1", "e2e-uuid-2"))
		if status != http.StatusCreated {
			t.Fatalf("更季后上报 status = %d, want 201 (body=%v)", status, envelope)
		}
		status, envelope = doJSON(t, env, http.MethodGet, "/api/rankings/1v1?season=2", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("查赛季2榜 status = %d (body=%v)", status, envelope)
		}
		if raw, _ := dataMap(t, envelope)["rankings"].([]any); len(raw) == 0 {
			t.Fatalf("赛季 2 榜为空：更季后结算未写入新赛季 %v", envelope)
		}
		status, envelope = doJSON(t, env, http.MethodGet, "/api/rankings/1v1", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("缺省赛季查询 status = %d (body=%v)", status, envelope)
		}
		rawDefault, _ := dataMap(t, envelope)["rankings"].([]any)
		if len(rawDefault) == 0 || jsonInt(t, rawDefault[0].(map[string]any), "season") != 2 {
			t.Fatalf("缺省赛季榜 = %v, want 跟随当前赛季 2", envelope)
		}
		status, envelope = doJSON(t, env, http.MethodGet, "/api/rankings/1v1?season=1", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("查赛季1榜 status = %d (body=%v)", status, envelope)
		}
		if raw, _ := dataMap(t, envelope)["rankings"].([]any); len(raw) == 0 {
			t.Fatalf("赛季 1 榜为空：旧赛季榜应保留 %v", envelope)
		}

		// 更季到 3 且显式重置：全员评分清零，新赛季从 0 开局。
		status, envelope = doJSON(t, env, http.MethodPost, "/api/seasons/roll", reporterToken,
			map[string]any{"target_season": 3, "reset_scores": true})
		if status != http.StatusCreated {
			t.Fatalf("重置更季 status = %d, want 201 (body=%v)", status, envelope)
		}
		if dataMap(t, envelope)["reset_scores"] != true {
			t.Fatalf("更季记录 reset_scores = %v, want true", envelope)
		}
		if score := jsonInt(t, getUserProfile(t, env, 101), "rank_score"); score != 0 {
			t.Fatalf("重置后 101 rank_score = %d, want 0", score)
		}
		status, envelope = doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, matchReport("e2e-uuid-1", "e2e-uuid-2"))
		if status != http.StatusCreated {
			t.Fatalf("赛季3上报 status = %d, want 201 (body=%v)", status, envelope)
		}
		parts := participantsFrom(t, envelope)
		if jsonInt(t, parts[101], "rank_score_before") != 0 {
			t.Errorf("赛季3胜者 before = %d, want 0（重置后从零开局）", jsonInt(t, parts[101], "rank_score_before"))
		}
		if jsonInt(t, parts[101], "rank_score_delta") <= 0 {
			t.Errorf("赛季3胜者 delta = %d, want 正增量", jsonInt(t, parts[101], "rank_score_delta"))
		}
	})
}
