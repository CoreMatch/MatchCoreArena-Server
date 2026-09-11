package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"MatchCoreArena-Server/internal/rating"
)

// Match 对战记录模型。
type Match struct {
	ID               int64   `json:"id"`
	MatchType        string  `json:"match_type"`
	WinnerUID        int64   `json:"winner_uid"`
	LoserUID         int64   `json:"loser_uid"`
	WinnerScore      int     `json:"winner_score"`
	LoserScore       int     `json:"loser_score"`
	WinnerPerf       float64 `json:"winner_perf"`
	LoserPerf        float64 `json:"loser_perf"`
	WinnerDelta      int     `json:"winner_delta"`
	LoserDelta       int     `json:"loser_delta"`
	DurationSeconds  int     `json:"duration_seconds"`
	StartedAt        string  `json:"started_at"`
	FinishedAt       string  `json:"finished_at"`
	CreatedAt        string  `json:"created_at"`
}

// ReportInput 上报对战请求参数。
type ReportInput struct {
	MatchType       string  `json:"match_type" binding:"required"`
	WinnerUID       int64   `json:"winner_uid" binding:"required"`
	LoserUID        int64   `json:"loser_uid" binding:"required"`
	WinnerScore     int     `json:"winner_score"`
	LoserScore      int     `json:"loser_score"`
	WinnerPerf      float64 `json:"winner_perf"`
	LoserPerf       float64 `json:"loser_perf"`
	DurationSeconds int     `json:"duration_seconds"`
	StartedAt       string  `json:"started_at" binding:"required"`
	FinishedAt      string  `json:"finished_at" binding:"required"`
}

// Page 分页参数。
type Page struct {
	PageNum  int `json:"page_num" form:"page_num"`
	PageSize int `json:"page_size" form:"page_size"`
}

// MatchService 对战业务接口。
type MatchService interface {
	// Report 上报对战结果。
	Report(ctx context.Context, reporterUID int64, in ReportInput) (*Match, error)
	// GetByID 获取对战详情。
	GetByID(ctx context.Context, id int64) (*Match, error)
	// ListByUser 列出用户参与的对战历史（分页）。
	ListByUser(ctx context.Context, uid int64, page Page) ([]*Match, error)
}

// matchService 数据库实现。
type matchService struct {
	db *sql.DB
}

// NewMatchService 创建 MatchService 实例。
func NewMatchService(db *sql.DB) MatchService {
	return &matchService{db: db}
}

// Report 上报对战结果。
func (s *matchService) Report(ctx context.Context, reporterUID int64, in ReportInput) (*Match, error) {
	if in.WinnerUID == in.LoserUID {
		return nil, fmt.Errorf("获胜者和失败者不能是同一个人")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	// 1. 获取双方当前数据
	winner, err := s.getUserRatingInfo(ctx, tx, in.WinnerUID)
	if err != nil {
		return nil, fmt.Errorf("获取获胜者信息失败: %w", err)
	}
	loser, err := s.getUserRatingInfo(ctx, tx, in.LoserUID)
	if err != nil {
		return nil, fmt.Errorf("获取失败者信息失败: %w", err)
	}

	// 2. 获取连胜/连败信息
	winnerStreak, err := s.getUserStreak(ctx, tx, in.WinnerUID)
	if err != nil {
		return nil, fmt.Errorf("获取获胜者连胜信息失败: %w", err)
	}
	loserStreak, err := s.getUserStreak(ctx, tx, in.LoserUID)
	if err != nil {
		return nil, fmt.Errorf("获取失败者连败信息失败: %w", err)
	}

	// 3. 计算分数变化
	// 期望胜率
	expectedW := rating.CalculateEloExpected(float64(winner.RankScore), float64(loser.RankScore))
	expectedL := rating.CalculateEloExpected(float64(loser.RankScore), float64(winner.RankScore))

	// K-Factor
	kW := rating.GetKFactor(winner.WinsCount)
	kL := rating.GetKFactor(loser.WinsCount)

	// 回合权重
	wRound := rating.GetRoundWeight(in.WinnerScore, in.LoserScore)

	// 连胜/连败权重 (新的一场比赛后的 streak)
	newWinnerStreak := winnerStreak
	if winnerStreak >= 0 {
		newWinnerStreak++
	} else {
		newWinnerStreak = 1
	}
	newLoserStreak := loserStreak
	if loserStreak <= 0 {
		newLoserStreak--
	} else {
		newLoserStreak = -1
	}

	wStreakW := rating.GetStreakWeight(newWinnerStreak)
	wStreakL := rating.GetStreakWeight(newLoserStreak)

	// 表现权重 (由上报者提供)
	wPerfW := in.WinnerPerf
	if wPerfW == 0 {
		wPerfW = 1.0
	}
	wPerfL := in.LoserPerf
	if wPerfL == 0 {
		wPerfL = 1.0
	}

	// 计算 Delta
	deltaW := rating.CalculateDelta(kW, 1.0, expectedW, wRound, wStreakW, wPerfW)
	deltaL := rating.CalculateDelta(kL, 0.0, expectedL, wRound, wStreakL, wPerfL)

	// 4. 记录对战 (包含 Delta 和 Perf)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO matches (match_type, winner_uid, loser_uid, winner_score, loser_score, winner_perf, loser_perf, winner_delta, loser_delta, duration_seconds, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.MatchType, in.WinnerUID, in.LoserUID, in.WinnerScore, in.LoserScore, wPerfW, wPerfL, deltaW, deltaL, in.DurationSeconds, in.StartedAt, in.FinishedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("记录对战失败: %w", err)
	}
	id, _ := res.LastInsertId()

	// 5. 更新用户积分和胜场
	if _, err := tx.ExecContext(ctx, "UPDATE users SET rank_score = rank_score + ?, wins_count = wins_count + 1 WHERE uid = ? AND deleted_at IS NULL", deltaW, in.WinnerUID); err != nil {
		return nil, fmt.Errorf("更新获胜者积分失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET rank_score = GREATEST(0, rank_score + ?) WHERE uid = ? AND deleted_at IS NULL", deltaL, in.LoserUID); err != nil {
		return nil, fmt.Errorf("更新失败者积分失败: %w", err)
	}

	// 6. 更新排行榜 (假设当前是第 1 赛季)
	if err := s.updateRanking(ctx, tx, in.WinnerUID, in.MatchType, 1); err != nil {
		return nil, fmt.Errorf("更新获胜者排行榜失败: %w", err)
	}
	if err := s.updateRanking(ctx, tx, in.LoserUID, in.MatchType, 1); err != nil {
		return nil, fmt.Errorf("更新失败者排行榜失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	return &Match{
		ID:              id,
		MatchType:       in.MatchType,
		WinnerUID:       in.WinnerUID,
		LoserUID:        in.LoserUID,
		WinnerScore:     in.WinnerScore,
		LoserScore:      in.LoserScore,
		WinnerPerf:      wPerfW,
		LoserPerf:       wPerfL,
		WinnerDelta:     deltaW,
		LoserDelta:      deltaL,
		DurationSeconds: in.DurationSeconds,
		StartedAt:       in.StartedAt,
		FinishedAt:      in.FinishedAt,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}, nil
}

type userRatingInfo struct {
	RankScore int
	WinsCount int
}

func (s *matchService) getUserRatingInfo(ctx context.Context, tx *sql.Tx, uid int64) (*userRatingInfo, error) {
	var info userRatingInfo
	err := tx.QueryRowContext(ctx, "SELECT rank_score, wins_count FROM users WHERE uid = ? AND deleted_at IS NULL", uid).Scan(&info.RankScore, &info.WinsCount)
	if err != nil {
		if err == sql.ErrNoRows {
			// 用户不存在则自动创建 (EnsureUser 逻辑)
			_, err = tx.ExecContext(ctx, "INSERT IGNORE INTO users (uid) VALUES (?)", uid)
			if err != nil {
				return nil, err
			}
			return &userRatingInfo{RankScore: 0, WinsCount: 0}, nil
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
		`SELECT id, match_type, winner_uid, loser_uid, winner_score, loser_score, winner_perf, loser_perf, winner_delta, loser_delta, duration_seconds, started_at, finished_at, created_at 
		 FROM matches WHERE id = ?`,
		id,
	).Scan(&m.ID, &m.MatchType, &m.WinnerUID, &m.LoserUID, &m.WinnerScore, &m.LoserScore, &m.WinnerPerf, &m.LoserPerf, &m.WinnerDelta, &m.LoserDelta, &m.DurationSeconds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt)

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
		`SELECT id, match_type, winner_uid, loser_uid, winner_score, loser_score, winner_perf, loser_perf, winner_delta, loser_delta, duration_seconds, started_at, finished_at, created_at 
		 FROM matches WHERE winner_uid = ? OR loser_uid = ? 
		 ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		uid, uid, page.PageSize, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("查询对战历史失败: %w", err)
	}
	defer rows.Close()

	var matches []*Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.ID, &m.MatchType, &m.WinnerUID, &m.LoserUID, &m.WinnerScore, &m.LoserScore, &m.WinnerPerf, &m.LoserPerf, &m.WinnerDelta, &m.LoserDelta, &m.DurationSeconds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("解析对战记录失败: %w", err)
		}
		matches = append(matches, &m)
	}
	return matches, nil
}
