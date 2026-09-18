package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/rating"
)

// Match 对战记录模型。
type Match struct {
	ID              int64   `json:"id"`
	MatchType       string  `json:"match_type"`
	WinnerTeamID    string  `json:"winner_team_id"`
	SurvivalRate    float64 `json:"survival_rate"`
	DurationSeconds int     `json:"duration_seconds"`
	StartedAt       string  `json:"started_at"`
	FinishedAt      string  `json:"finished_at"`
	CreatedAt       string  `json:"created_at"`
}

// Participant 对战参与者模型。
type Participant struct {
	UID             int64   `json:"uid"`
	TeamID          string  `json:"team_id"`
	IsWinner        bool    `json:"is_winner"`
	RankScoreBefore int     `json:"rank_score_before"`
	RankScoreDelta  int     `json:"rank_score_delta"`
	SurvivalTime    int     `json:"survival_time"`
	Kills           int     `json:"kills"`
	Deaths          int     `json:"deaths"`
	PerfTweak       float64 `json:"perf_tweak"` // 个人表现微调系数 [0.95, 1.05]
}

// TeamReportInput 团队对战上报参数。
type TeamReportInput struct {
	MatchType       string     `json:"match_type" binding:"required"`
	WinnerTeam      TeamInfo   `json:"winner_team" binding:"required"`
	LoserTeams      []TeamInfo `json:"loser_teams" binding:"required"`
	DurationSeconds int        `json:"duration_seconds"`
	StartedAt       string     `json:"started_at" binding:"required"`
	FinishedAt      string     `json:"finished_at" binding:"required"`
}

type TeamInfo struct {
	TeamID       string              `json:"team_id" binding:"required"`
	InitialCount int                 `json:"initial_count"`
	AliveCount   int                 `json:"alive_count"`
	Members      []ParticipantReport `json:"members" binding:"required"`
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

// MatchService 对战业务接口。
type MatchService interface {
	// Report 上报团队对战结果。
	Report(ctx context.Context, reporterUID int64, in TeamReportInput) (*Match, error)
	// GetByID 获取对战详情。
	GetByID(ctx context.Context, id int64) (*Match, error)
	// ListByUser 列出用户参与的对战历史（分页）。
	ListByUser(ctx context.Context, uid int64, page Page) ([]*Match, error)
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

// Report 上报团队对战结果。
func (s *matchService) Report(ctx context.Context, reporterUID int64, in TeamReportInput) (*Match, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	// 1. 获取所有参与者的基本信息和 Elo
	allParticipants := make(map[string]*userRatingInfo) // key: UUID
	allMembers := append([]ParticipantReport{}, in.WinnerTeam.Members...)
	for _, team := range in.LoserTeams {
		allMembers = append(allMembers, team.Members...)
	}

	for _, m := range allMembers {
		uid, err := s.resolveUID(ctx, m.UUID)
		if err != nil {
			return nil, fmt.Errorf("解析用户 UUID (%s) 失败: %w", m.UUID, err)
		}

		info, err := s.getUserRatingInfo(ctx, tx, uid)
		if err != nil {
			return nil, err
		}
		info.UID = uid
		allParticipants[m.UUID] = info
	}

	// 2. 计算团队平均 Elo
	winnerRatings := make([]float64, 0, len(in.WinnerTeam.Members))
	for _, m := range in.WinnerTeam.Members {
		winnerRatings = append(winnerRatings, float64(allParticipants[m.UUID].RankScore))
	}
	avgWinnerElo := rating.CalculateTeamAverageRating(winnerRatings)

	loserRatings := make([]float64, 0)
	for _, team := range in.LoserTeams {
		for _, m := range team.Members {
			loserRatings = append(loserRatings, float64(allParticipants[m.UUID].RankScore))
		}
	}
	avgLoserElo := rating.CalculateTeamAverageRating(loserRatings)

	// 3. 计算权重
	// 期望胜率 (胜队 vs 负队平均)
	expectedW := rating.CalculateEloExpected(avgWinnerElo, avgLoserElo)
	expectedL := rating.CalculateEloExpected(avgLoserElo, avgWinnerElo)

	// 回合权重 (基于获胜队生存比例)
	wRound := rating.GetSurvivalWeight(in.WinnerTeam.AliveCount, in.WinnerTeam.InitialCount)

	// 4. 记录对战主表
	res, err := tx.ExecContext(ctx,
		`INSERT INTO matches (match_type, winner_team_id, survival_rate, duration_seconds, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		in.MatchType, in.WinnerTeam.TeamID, float64(in.WinnerTeam.AliveCount)/float64(in.WinnerTeam.InitialCount),
		in.DurationSeconds, in.StartedAt, in.FinishedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("记录对战失败: %w", err)
	}
	matchID, _ := res.LastInsertId()

	// 5. 处理每个参与者的积分变动
	processPlayer := func(p ParticipantReport, isWinner bool, teamID string) error {
		info := allParticipants[p.UUID]
		streak, err := s.getUserStreak(ctx, tx, info.UID)
		if err != nil {
			return err
		}

		// 计算连胜权重
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
		sValue := 0.0
		if isWinner {
			sValue = 1.0
		}
		eValue := expectedL
		if isWinner {
			eValue = expectedW
		}

		wStreak := rating.GetStreakWeight(newStreak)
		wPerf := rating.GetIndividualPerformanceTweak(p.PerfTweak)
		if wPerf == 0 {
			wPerf = 1.0
		}

		delta := rating.CalculateDelta(k, sValue, eValue, wRound, wStreak, wPerf)

		// 更新用户表
		scoreUpdate := "rank_score = GREATEST(0, rank_score + ?)"
		winsUpdate := ""
		if isWinner {
			winsUpdate = ", wins_count = wins_count + 1"
		}

		_, err = tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE users SET %s %s WHERE uid = ?", scoreUpdate, winsUpdate),
			delta, info.UID,
		)
		if err != nil {
			return err
		}

		// 记录参与者详情
		_, err = tx.ExecContext(ctx,
			`INSERT INTO match_participants (match_id, user_uid, team_id, is_winner, rank_score_before, rank_score_delta, survival_time_seconds, kills, deaths)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			matchID, info.UID, teamID, isWinner, info.RankScore, delta, p.SurvivalTime, p.Kills, p.Deaths,
		)
		if err != nil {
			return err
		}

		// 更新排行榜
		return s.updateRanking(ctx, tx, info.UID, in.MatchType, 1)
	}

	// 执行胜者处理
	for _, m := range in.WinnerTeam.Members {
		if err := processPlayer(m, true, in.WinnerTeam.TeamID); err != nil {
			return nil, err
		}
	}
	// 执行败者处理
	for _, team := range in.LoserTeams {
		for _, m := range team.Members {
			if err := processPlayer(m, false, team.TeamID); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	return &Match{
		ID:              matchID,
		MatchType:       in.MatchType,
		WinnerTeamID:    in.WinnerTeam.TeamID,
		SurvivalRate:    float64(in.WinnerTeam.AliveCount) / float64(in.WinnerTeam.InitialCount),
		DurationSeconds: in.DurationSeconds,
		StartedAt:       in.StartedAt,
		FinishedAt:      in.FinishedAt,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}, nil
}

type userRatingInfo struct {
	UID       int64
	RankScore int
	WinsCount int
}

func (s *matchService) resolveUID(ctx context.Context, uuid string) (int64, error) {
	// 1. 尝试从 Redis 缓存获取
	cacheKey := "mca:uuid_to_uid:" + uuid
	if s.redis != nil {
		if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil && val != "" {
			return strconv.ParseInt(val, 10, 64)
		}
	}

	// 2. 调用 HRPAuth API
	uid, err := s.authClient.LookupUserByUUID(uuid)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return 0, fmt.Errorf("用户不存在: %w", err)
		}
		return 0, fmt.Errorf("调用 HRPAuth 失败: %w", err)
	}

	// 3. 写入 Redis 缓存 (暂存)
	if s.redis != nil {
		s.redis.Set(ctx, cacheKey, strconv.FormatInt(uid, 10), 24*time.Hour)
	}

	return uid, nil
}

func (s *matchService) getUserRatingInfo(ctx context.Context, tx *sql.Tx, uid int64) (*userRatingInfo, error) {
	var info userRatingInfo
	err := tx.QueryRowContext(ctx, "SELECT rank_score, wins_count FROM users WHERE uid = ? AND deleted_at IS NULL", uid).Scan(&info.RankScore, &info.WinsCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("用户 (uid: %d) 在本地系统中不存在", uid)
		}
		return nil, err
	}
	return &info, nil
}

func (s *matchService) getUserStreak(ctx context.Context, tx *sql.Tx, uid int64) (int, error) {
	// 获取最近 10 场对战结果来计算连胜/连败
	rows, err := tx.QueryContext(ctx,
		`SELECT winner_uid FROM matches 
		 WHERE (winner_uid = ? OR loser_uid = ?) 
		 ORDER BY created_at DESC LIMIT 10`,
		uid, uid,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	streak := 0
	isFirst := true
	winning := false

	for rows.Next() {
		var winnerUID int64
		if err := rows.Scan(&winnerUID); err != nil {
			return 0, err
		}

		isWin := winnerUID == uid
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

// GetByID 获取对战详情。
func (s *matchService) GetByID(ctx context.Context, id int64) (*Match, error) {
	var m Match
	err := s.db.QueryRowContext(ctx,
		`SELECT id, match_type, winner_team_id, survival_rate, duration_seconds, started_at, finished_at, created_at 
		 FROM matches WHERE id = ?`,
		id,
	).Scan(&m.ID, &m.MatchType, &m.WinnerTeamID, &m.SurvivalRate, &m.DurationSeconds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("对战记录不存在")
		}
		return nil, fmt.Errorf("查询对战详情失败: %w", err)
	}
	return &m, nil
}

// ListByUser 列出用户参与的对战历史（分页）。
func (s *matchService) ListByUser(ctx context.Context, uid int64, page Page) ([]*Match, error) {
	offset := (page.PageNum - 1) * page.PageSize
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.match_type, m.winner_team_id, m.survival_rate, m.duration_seconds, m.started_at, m.finished_at, m.created_at 
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

	var matches []*Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.ID, &m.MatchType, &m.WinnerTeamID, &m.SurvivalRate, &m.DurationSeconds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("解析对战记录失败: %w", err)
		}
		matches = append(matches, &m)
	}
	return matches, nil
}
