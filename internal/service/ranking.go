package service

import (
	"context"
	"database/sql"
)

// Ranking 排行榜条目模型。
type Ranking struct {
	ID          int64  `json:"id"`
	UserUID     int64  `json:"user_uid"`
	RankType    string `json:"rank_type"`
	Score       int64  `json:"score"`
	RankPosition int   `json:"rank_position"`
	Season      int    `json:"season"`
	UpdatedAt   string `json:"updated_at"`
}

// RankingService 排行榜业务接口。
type RankingService interface {
	// GetTop 获取指定类型排行榜 Top N。
	GetTop(ctx context.Context, rankType string, limit, season int) ([]*Ranking, error)
	// GetMyRank 获取当前用户在指定类型的排名。
	GetMyRank(ctx context.Context, uid int64, rankType string, season int) (*Ranking, error)
}

// rankingService 数据库/Redis 实现。
type rankingService struct {
	db    *sql.DB
}

// NewRankingService 创建 RankingService 实例。
func NewRankingService(db *sql.DB) RankingService {
	return &rankingService{db: db}
}

// GetTop 获取排行榜 Top N（骨架 stub）。
func (s *rankingService) GetTop(ctx context.Context, rankType string, limit, season int) ([]*Ranking, error) {
	// TODO: 优先从 Redis ZSET 读取（ZREVRANGE key 0 limit-1）
	// TODO: 回退到 SELECT * FROM rankings WHERE rank_type = ? AND season = ? ORDER BY score DESC LIMIT ?
	if limit <= 0 {
		limit = 100
	}
	if season <= 0 {
		season = 1
	}
	return []*Ranking{}, nil
}

// GetMyRank 获取我的排名（骨架 stub）。
func (s *rankingService) GetMyRank(ctx context.Context, uid int64, rankType string, season int) (*Ranking, error) {
	// TODO: SELECT * FROM rankings WHERE user_uid = ? AND rank_type = ? AND season = ?
	if season <= 0 {
		season = 1
	}
	return &Ranking{
		ID:           1,
		UserUID:      uid,
		RankType:     rankType,
		Score:        0,
		RankPosition: 0,
		Season:       season,
		UpdatedAt:    "2026-09-11T00:00:00Z",
	}, nil
}
