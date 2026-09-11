package service

import (
	"context"
	"database/sql"
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

// Report 上报对战结果（骨架 stub）。
func (s *matchService) Report(ctx context.Context, reporterUID int64, in ReportInput) (*Match, error) {
	// TODO: 校验 winner_uid / loser_uid 合法且不同
	// TODO: INSERT INTO matches (match_type, winner_uid, loser_uid, ...) VALUES (?, ?, ?, ...)
	// TODO: 更新 users.experience + 重算 rankings + 写 Redis ZSET
	return &Match{
		ID:              1,
		MatchType:       in.MatchType,
		WinnerUID:       in.WinnerUID,
		LoserUID:        in.LoserUID,
		WinnerScore:     in.WinnerScore,
		LoserScore:      in.LoserScore,
		DurationSeconds: in.DurationSeconds,
		StartedAt:       in.StartedAt,
		FinishedAt:      in.FinishedAt,
		CreatedAt:       "2026-09-11T00:00:00Z",
	}, nil
}

// GetByID 获取对战详情（骨架 stub）。
func (s *matchService) GetByID(ctx context.Context, id int64) (*Match, error) {
	// TODO: SELECT * FROM matches WHERE id = ?
	return &Match{
		ID:              id,
		MatchType:       "1v1",
		WinnerUID:       1,
		LoserUID:        2,
		WinnerScore:     3,
		LoserScore:      1,
		DurationSeconds: 120,
		StartedAt:       "2026-09-11T00:00:00Z",
		FinishedAt:      "2026-09-11T00:02:00Z",
		CreatedAt:       "2026-09-11T00:02:00Z",
	}, nil
}

// ListByUser 列出对战历史（骨架 stub）。
func (s *matchService) ListByUser(ctx context.Context, uid int64, page Page) ([]*Match, error) {
	// TODO: SELECT * FROM matches WHERE winner_uid = ? OR loser_uid = ? ORDER BY created_at DESC LIMIT ? OFFSET ?
	return []*Match{}, nil
}
