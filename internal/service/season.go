package service

// 赛季手动更迭（契约 1.12.0，2026-11-12 作者决策，knowledge/0007）：
// 更季只能由运维端点手动触发，绝不允许按时间自动更季；是否重置评分
// 是运维的显式参数（默认不重置），服务端不替运维决定；每次更季记录
// 在案（谁、何时、是否重置、备注），重复调用有明确行为（目标赛季已
// 存在 → 409，绝不重复推进）。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	gomysql "github.com/go-sql-driver/mysql"

	"MatchCoreArena-Server/internal/apperr"
)

// SeasonRecord 赛季簿记记录。字段形态与契约 1.12.0 的 SeasonRecord
// schema 对齐。
type SeasonRecord struct {
	Season      int    `json:"season"`
	ResetScores bool   `json:"reset_scores"`
	OpenedAt    string `json:"opened_at"`
	OpenedBy    string `json:"opened_by"`
	Note        string `json:"note,omitempty"`
}

// SeasonService 赛季业务接口。
type SeasonService interface {
	// Current 返回当前赛季号（结算写入与排行榜缺省查询都用它）。
	Current(ctx context.Context) (int, error)
	// List 返回全部赛季记录，新到旧（首条即当前赛季）。
	List(ctx context.Context) ([]*SeasonRecord, error)
	// Roll 手动更季：目标赛季必须是当前赛季 +1；重复目标 409；
	// resetScores 为运维显式选择（重置全部 users.rank_score 为 0，
	// 不动 wins_count 与对局历史）。
	Roll(ctx context.Context, targetSeason int, resetScores bool, openedBy, note string) (*SeasonRecord, error)
}

type seasonService struct {
	db *sql.DB
}

// NewSeasonService 创建 SeasonService 实例。
func NewSeasonService(db *sql.DB) SeasonService {
	return &seasonService{db: db}
}

// CurrentSeason 读取当前赛季号（迁移 v6 保证表非空：种子赛季 1）。
// 事务内外均可用——结算在自己的事务里调用以拿到一致快照。
func CurrentSeason(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}) (int, error) {
	var season sql.NullInt64
	if err := q.QueryRowContext(ctx, "SELECT MAX(season) FROM seasons").Scan(&season); err != nil {
		return 0, fmt.Errorf("读取当前赛季失败: %w", err)
	}
	if !season.Valid {
		return 0, fmt.Errorf("seasons 表为空：迁移种子数据缺失")
	}
	return int(season.Int64), nil
}

func (s *seasonService) Current(ctx context.Context) (int, error) {
	return CurrentSeason(ctx, s.db)
}

func (s *seasonService) List(ctx context.Context) ([]*SeasonRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT season, reset_scores, opened_at, opened_by, note FROM seasons ORDER BY season DESC")
	if err != nil {
		return nil, fmt.Errorf("查询赛季记录失败: %w", err)
	}
	defer rows.Close()
	seasons := make([]*SeasonRecord, 0, 4)
	for rows.Next() {
		var rec SeasonRecord
		var note sql.NullString
		if err := rows.Scan(&rec.Season, &rec.ResetScores, &rec.OpenedAt, &rec.OpenedBy, &note); err != nil {
			return nil, fmt.Errorf("读取赛季记录失败: %w", err)
		}
		rec.Note = note.String
		seasons = append(seasons, &rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取赛季记录失败: %w", err)
	}
	return seasons, nil
}

// Roll 手动更季（事务）：校验目标 = 当前+1 → 写入赛季记录 →
// （显式要求时）重置全部评分。uk_season 兜底并发竞态。
func (s *seasonService) Roll(ctx context.Context, targetSeason int, resetScores bool, openedBy, note string) (*SeasonRecord, error) {
	if targetSeason < 1 {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest,
			fmt.Sprintf("target_season 非法: %d（赛季号从 1 起）", targetSeason))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	current, err := CurrentSeason(ctx, tx)
	if err != nil {
		return nil, err
	}
	if targetSeason <= current {
		return nil, apperr.New(apperr.CodeMCASeasonExists,
			fmt.Sprintf("赛季 %d 已存在（当前赛季 %d）——更季不可重复、不可回退", targetSeason, current))
	}
	if targetSeason != current+1 {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest,
			fmt.Sprintf("更季只允许逐季递增：当前赛季 %d，target_season 必须为 %d", current, current+1))
	}

	var noteArg any
	if note != "" {
		noteArg = note
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO seasons (season, reset_scores, opened_by, note) VALUES (?, ?, ?, ?)",
		targetSeason, resetScores, openedBy, noteArg,
	); err != nil {
		var me *gomysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			// 并发竞态：另一更季请求先写入了同一目标赛季。
			return nil, apperr.New(apperr.CodeMCASeasonExists,
				fmt.Sprintf("赛季 %d 已存在（并发更季）", targetSeason))
		}
		return nil, fmt.Errorf("写入赛季记录失败: %w", err)
	}

	// 重置评分是运维的显式选择：只清 rank_score（表默认值 0），
	// 不动 wins_count（K 阶段）与对局历史（连胜等）。
	if resetScores {
		if _, err := tx.ExecContext(ctx,
			"UPDATE users SET rank_score = 0 WHERE deleted_at IS NULL"); err != nil {
			return nil, fmt.Errorf("重置评分失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	rec := &SeasonRecord{Season: targetSeason, ResetScores: resetScores, OpenedBy: openedBy, Note: note}
	if err := s.db.QueryRowContext(ctx,
		"SELECT opened_at FROM seasons WHERE season = ?", targetSeason,
	).Scan(&rec.OpenedAt); err != nil {
		return nil, fmt.Errorf("回读赛季记录失败 (season: %d): %w", targetSeason, err)
	}
	return rec, nil
}
