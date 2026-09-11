package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Match 对战记录模型。
type Match struct {
	ID              int64  `json:"id"`
	MatchType       string `json:"match_type"`
	WinnerUID       int64  `json:"winner_uid"`
	LoserUID        int64  `json:"loser_uid"`
	WinnerScore     int    `json:"winner_score"`
	LoserScore      int    `json:"loser_score"`
	DurationSeconds int    `json:"duration_seconds"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at"`
	CreatedAt       string `json:"created_at"`
}

// ReportInput 上报对战请求参数。
type ReportInput struct {
	MatchType       string `json:"match_type" binding:"required"`
	WinnerUID       int64  `json:"winner_uid" binding:"required"`
	LoserUID        int64  `json:"loser_uid" binding:"required"`
	WinnerScore     int    `json:"winner_score"`
	LoserScore      int    `json:"loser_score"`
	DurationSeconds int    `json:"duration_seconds"`
	StartedAt       string `json:"started_at" binding:"required"`
	FinishedAt      string `json:"finished_at" binding:"required"`
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

	// 1. 记录对战
	res, err := tx.ExecContext(ctx,
		`INSERT INTO matches (match_type, winner_uid, loser_uid, winner_score, loser_score, duration_seconds, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.MatchType, in.WinnerUID, in.LoserUID, in.WinnerScore, in.LoserScore, in.DurationSeconds, in.StartedAt, in.FinishedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("记录对战失败: %w", err)
	}
	id, _ := res.LastInsertId()

	// 2. 更新积分 (赢家 +20, 输家 -10)
	// 注意：这里假设用户已经存在。如果不存在，UPDATE 会成功但 RowsAffected 为 0。
	// 在生产环境中可能需要先 EnsureUser。
	if _, err := tx.ExecContext(ctx, "UPDATE users SET rank_score = rank_score + 20 WHERE uid = ? AND deleted_at IS NULL", in.WinnerUID); err != nil {
		return nil, fmt.Errorf("更新获胜者积分失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET rank_score = GREATEST(0, rank_score - 10) WHERE uid = ? AND deleted_at IS NULL", in.LoserUID); err != nil {
		return nil, fmt.Errorf("更新失败者积分失败: %w", err)
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
		DurationSeconds: in.DurationSeconds,
		StartedAt:       in.StartedAt,
		FinishedAt:      in.FinishedAt,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}, nil
}

// GetByID 获取对战详情。
func (s *matchService) GetByID(ctx context.Context, id int64) (*Match, error) {
	var m Match
	err := s.db.QueryRowContext(ctx,
		`SELECT id, match_type, winner_uid, loser_uid, winner_score, loser_score, duration_seconds, started_at, finished_at, created_at 
		 FROM matches WHERE id = ?`,
		id,
	).Scan(&m.ID, &m.MatchType, &m.WinnerUID, &m.LoserUID, &m.WinnerScore, &m.LoserScore, &m.DurationSeconds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt)

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
		`SELECT id, match_type, winner_uid, loser_uid, winner_score, loser_score, duration_seconds, started_at, finished_at, created_at 
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
		if err := rows.Scan(&m.ID, &m.MatchType, &m.WinnerUID, &m.LoserUID, &m.WinnerScore, &m.LoserScore, &m.DurationSeconds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("解析对战记录失败: %w", err)
		}
		matches = append(matches, &m)
	}
	return matches, nil
}
