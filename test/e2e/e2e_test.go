//go:build e2e

// Package e2e_test 是不依赖真实游戏的端到端验证：真实 MariaDB +
// 完整路由/中间件/结算事务 + 内嵌 HRPAuth stub。
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
// 105/106 不参与任何对局，保持 0 分用于匹配闭环（分差 0 在基础窗口内，
// 引擎首个 tick 即可成局）。
var e2eUsers = map[string]int64{
	"e2e-uuid-1": 101,
	"e2e-uuid-2": 102,
	"e2e-uuid-3": 103,
	"e2e-uuid-4": 104,
	"e2e-uuid-5": 105,
	"e2e-uuid-6": 106,
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

	// tokens.yaml：match-reporter 静态服务令牌（真实部署同构）。
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
		Version:        config.CurrentVersion,
		VirtualMatches: config.VirtualMatchesConfig{Enabled: true},
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
		},
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

// virtualMatch 构造一局 1v1 虚拟对局。
func virtualMatch(winnerUUID, loserUUID string) map[string]any {
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

// TestVirtualMatchE2E 端到端：虚拟对局 → 定级结算 → 查询 → 匹配成局。
func TestVirtualMatchE2E(t *testing.T) {
	env := setupEnv(t)

	t.Run("status endpoint", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodGet, "/api/status", "", nil)
		if status != http.StatusOK || envelope["success"] != true {
			t.Fatalf("status = %d, envelope=%v", status, envelope)
		}
	})

	t.Run("virtual match without scope is rejected", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches/virtual", userToken(101), virtualMatch("e2e-uuid-1", "e2e-uuid-2"))
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
	t.Run("first virtual match settles ratings", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches/virtual", reporterToken, virtualMatch("e2e-uuid-1", "e2e-uuid-2"))
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%v)", status, envelope)
		}
		data := dataMap(t, envelope)
		match := data["match"].(map[string]any)
		match1ID = jsonInt(t, match, "id")
		if match["source"] != "virtual" || match["map_name"] != "e2e-arena-1" {
			t.Errorf("match source/map = %v/%v, want virtual/e2e-arena-1", match["source"], match["map_name"])
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

	t.Run("match detail carries participants and source", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodGet, fmt.Sprintf("/api/matches/%d", match1ID), userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		data := dataMap(t, envelope)
		if data["source"] != "virtual" {
			t.Errorf("source = %v, want virtual", data["source"])
		}
		parts, ok := data["participants"].([]any)
		if !ok || len(parts) != 2 {
			t.Fatalf("participants = %v, want 2 entries", data["participants"])
		}
	})

	// 批量灌入：再提交 4 局（交替胜负），逐局断言 before 与上一局
	// after 衔接、after = max(0, before+delta)。
	t.Run("batch virtual matches stay consistent", func(t *testing.T) {
		tracked := map[int64]int64{101: 325, 102: 0}
		for i := 0; i < 4; i++ {
			winner, loser := "e2e-uuid-2", "e2e-uuid-1"
			if i%2 == 0 {
				winner, loser = "e2e-uuid-1", "e2e-uuid-2"
			}
			status, envelope := doJSON(t, env, http.MethodPost, "/api/matches/virtual", reporterToken, virtualMatch(winner, loser))
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
	t.Run("draw match settles symmetrically", func(t *testing.T) {
		body := map[string]any{
			"match_type": "1v1",
			"teams": []map[string]any{
				{"team_id": "A", "result": "draw", "initial_count": 1, "alive_count": 1,
					"members": []map[string]any{{"uuid": "e2e-uuid-3"}}},
				{"team_id": "B", "result": "draw", "initial_count": 1, "alive_count": 1,
					"members": []map[string]any{{"uuid": "e2e-uuid-4"}}},
			},
		}
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches/virtual", reporterToken, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%v)", status, envelope)
		}
		data := dataMap(t, envelope)
		match := data["match"].(map[string]any)
		if match["winner_team_id"] != "" {
			t.Errorf("winner_team_id = %v, want empty for draw", match["winner_team_id"])
		}
		for uid, p := range participantsFrom(t, envelope) {
			if p["result"] != "draw" {
				t.Errorf("uid %d result = %v, want draw", uid, p["result"])
			}
			if jsonInt(t, p, "rank_score_delta") != 0 {
				t.Errorf("uid %d delta = %d, want 0 for equal-rating draw", uid, jsonInt(t, p, "rank_score_delta"))
			}
		}
	})

	// 游戏上报路径回归：match:report 令牌的 POST /api/matches 不受影响。
	var gameMatchID int64
	t.Run("game report path unchanged", func(t *testing.T) {
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
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matches", reporterToken, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%v)", status, envelope)
		}
		gameMatchID = jsonInt(t, dataMap(t, envelope), "id")

		status, envelope = doJSON(t, env, http.MethodGet, fmt.Sprintf("/api/matches/%d", gameMatchID), userToken(103), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		if dataMap(t, envelope)["source"] != "game" {
			t.Errorf("source = %v, want game", dataMap(t, envelope)["source"])
		}
	})

	// 排行榜：rank_type=1v1 与 users.rank_score 一致。
	t.Run("rankings reflect settled scores", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodGet, "/api/rankings/1v1", userToken(101), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%v)", status, envelope)
		}
		raw, ok := envelope["data"].([]any)
		if !ok || len(raw) == 0 {
			t.Fatalf("rankings 为空: %v", envelope)
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

	// 匹配闭环：入队 → 引擎成局 → 成局记录可查。
	// 用 0 分的 105/106（分差在基础窗口内，首个 tick 即成局）。
	t.Run("matchmaking forms a group", func(t *testing.T) {
		status, envelope := doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(105), map[string]any{"match_type": "1v1"})
		if status != http.StatusCreated {
			t.Fatalf("join 105 status = %d, want 201 (body=%v)", status, envelope)
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

	// 入队→主动离开。
	t.Run("join then leave", func(t *testing.T) {
		status, _ := doJSON(t, env, http.MethodPost, "/api/matchmaking/join", userToken(103), map[string]any{"match_type": "1v1"})
		if status != http.StatusCreated {
			t.Fatalf("join status = %d, want 201", status)
		}
		status, envelope := doJSON(t, env, http.MethodDelete, "/api/matchmaking/leave", userToken(103), nil)
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
}
