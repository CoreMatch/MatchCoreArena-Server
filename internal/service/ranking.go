package service

import (
	"context"
	"database/sql"
	"fmt"
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

// GetTop 获取排行榜 Top N。
func (s *rankingService) GetTop(ctx context.Context, rankType string, limit, season int) ([]*Ranking, error) {
	if limit <= 0 {
		limit = 100
	}
	if season <= 0 {
		season = 1
	}

	// 计算排名
	// 注意：这里我们实时计算排名，虽然性能一般，但逻辑最准确
	// 生产环境应配合 Redis ZSET 或定期更新 rank_position 字段
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_uid, rank_type, score, season, updated_at 
		 FROM rankings 
		 WHERE rank_type = ? AND season = ? 
		 ORDER BY score DESC LIMIT ?`,
		rankType, season, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("查询排行榜失败: %w", err)
	}
	defer rows.Close()

	var rankings []*Ranking
	pos := 1
	for rows.Next() {
		var r Ranking
		if err := rows.Scan(&r.ID, &r.UserUID, &r.RankType, &r.Score, &r.Season, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("解析排行榜记录失败: %w", err)
		}
		r.RankPosition = pos
		rankings = append(rankings, &r)
		pos++
	}
	return rankings, nil
}

// GetMyRank 获取我的排名。
func (s *rankingService) GetMyRank(ctx context.Context, uid int64, rankType string, season int) (*Ranking, error) {
	if season <= 0 {
		season = 1
	}

	// 1. 获取我的记录
	var r Ranking
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_uid, rank_type, score, season, updated_at 
		 FROM rankings 
		 WHERE user_uid = ? AND rank_type = ? AND season = ?`,
		uid, rankType, season,
	).Scan(&r.ID, &r.UserUID, &r.RankType, &r.Score, &r.Season, &r.UpdatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("未找到您的排名信息")
		}
		return nil, fmt.Errorf("查询排名失败: %w", err)
	}

	// 2. 计算实时排名
	var pos int
	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) + 1 FROM rankings WHERE rank_type = ? AND season = ? AND score > ?",
		rankType, season, r.Score,
	).Scan(&pos)
	if err != nil {
		return nil, fmt.Errorf("计算排名位置失败: %w", err)
	}
	r.RankPosition = pos

	return &r, nil
}
