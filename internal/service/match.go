package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	goredis "github.com/redis/go-redis/v9"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/rating"
	"MatchCoreArena-Server/internal/reportlog"
)

// 对局来源标记（matches.source）。
// 仅作来源标记使用，结算逻辑不据此分流。
const (
	MatchSourceGame    = "game"
	MatchSourceVirtual = "virtual"
)

// 队伍结果常量。
const (
	ResultWin  = "win"
	ResultLose = "lose"
	ResultDraw = "draw"
)

// Match 对战记录模型。
// Source 为来源标记（game/virtual），MapName/ServerID/GameID 为可选字段。
type Match struct {
	ID              int64   `json:"id"`
	MatchType       string  `json:"match_type"`
	Source          string  `json:"source"`
	MapName         string  `json:"map_name"`
	ServerID        string  `json:"server_id"`
	GameID          string  `json:"game_id"`
	WinnerTeamID    string  `json:"winner_team_id"`
	SurvivalRate    float64 `json:"survival_rate"`
	DurationSeconds int     `json:"duration_seconds"`
	StartedAt       string  `json:"started_at"`
	FinishedAt      string  `json:"finished_at"`
	CreatedAt       string  `json:"created_at"`

	// Participants 参与者明细；GetByID 与上报响应填充，列表接口为空。
	Participants []*ParticipantDetail `json:"participants,omitempty"`

	// SelfSummary 列表项的"本人视角"摘要（仅战绩列表接口填充，
	// 视角为列表主体玩家）；其余接口为空。
	SelfSummary *SelfSummary `json:"self_summary,omitempty"`
}

// SelfSummary 战绩列表项的本人视角摘要：本人所在队伍、胜负与分数变化。
type SelfSummary struct {
	TeamID         string `json:"team_id"`
	IsWinner       bool   `json:"is_winner"`
	IsDraw         bool   `json:"is_draw"`
	RankScoreDelta int    `json:"rank_score_delta"`
	RankScoreAfter int    `json:"rank_score_after"`
}

// ParticipantDetail 对战参与者明细（含本局评分变动），随对战详情返回。
type ParticipantDetail struct {
	UID             int64  `json:"uid"`
	TeamID          string `json:"team_id"`
	IsWinner        bool   `json:"is_winner"`
	IsDraw          bool   `json:"is_draw"`
	RankScoreBefore int    `json:"rank_score_before"`
	RankScoreDelta  int    `json:"rank_score_delta"`
	RankScoreAfter  int    `json:"rank_score_after"`
	SurvivalTime    int    `json:"survival_time"`
	Kills           int    `json:"kills"`
	Deaths          int    `json:"deaths"`
}

// TeamReportInput 对局上报参数（唯一上报入口）。
//
// 两种等价形状二选一：
//   - 统一形状：teams（每队 result 为 win/lose/draw，至多一队 win，
//     有 win 时不得与 draw 混用；允许全平局）；
//   - 兼容形状：winner_team + loser_teams（等价于 teams 的
//     win/lose 展开，保持既有上报方兼容）。
//
// game_id 为上报方提供的稳定幂等键：同一 game_id 重复上报不重复结算。
// matchmaking_group_id 关联匹配成局，用于队伍（party）共享结算。
type TeamReportInput struct {
	MatchType       string     `json:"match_type" binding:"required"`
	Teams           []TeamInfo `json:"teams"`
	WinnerTeam      TeamInfo   `json:"winner_team"`
	LoserTeams      []TeamInfo `json:"loser_teams"`
	DurationSeconds int        `json:"duration_seconds"`
	MapName         string     `json:"map_name"`
	ServerID        string     `json:"server_id"`
	GameID          string     `json:"game_id"`
	Source          string     `json:"source"`
	MatchmakingGroupID int64   `json:"matchmaking_group_id"`
	StartedAt       string     `json:"started_at" binding:"required"`
	FinishedAt      string     `json:"finished_at" binding:"required"`
}

// TeamInfo 队伍上报信息。字段必填性由 service 层 validateTeams 以
// 类型化错误校验（两种等价形状下 winner_team 可为零值，不能用
// binding:"required"）。
type TeamInfo struct {
	TeamID       string              `json:"team_id"`
	Result       string              `json:"result"`
	InitialCount int                 `json:"initial_count"`
	AliveCount   int                 `json:"alive_count"`
	Members      []ParticipantReport `json:"members"`
}

type ParticipantReport struct {
	UUID         string  `json:"uuid" binding:"required"`
	SurvivalTime int     `json:"survival_time"`
	Kills        int     `json:"kills"`
	Deaths       int     `json:"deaths"`
	PerfTweak    float64 `json:"perf_tweak"`
}

// Page 分页参数。
type Page struct {
	PageNum  int `json:"page_num" form:"page_num"`
	PageSize int `json:"page_size" form:"page_size"`
}

// MatchFilter 全局对局查询过滤条件（OPS 查询用）。
// 空值字段不参与过滤；FinishedFrom/FinishedTo 作用于 finished_at，
// 接受 RFC3339 或 "2006-01-02 15:04:05"（UTC）。
type MatchFilter struct {
	Source       string `form:"source"`
	MapName      string `form:"map_name"`
	GameID       string `form:"game_id"`
	UID          int64  `form:"uid"`
	FinishedFrom string `form:"finished_from"`
	FinishedTo   string `form:"finished_to"`
}

// MatchService 对战业务接口。
type MatchService interface {
	// Report 上报对局结果并完成定级结算。
	// 返回结算后的对局；第二个返回值为 true 表示命中 game_id 幂等
	// （同一 game_id 的重复上报，返回既有记录，不重复结算）。
	Report(ctx context.Context, reporterUID int64, in TeamReportInput) (*Match, bool, error)
	// GetByID 获取对战详情（含参与者明细）。
	GetByID(ctx context.Context, id int64) (*Match, error)
	// GetByGameID 按 game_id 查询对局（OPS 排查幂等重放/冲突用）；
	// 不存在时返回 mca_match_not_found。
	GetByGameID(ctx context.Context, gameID string) (*Match, error)
	// ListByUser 列出用户参与的对战历史（分页）。
	ListByUser(ctx context.Context, uid int64, page Page) ([]*Match, error)
	// List 按过滤条件全局查询对局（OPS），按 finished_at 倒序，
	// 含参与者明细（与单局详情同形状）。
	List(ctx context.Context, filter MatchFilter, page Page) ([]*Match, error)
}

// matchService 数据库实现。
type matchService struct {
	db         *sql.DB
	authClient *auth.Client
	redis      *goredis.Client
}

// NewMatchService 创建 MatchService 实例。
func NewMatchService(db *sql.DB, authClient *auth.Client, redis *goredis.Client) MatchService {
	return &matchService{
		db:         db,
		authClient: authClient,
		redis:      redis,
	}
}

// normalizeTeams 将上报载荷归一为带 result 的队伍列表。
func normalizeTeams(in TeamReportInput) ([]TeamInfo, error) {
	if len(in.Teams) > 0 {
		for i := range in.Teams {
			if in.Teams[i].Result == "" {
				return nil, apperr.New(apperr.CodeMCAInvalidRequest,
					fmt.Sprintf("teams[%d].result 必填（win/lose/draw）", i))
			}
		}
		return in.Teams, nil
	}
	if in.WinnerTeam.TeamID == "" && len(in.WinnerTeam.Members) == 0 {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest,
			"必须提供 teams，或 winner_team + loser_teams")
	}
	teams := make([]TeamInfo, 0, 1+len(in.LoserTeams))
	winner := in.WinnerTeam
	winner.Result = ResultWin
	teams = append(teams, winner)
	for i := range in.LoserTeams {
		loser := in.LoserTeams[i]
		loser.Result = ResultLose
		teams = append(teams, loser)
	}
	return teams, nil
}

// parseTimeValue 解析单个时间字段：接受 RFC3339 或
// "2006-01-02 15:04:05"（按 UTC 处理）。
func parseTimeValue(v, field string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", v, time.UTC); err == nil {
		return t, nil
	}
	return time.Time{}, apperr.New(apperr.CodeMCAInvalidRequest,
		fmt.Sprintf("%s 必须为 RFC3339 或 'YYYY-MM-DD HH:MM:SS' 格式", field))
}

// parseReportTimes 解析时间字段并校验结束不早于开始。
func parseReportTimes(started, finished string) (time.Time, time.Time, error) {
	startedAt, err := parseTimeValue(started, "started_at")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	finishedAt, err := parseTimeValue(finished, "finished_at")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if finishedAt.Before(startedAt) {
		return time.Time{}, time.Time{}, apperr.New(apperr.CodeMCAReportInconsistent,
			"finished_at 早于 started_at")
	}
	return startedAt, finishedAt, nil
}

// validateTeams 校验队伍数据的结构语义；自相矛盾的数据以
// mca_report_inconsistent 拒绝。
func validateTeams(teams []TeamInfo, durationSeconds int) error {
	if len(teams) < 2 {
		return apperr.New(apperr.CodeMCAInvalidRequest, "至少需要 2 支队伍")
	}
	if durationSeconds < 0 {
		return apperr.New(apperr.CodeMCAReportInconsistent, "duration_seconds 不能为负数")
	}
	seenUUID := make(map[string]bool)
	winners := 0
	for i := range teams {
		team := &teams[i]
		switch team.Result {
		case ResultWin:
			winners++
		case ResultLose, ResultDraw:
		default:
			return apperr.New(apperr.CodeMCAInvalidRequest,
				fmt.Sprintf("teams[%d].result 非法: %q（允许 win/lose/draw）", i, team.Result))
		}
		if len(team.Members) == 0 {
			return apperr.New(apperr.CodeMCAInvalidRequest,
				fmt.Sprintf("teams[%d].members 不能为空", i))
		}
		if team.InitialCount < 1 {
			return apperr.New(apperr.CodeMCAReportInconsistent,
				fmt.Sprintf("teams[%d].initial_count 必须 >= 1", i))
		}
		if team.AliveCount < 0 || team.AliveCount > team.InitialCount {
			return apperr.New(apperr.CodeMCAReportInconsistent,
				fmt.Sprintf("teams[%d].alive_count 必须在 [0, initial_count] 范围内", i))
		}
		for _, m := range team.Members {
			if m.Kills < 0 || m.Deaths < 0 || m.SurvivalTime < 0 {
				return apperr.New(apperr.CodeMCAReportInconsistent,
					fmt.Sprintf("参与者 %s 的 kills/deaths/survival_time 不能为负数", m.UUID))
			}
			// 与上报方前端校验同口径：perf_tweak 只接受未提供（0）或
			// [0.95, 1.05] 区间内的值，越界直接拒绝而非静默截断。
			if m.PerfTweak != 0 && (m.PerfTweak < 0.95 || m.PerfTweak > 1.05) {
				return apperr.New(apperr.CodeMCAReportInconsistent,
					fmt.Sprintf("参与者 %s 的 perf_tweak 必须在 [0.95, 1.05] 区间内（或省略）", m.UUID))
			}
			if seenUUID[m.UUID] {
				return apperr.New(apperr.CodeMCAReportInconsistent,
					fmt.Sprintf("参与者 uuid 重复: %s", m.UUID))
			}
			seenUUID[m.UUID] = true
		}
	}
	if winners > 1 {
		return apperr.New(apperr.CodeMCAReportInconsistent, "至多一支队伍的 result 可为 win")
	}
	if winners == 1 {
		for i := range teams {
			if teams[i].Result == ResultDraw {
				return apperr.New(apperr.CodeMCAReportInconsistent,
					"存在获胜队伍时，其余队伍 result 必须为 lose，不能与平局混用")
			}
		}
	}
	return nil
}

// payloadHash 计算上报载荷的规范化哈希（幂等冲突检测用）。
func payloadHash(in TeamReportInput, teams []TeamInfo, startedAt, finishedAt time.Time) string {
	type hMember struct {
		UUID         string  `json:"uuid"`
		SurvivalTime int     `json:"survival_time"`
		Kills        int     `json:"kills"`
		Deaths       int     `json:"deaths"`
		PerfTweak    float64 `json:"perf_tweak"`
	}
	type hTeam struct {
		TeamID       string    `json:"team_id"`
		Result       string    `json:"result"`
		InitialCount int       `json:"initial_count"`
		AliveCount   int       `json:"alive_count"`
		Members      []hMember `json:"members"`
	}
	norm := make([]hTeam, 0, len(teams))
	for _, t := range teams {
		members := make([]hMember, 0, len(t.Members))
		for _, m := range t.Members {
			members = append(members, hMember{UUID: m.UUID, SurvivalTime: m.SurvivalTime, Kills: m.Kills, Deaths: m.Deaths, PerfTweak: m.PerfTweak})
		}
		sort.Slice(members, func(i, j int) bool { return members[i].UUID < members[j].UUID })
		norm = append(norm, hTeam{TeamID: t.TeamID, Result: t.Result, InitialCount: t.InitialCount, AliveCount: t.AliveCount, Members: members})
	}
	sort.Slice(norm, func(i, j int) bool { return norm[i].TeamID < norm[j].TeamID })

	source := in.Source
	if source == "" {
		source = MatchSourceGame
	}
	doc := struct {
		MatchType       string  `json:"match_type"`
		DurationSeconds int     `json:"duration_seconds"`
		MapName         string  `json:"map_name"`
		ServerID        string  `json:"server_id"`
		Source          string  `json:"source"`
		StartedAt       string  `json:"started_at"`
		FinishedAt      string  `json:"finished_at"`
		Teams           []hTeam `json:"teams"`
	}{
		MatchType:       in.MatchType,
		DurationSeconds: in.DurationSeconds,
		MapName:         in.MapName,
		ServerID:        in.ServerID,
		Source:          source,
		StartedAt:       startedAt.Format(time.RFC3339),
		FinishedAt:      finishedAt.Format(time.RFC3339),
		Teams:           norm,
	}
	raw, _ := json.Marshal(doc)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// participantSettle 结算过程中单个参与者的中间状态。
type participantSettle struct {
	uuid     string
	member   ParticipantReport
	team     TeamInfo
	info     *userRatingInfo
	isWinner bool
	isDraw   bool
	delta    int
}

// Report 上报对局结果并完成定级结算（唯一结算入口）。
//
// 评分规则（internal/rating 加权 Elo，参数不变）：
//   - 恰好一队获胜：胜/败双方平均分求期望胜率，胜队生存权重作用于全部
//     参与者，连胜权重取自历史；
//   - 全平局：S=0.5，每队以本队平均分对对方全员平均分求期望胜率，
//     生存权重取本队自身，连胜清零。
//
// 幂等：提供 game_id 时，同一 game_id 且载荷哈希一致的重复上报返回既有
// 记录（不再结算）；载荷不一致以 409 mca_game_id_conflict 拒绝。
func (s *matchService) Report(ctx context.Context, reporterUID int64, in TeamReportInput) (match *Match, replayed bool, err error) {
	// 拒绝/冲突可观测：两类调试关注的拒绝进入有界内存环（脱敏后），
	// 供 GET /api/matches/rejections 只读暴露。
	defer func() {
		var ae *apperr.Error
		if errors.As(err, &ae) &&
			(ae.Code == apperr.CodeMCAReportInconsistent || ae.Code == apperr.CodeMCAGameIDConflict) {
			reportlog.Default.Record(ae.Code, in.GameID, ae.Message)
		}
	}()

	teams, err := normalizeTeams(in)
	if err != nil {
		return nil, false, err
	}
	if err := validateTeams(teams, in.DurationSeconds); err != nil {
		return nil, false, err
	}
	startedAt, finishedAt, err := parseReportTimes(in.StartedAt, in.FinishedAt)
	if err != nil {
		return nil, false, err
	}
	// 与上报方前端校验同口径：提供了时长则必须与起止时刻差一致。
	if in.DurationSeconds > 0 && int64(in.DurationSeconds) != int64(finishedAt.Sub(startedAt)/time.Second) {
		return nil, false, apperr.New(apperr.CodeMCAReportInconsistent,
			"duration_seconds 与 finished_at - started_at 不一致")
	}

	source := in.Source
	if source == "" {
		source = MatchSourceGame
	}
	if source != MatchSourceGame && source != MatchSourceVirtual {
		return nil, false, apperr.New(apperr.CodeMCAInvalidRequest,
			fmt.Sprintf("source 非法: %q（允许 game/virtual）", source))
	}
	if in.MatchmakingGroupID < 0 {
		return nil, false, apperr.New(apperr.CodeMCAInvalidRequest, "matchmaking_group_id 不能为负数")
	}

	var hash string
	if in.GameID != "" {
		hash = payloadHash(in, teams, startedAt, finishedAt)
		// 幂等预检（事务外快速路径；并发竞态由唯一键兜底）。
		existing, existingHash, found, err := s.findByGameID(ctx, in.GameID)
		if err != nil {
			return nil, false, err
		}
		if found {
			if existingHash == hash {
				return existing, true, nil
			}
			return nil, false, apperr.New(apperr.CodeMCAGameIDConflict,
				fmt.Sprintf("game_id %s 已存在且载荷不一致", in.GameID))
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	// 1. 解析全部参与者身份并读取评分信息（uid 只来自 HRPAuth 解析结果，
	// 缺行时在事务内懒建行）。
	allParticipants := make(map[string]*userRatingInfo) // key: UUID
	for _, team := range teams {
		for _, m := range team.Members {
			uid, err := s.resolveUID(ctx, m.UUID)
			if err != nil {
				switch {
				case errors.Is(err, auth.ErrUserNotFound):
					return nil, false, apperr.New(apperr.CodeMCAUserNotFound,
						fmt.Sprintf("参与者 (uuid: %s) 在 HRPAuth 中不存在", m.UUID))
				case errors.Is(err, auth.ErrServiceAuth):
					return nil, false, apperr.New(apperr.CodeOAuthHRPAuthDown,
						"HRPAuth 服务凭据不可用，无法解析参与者身份")
				default:
					return nil, false, apperr.New(apperr.CodeOAuthHRPAuthDown,
						fmt.Sprintf("解析参与者 (uuid: %s) 身份失败", m.UUID))
				}
			}
			info, err := s.getUserRatingInfo(ctx, tx, uid)
			if err != nil {
				return nil, false, err
			}
			info.UID = uid
			allParticipants[m.UUID] = info
		}
	}

	// 2. 记录对战主表。
	winnerIdx := -1
	for i := range teams {
		if teams[i].Result == ResultWin {
			winnerIdx = i
			break
		}
	}
	winnerTeamID := ""
	survivalRate := 0.0
	if winnerIdx >= 0 {
		w := &teams[winnerIdx]
		winnerTeamID = w.TeamID
		survivalRate = float64(w.AliveCount) / float64(w.InitialCount)
	}
	var gameIDArg, hashArg any
	if in.GameID != "" {
		gameIDArg, hashArg = in.GameID, hash
	}
	// matchmaking_group_id 携带时落库（成局是否已结算的判定依据，
	// 契约 1.9.0 的代理路由端点用）；未携带写 NULL，既有行为不变。
	var groupIDArg any
	if in.MatchmakingGroupID > 0 {
		groupIDArg = in.MatchmakingGroupID
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO matches (match_type, source, map_name, server_id, game_id, payload_hash, matchmaking_group_id, winner_team_id, survival_rate, duration_seconds, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.MatchType, source, in.MapName, in.ServerID, gameIDArg, hashArg, groupIDArg, winnerTeamID, survivalRate,
		in.DurationSeconds, startedAt, finishedAt,
	)
	if err != nil {
		var me *gomysql.MySQLError
		if in.GameID != "" && errors.As(err, &me) && me.Number == 1062 {
			// 并发竞态：另一请求先写入了同一 game_id。
			tx.Rollback()
			existing, existingHash, found, rerr := s.findByGameID(ctx, in.GameID)
			if rerr != nil {
				return nil, false, rerr
			}
			if found && existingHash == hash {
				return existing, true, nil
			}
			return nil, false, apperr.New(apperr.CodeMCAGameIDConflict,
				fmt.Sprintf("game_id %s 已存在且载荷不一致", in.GameID))
		}
		return nil, false, fmt.Errorf("记录对战失败: %w", err)
	}
	matchID, _ := res.LastInsertId()

	// 3. 计算各队平均分。
	teamAvg := make([]float64, len(teams))
	for i := range teams {
		ratings := make([]float64, 0, len(teams[i].Members))
		for _, m := range teams[i].Members {
			ratings = append(ratings, float64(allParticipants[m.UUID].RankScore))
		}
		teamAvg[i] = rating.CalculateTeamAverageRating(ratings)
	}

	// 4. 计算每个参与者的积分变动（先算后写，便于队伍共享结算）。
	settles := make([]participantSettle, 0, len(allParticipants))
	if winnerIdx >= 0 {
		// 有获胜队：胜队平均分对败方全员平均分求期望，胜队生存权重
		// 作用于全部参与者。
		loserRatings := make([]float64, 0)
		for i := range teams {
			if i == winnerIdx {
				continue
			}
			for _, m := range teams[i].Members {
				loserRatings = append(loserRatings, float64(allParticipants[m.UUID].RankScore))
			}
		}
		avgLoser := rating.CalculateTeamAverageRating(loserRatings)
		expectedW := rating.CalculateEloExpected(teamAvg[winnerIdx], avgLoser)
		expectedL := rating.CalculateEloExpected(avgLoser, teamAvg[winnerIdx])
		wRound := rating.GetSurvivalWeight(teams[winnerIdx].AliveCount, teams[winnerIdx].InitialCount)

		for i := range teams {
			isWinner := i == winnerIdx
			for _, m := range teams[i].Members {
				info := allParticipants[m.UUID]
				streak, err := s.getUserStreak(ctx, tx, info.UID)
				if err != nil {
					return nil, false, err
				}
				newStreak := streak
				if isWinner {
					if streak >= 0 {
						newStreak++
					} else {
						newStreak = 1
					}
				} else {
					if streak <= 0 {
						newStreak--
					} else {
						newStreak = -1
					}
				}

				k := rating.GetKFactor(info.WinsCount)
				sValue, eValue := 0.0, expectedL
				if isWinner {
					sValue, eValue = 1.0, expectedW
				}
				wStreak := rating.GetStreakWeight(newStreak)
				wPerf := rating.GetIndividualPerformanceTweak(m.PerfTweak)
				if wPerf == 0 {
					wPerf = 1.0
				}
				delta := rating.CalculateDelta(k, sValue, eValue, wRound, wStreak, wPerf)
				settles = append(settles, participantSettle{
					uuid: m.UUID, member: m, team: teams[i], info: info,
					isWinner: isWinner, delta: delta,
				})
			}
		}
	} else {
		// 全平局：S=0.5，每队对对方全员平均分求期望，生存权重取本队自身，
		// 连胜清零（不查询历史），不计胜场。
		totalMembers := 0
		for i := range teams {
			totalMembers += len(teams[i].Members)
		}
		for i := range teams {
			oppSum := 0.0
			for j := range teams {
				if j == i {
					continue
				}
				for _, m := range teams[j].Members {
					oppSum += float64(allParticipants[m.UUID].RankScore)
				}
			}
			oppAvg := 0.0
			if oppCount := totalMembers - len(teams[i].Members); oppCount > 0 {
				oppAvg = oppSum / float64(oppCount)
			}
			eValue := rating.CalculateEloExpected(teamAvg[i], oppAvg)
			wRound := rating.GetSurvivalWeight(teams[i].AliveCount, teams[i].InitialCount)

			for _, m := range teams[i].Members {
				info := allParticipants[m.UUID]
				k := rating.GetKFactor(info.WinsCount)
				delta := rating.CalculateDelta(k, 0.5, eValue, wRound, rating.GetStreakWeight(0), 1.0)
				settles = append(settles, participantSettle{
					uuid: m.UUID, member: m, team: teams[i], info: info,
					isDraw: true, delta: delta,
				})
			}
		}
	}

	// 5. 队伍（party）共享结算：关联匹配成局时，同一 party 的成员共享
	// 平均后的积分变动。
	if in.MatchmakingGroupID > 0 {
		if err := s.applyPartySharing(ctx, tx, in.MatchmakingGroupID, settles); err != nil {
			return nil, false, err
		}
	}

	// 6. 应用积分变动并写入参与者明细/排行榜。
	details := make([]*ParticipantDetail, 0, len(settles))
	for _, p := range settles {
		scoreUpdate := "rank_score = GREATEST(0, rank_score + ?)"
		winsUpdate := ""
		if p.isWinner {
			winsUpdate = ", wins_count = wins_count + 1"
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE users SET %s %s WHERE uid = ?", scoreUpdate, winsUpdate),
			p.delta, p.info.UID,
		); err != nil {
			return nil, false, fmt.Errorf("更新用户积分失败 (uid: %d): %w", p.info.UID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO match_participants (match_id, user_uid, team_id, is_winner, is_draw, rank_score_before, rank_score_delta, survival_time_seconds, kills, deaths)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			matchID, p.info.UID, p.team.TeamID, p.isWinner, p.isDraw, p.info.RankScore, p.delta,
			p.member.SurvivalTime, p.member.Kills, p.member.Deaths,
		); err != nil {
			return nil, false, fmt.Errorf("记录参与者失败 (uid: %d): %w", p.info.UID, err)
		}
		if err := s.updateRanking(ctx, tx, p.info.UID, in.MatchType, 1); err != nil {
			return nil, false, err
		}
		after := p.info.RankScore + p.delta
		if after < 0 {
			after = 0
		}
		details = append(details, &ParticipantDetail{
			UID: p.info.UID, TeamID: p.team.TeamID, IsWinner: p.isWinner, IsDraw: p.isDraw,
			RankScoreBefore: p.info.RankScore, RankScoreDelta: p.delta, RankScoreAfter: after,
			SurvivalTime: p.member.SurvivalTime, Kills: p.member.Kills, Deaths: p.member.Deaths,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("提交事务失败: %w", err)
	}

	return &Match{
		ID:              matchID,
		MatchType:       in.MatchType,
		Source:          source,
		MapName:         in.MapName,
		ServerID:        in.ServerID,
		GameID:          in.GameID,
		WinnerTeamID:    winnerTeamID,
		SurvivalRate:    survivalRate,
		DurationSeconds: in.DurationSeconds,
		StartedAt:       startedAt.Format(time.RFC3339),
		FinishedAt:      finishedAt.Format(time.RFC3339),
		CreatedAt:       time.Now().Format(time.RFC3339),
		Participants:    details,
	}, false, nil
}

// findByGameID 按 game_id 查找既有对局。
func (s *matchService) findByGameID(ctx context.Context, gameID string) (*Match, string, bool, error) {
	var id int64
	var hash sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT id, payload_hash FROM matches WHERE game_id = ?", gameID,
	).Scan(&id, &hash)
	if err == sql.ErrNoRows {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("查询 game_id 失败: %w", err)
	}
	m, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, "", false, err
	}
	return m, hash.String, true, nil
}

// applyPartySharing 按匹配成局中的 party 归属共享积分变动：
// 同一 party（≥2 名成员参与本局）的成员统一为该 party 成员变动的平均值。
func (s *matchService) applyPartySharing(ctx context.Context, tx *sql.Tx, groupID int64, settles []participantSettle) error {
	var count int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM matchmaking_groups WHERE id = ?", groupID,
	).Scan(&count); err != nil {
		return fmt.Errorf("查询匹配成局失败: %w", err)
	}
	if count == 0 {
		return apperr.New(apperr.CodeMCAInvalidRequest,
			fmt.Sprintf("matchmaking_group_id %d 不存在", groupID))
	}

	rows, err := tx.QueryContext(ctx,
		"SELECT user_uid, party_id FROM matchmaking_group_members WHERE group_id = ? AND party_id IS NOT NULL",
		groupID,
	)
	if err != nil {
		return fmt.Errorf("查询成局成员失败: %w", err)
	}
	defer rows.Close()
	partyByUID := make(map[int64]int64)
	for rows.Next() {
		var uid, partyID int64
		if err := rows.Scan(&uid, &partyID); err != nil {
			return fmt.Errorf("解析成局成员失败: %w", err)
		}
		partyByUID[uid] = partyID
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历成局成员失败: %w", err)
	}
	if len(partyByUID) == 0 {
		return nil
	}

	// 按 party 聚合变动索引。
	partyMembers := make(map[int64][]int) // party_id → settles 下标
	for i := range settles {
		if partyID, ok := partyByUID[settles[i].info.UID]; ok {
			partyMembers[partyID] = append(partyMembers[partyID], i)
		}
	}
	for _, idxs := range partyMembers {
		if len(idxs) < 2 {
			continue
		}
		sum := 0
		for _, i := range idxs {
			sum += settles[i].delta
		}
		// 平均值按远离零方向取整，保证同 party 成员共享同一值。
		n := len(idxs)
		var shared int
		if sum >= 0 {
			shared = (sum + n/2) / n
		} else {
			shared = (sum - n/2) / n
		}
		for _, i := range idxs {
			settles[i].delta = shared
		}
	}
	return nil
}

type userRatingInfo struct {
	UID       int64
	RankScore int
	WinsCount int
}

func (s *matchService) resolveUID(ctx context.Context, uuid string) (int64, error) {
	uid, err := resolveUUID(ctx, s.authClient, s.redis, uuid)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return 0, fmt.Errorf("用户不存在: %w", err)
		}
		if errors.Is(err, auth.ErrServiceAuth) {
			return 0, fmt.Errorf("服务身份不可用: %w", err)
		}
		return 0, fmt.Errorf("调用 HRPAuth 失败: %w", err)
	}
	return uid, nil
}

func (s *matchService) getUserRatingInfo(ctx context.Context, tx *sql.Tx, uid int64) (*userRatingInfo, error) {
	info, err := queryUserRating(ctx, tx, uid)
	if err == nil {
		return info, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	// 懒初始化（作者决策，见 knowledge 0003）：本地缺行时在结算事务内
	// 自动建行后继续结算。
	// 信任边界：uid 只来自本次请求已通过校验的令牌 / HRPAuth 解析结果
	//（resolveUID），上报载荷不携带也无法自填 uid；建行只写 uid，
	// rating/wins 等全部取表默认值，上报方不可指定。
	if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO users (uid) VALUES (?)", uid); err != nil {
		return nil, fmt.Errorf("懒建行失败 (uid: %d): %w", uid, err)
	}

	info, err = queryUserRating(ctx, tx, uid)
	if err != nil {
		if err == sql.ErrNoRows {
			// INSERT IGNORE 被忽略（如 uid 被软删除行占用）→ 建行不可能：
			// 类型化错误供运维定位，不与真内部故障混同。
			return nil, apperr.New(apperr.CodeMCAUserNotProvisioned,
				fmt.Sprintf("参与者 (uid: %d) users 行无法建立（uid 可能被已删除账户占用）", uid))
		}
		return nil, err
	}
	return info, nil
}

// queryUserRating 读取 uid 的评分信息；无行时返回 sql.ErrNoRows。
func queryUserRating(ctx context.Context, tx *sql.Tx, uid int64) (*userRatingInfo, error) {
	var info userRatingInfo
	err := tx.QueryRowContext(ctx, "SELECT rank_score, wins_count FROM users WHERE uid = ? AND deleted_at IS NULL", uid).Scan(&info.RankScore, &info.WinsCount)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

func (s *matchService) getUserStreak(ctx context.Context, tx *sql.Tx, uid int64) (int, error) {
	// 获取最近 10 场对战结果来计算连胜/连败。
	// 团队对战的胜负按玩家记录在 match_participants（matches 主表的
	// winner_uid/loser_uid 不随上报写入），因此以参与者记录为准。
	// 平局终止连胜/连败：遇到 is_draw 行即停止计数。
	rows, err := tx.QueryContext(ctx,
		`SELECT mp.is_winner, mp.is_draw
		 FROM match_participants mp
		 JOIN matches m ON mp.match_id = m.id
		 WHERE mp.user_uid = ?
		 ORDER BY m.created_at DESC, mp.id DESC LIMIT 10`,
		uid,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	streak := 0
	isFirst := true
	winning := false

	for rows.Next() {
		var isWinner, isDraw bool
		if err := rows.Scan(&isWinner, &isDraw); err != nil {
			return 0, err
		}

		if isDraw {
			// 平局不属于任何一侧：连胜/连败清零并终止计数。
			break
		}

		isWin := isWinner
		if isFirst {
			winning = isWin
			streak = 1
			isFirst = false
			continue
		}

		if isWin == winning {
			streak++
		} else {
			break
		}
	}

	if !winning {
		streak = -streak
	}
	return streak, nil
}

func (s *matchService) updateRanking(ctx context.Context, tx *sql.Tx, uid int64, rankType string, season int) error {
	// 获取最新积分
	var score int
	err := tx.QueryRowContext(ctx, "SELECT rank_score FROM users WHERE uid = ?", uid).Scan(&score)
	if err != nil {
		return err
	}

	// 更新或插入排行榜记录
	_, err = tx.ExecContext(ctx,
		`INSERT INTO rankings (user_uid, rank_type, score, season) 
		 VALUES (?, ?, ?, ?) 
		 ON DUPLICATE KEY UPDATE score = VALUES(score), updated_at = CURRENT_TIMESTAMP`,
		uid, rankType, score, season,
	)
	return err
}

// GetByID 获取对战详情（含参与者明细与各自的评分变动）。
func (s *matchService) GetByID(ctx context.Context, id int64) (*Match, error) {
	var m Match
	var mapName, serverID, gameID, winnerTeamID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, match_type, source, map_name, server_id, game_id, winner_team_id, survival_rate, duration_seconds, started_at, finished_at, created_at
		 FROM matches WHERE id = ?`,
		id,
	).Scan(&m.ID, &m.MatchType, &m.Source, &mapName, &serverID, &gameID, &winnerTeamID, &m.SurvivalRate, &m.DurationSeconds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperr.New(apperr.CodeMCAMatchNotFound, fmt.Sprintf("对战记录 (id: %d) 不存在", id))
		}
		return nil, fmt.Errorf("查询对战详情失败: %w", err)
	}
	m.MapName = mapName.String
	m.ServerID = serverID.String
	m.GameID = gameID.String
	m.WinnerTeamID = winnerTeamID.String

	participants, err := s.loadParticipants(ctx, id)
	if err != nil {
		return nil, err
	}
	m.Participants = participants
	return &m, nil
}

// loadParticipants 读取指定对局的参与者明细（含评分变动）。
func (s *matchService) loadParticipants(ctx context.Context, matchID int64) ([]*ParticipantDetail, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_uid, team_id, is_winner, is_draw, rank_score_before, rank_score_delta, survival_time_seconds, kills, deaths
		 FROM match_participants WHERE match_id = ? ORDER BY id`,
		matchID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询对战参与者失败: %w", err)
	}
	defer rows.Close()

	participants := []*ParticipantDetail{}
	for rows.Next() {
		var p ParticipantDetail
		if err := rows.Scan(&p.UID, &p.TeamID, &p.IsWinner, &p.IsDraw, &p.RankScoreBefore, &p.RankScoreDelta, &p.SurvivalTime, &p.Kills, &p.Deaths); err != nil {
			return nil, fmt.Errorf("解析对战参与者失败: %w", err)
		}
		p.RankScoreAfter = p.RankScoreBefore + p.RankScoreDelta
		if p.RankScoreAfter < 0 {
			p.RankScoreAfter = 0
		}
		participants = append(participants, &p)
	}
	return participants, rows.Err()
}

// ListByUser 列出用户参与的对战历史（分页）。每条附带该用户视角的
// SelfSummary（本人所在队伍、胜负、分数变化）。
func (s *matchService) ListByUser(ctx context.Context, uid int64, page Page) ([]*Match, error) {
	offset := (page.PageNum - 1) * page.PageSize
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.match_type, m.source, m.map_name, m.server_id, m.game_id, m.winner_team_id, m.survival_rate, m.duration_seconds, m.started_at, m.finished_at, m.created_at,
		        mp.team_id, mp.is_winner, mp.is_draw, mp.rank_score_before, mp.rank_score_delta
		 FROM matches m
		 JOIN match_participants mp ON m.id = mp.match_id
		 WHERE mp.user_uid = ?
		 ORDER BY m.created_at DESC LIMIT ? OFFSET ?`,
		uid, page.PageSize, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("查询对战历史失败: %w", err)
	}
	defer rows.Close()

	matches := []*Match{}
	for rows.Next() {
		var m Match
		var mapName, serverID, gameID, winnerTeamID sql.NullString
		var self SelfSummary
		var scoreBefore int
		if err := rows.Scan(&m.ID, &m.MatchType, &m.Source, &mapName, &serverID, &gameID, &winnerTeamID, &m.SurvivalRate, &m.DurationSeconds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt,
			&self.TeamID, &self.IsWinner, &self.IsDraw, &scoreBefore, &self.RankScoreDelta); err != nil {
			return nil, fmt.Errorf("解析对战记录失败: %w", err)
		}
		m.MapName = mapName.String
		m.ServerID = serverID.String
		m.GameID = gameID.String
		m.WinnerTeamID = winnerTeamID.String
		self.RankScoreAfter = scoreBefore + self.RankScoreDelta
		if self.RankScoreAfter < 0 {
			self.RankScoreAfter = 0
		}
		m.SelfSummary = &self
		matches = append(matches, &m)
	}
	return matches, nil
}

// GetByGameID 按 game_id 查询对局（OPS 排查幂等重放/冲突用）。
func (s *matchService) GetByGameID(ctx context.Context, gameID string) (*Match, error) {
	if gameID == "" {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest, "game_id 不能为空")
	}
	m, _, found, err := s.findByGameID(ctx, gameID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, apperr.New(apperr.CodeMCAMatchNotFound,
			fmt.Sprintf("对战记录 (game_id: %s) 不存在", gameID))
	}
	return m, nil
}

// List 按过滤条件全局查询对局（OPS 查询用）：source/map_name/game_id
// 精确匹配、uid 按参与者过滤、finished_from/finished_to 作用于
// finished_at 区间；按 finished_at 倒序（其次 id 倒序），含参与者明细。
func (s *matchService) List(ctx context.Context, filter MatchFilter, page Page) ([]*Match, error) {
	where := []string{}
	args := []any{}
	if filter.Source != "" {
		where = append(where, "m.source = ?")
		args = append(args, filter.Source)
	}
	if filter.MapName != "" {
		where = append(where, "m.map_name = ?")
		args = append(args, filter.MapName)
	}
	if filter.GameID != "" {
		where = append(where, "m.game_id = ?")
		args = append(args, filter.GameID)
	}
	if filter.UID != 0 {
		where = append(where,
			"EXISTS (SELECT 1 FROM match_participants mp WHERE mp.match_id = m.id AND mp.user_uid = ?)")
		args = append(args, filter.UID)
	}
	if filter.FinishedFrom != "" {
		t, err := parseTimeValue(filter.FinishedFrom, "finished_from")
		if err != nil {
			return nil, err
		}
		where = append(where, "m.finished_at >= ?")
		args = append(args, t)
	}
	if filter.FinishedTo != "" {
		t, err := parseTimeValue(filter.FinishedTo, "finished_to")
		if err != nil {
			return nil, err
		}
		where = append(where, "m.finished_at <= ?")
		args = append(args, t)
	}

	query := `SELECT m.id, m.match_type, m.source, m.map_name, m.server_id, m.game_id, m.winner_team_id, m.survival_rate, m.duration_seconds, m.started_at, m.finished_at, m.created_at
		 FROM matches m`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY m.finished_at DESC, m.id DESC LIMIT ? OFFSET ?"
	offset := (page.PageNum - 1) * page.PageSize
	args = append(args, page.PageSize, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询对战列表失败: %w", err)
	}
	defer rows.Close()

	matches := []*Match{}
	for rows.Next() {
		var m Match
		var mapName, serverID, gameID, winnerTeamID sql.NullString
		if err := rows.Scan(&m.ID, &m.MatchType, &m.Source, &mapName, &serverID, &gameID, &winnerTeamID, &m.SurvivalRate, &m.DurationSeconds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("解析对战记录失败: %w", err)
		}
		m.MapName = mapName.String
		m.ServerID = serverID.String
		m.GameID = gameID.String
		m.WinnerTeamID = winnerTeamID.String
		matches = append(matches, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历对战列表失败: %w", err)
	}

	// 与单局详情同形状：逐局加载参与者明细（OPS 调试页，页长有界）。
	for _, m := range matches {
		participants, err := s.loadParticipants(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		m.Participants = participants
	}
	return matches, nil
}
