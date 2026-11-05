package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"MatchCoreArena-Server/config"
	"MatchCoreArena-Server/internal/apperr"
)

// QueueEntry 匹配队列条目。
type QueueEntry struct {
	UID        int64  `json:"uid"`
	MatchType  string `json:"match_type"`
	RankScore  int    `json:"rank_score"`
	EnqueuedAt string `json:"enqueued_at"`
}

// GroupMember 成局成员。
type GroupMember struct {
	UID       int64  `json:"uid"`
	TeamID    string `json:"team_id"`
	RankScore int    `json:"rank_score"`
}

// FormedGroup 匹配成局记录。
type FormedGroup struct {
	GroupID   int64         `json:"group_id"`
	MatchType string        `json:"match_type"`
	FormedAt  string        `json:"formed_at"`
	Members   []GroupMember `json:"members"`
}

// MatchmakingStatus 用户当前匹配状态。
type MatchmakingStatus struct {
	InQueue      bool          `json:"in_queue"`
	QueueEntry   *QueueEntry   `json:"queue_entry,omitempty"`
	RecentGroups []FormedGroup `json:"recent_groups"`
}

// MatchmakingService 匹配业务接口。
type MatchmakingService interface {
	// Join 加入匹配队列；每个用户同时至多一条排队记录。
	Join(ctx context.Context, uid int64, matchType string) (*QueueEntry, error)
	// Leave 离开匹配队列。
	Leave(ctx context.Context, uid int64) error
	// Status 查询用户排队状态与本人相关的近期成局记录。
	Status(ctx context.Context, uid int64) (*MatchmakingStatus, error)
}

type matchmakingService struct {
	db  *sql.DB
	cfg config.MatchmakingConfig
}

// NewMatchmakingService 创建 MatchmakingService 实例。
func NewMatchmakingService(db *sql.DB, cfg config.MatchmakingConfig) MatchmakingService {
	return &matchmakingService{db: db, cfg: cfg}
}

// Join 加入匹配队列。评分取 users.rank_score 快照；缺行时按与结算
// 一致的懒初始化建行（uid 只来自已校验令牌）。
func (s *matchmakingService) Join(ctx context.Context, uid int64, matchType string) (*QueueEntry, error) {
	if !s.cfg.Enabled {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest, "匹配系统未启用")
	}
	mode, ok := s.cfg.FindMode(matchType)
	if !ok {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest,
			fmt.Sprintf("未知匹配模式: %s", matchType))
	}

	score, err := s.getUserRankScore(ctx, uid)
	if err != nil {
		return nil, err
	}

	entry := &QueueEntry{UID: uid, MatchType: mode.MatchType, RankScore: score}
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO matchmaking_queue (user_uid, match_type, rank_score) VALUES (?, ?, ?)",
		uid, mode.MatchType, score,
	); err != nil {
		var me *gomysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			return nil, apperr.New(apperr.CodeMCAQueueAlreadyJoined, "已在匹配队列中")
		}
		return nil, fmt.Errorf("加入匹配队列失败: %w", err)
	}

	// enqueued_at 由数据库生成，回读以返回准确值。
	var enqueuedAt string
	if err := s.db.QueryRowContext(ctx,
		"SELECT enqueued_at FROM matchmaking_queue WHERE user_uid = ?", uid,
	).Scan(&enqueuedAt); err != nil {
		return nil, fmt.Errorf("回读排队记录失败: %w", err)
	}
	entry.EnqueuedAt = enqueuedAt
	return entry, nil
}

// getUserRankScore 读取用户评分；缺行时懒建行（与结算路径同一模式与
// 信任边界），建行不可能时返回类型化错误。
func (s *matchmakingService) getUserRankScore(ctx context.Context, uid int64) (int, error) {
	var score int
	err := s.db.QueryRowContext(ctx,
		"SELECT rank_score FROM users WHERE uid = ? AND deleted_at IS NULL", uid,
	).Scan(&score)
	if err == nil {
		return score, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("查询用户评分失败: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, "INSERT IGNORE INTO users (uid) VALUES (?)", uid); err != nil {
		return 0, fmt.Errorf("懒建行失败 (uid: %d): %w", uid, err)
	}
	err = s.db.QueryRowContext(ctx,
		"SELECT rank_score FROM users WHERE uid = ? AND deleted_at IS NULL", uid,
	).Scan(&score)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, apperr.New(apperr.CodeMCAUserNotProvisioned,
				fmt.Sprintf("用户 (uid: %d) users 行无法建立（uid 可能被已删除账户占用）", uid))
		}
		return 0, fmt.Errorf("查询用户评分失败: %w", err)
	}
	return score, nil
}

// Leave 离开匹配队列；不在队列中返回 404 mca_queue_not_joined。
func (s *matchmakingService) Leave(ctx context.Context, uid int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM matchmaking_queue WHERE user_uid = ?", uid)
	if err != nil {
		return fmt.Errorf("离开匹配队列失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("离开匹配队列失败: %w", err)
	}
	if n == 0 {
		return apperr.New(apperr.CodeMCAQueueNotJoined, "未在匹配队列中")
	}
	return nil
}

// Status 查询用户排队状态与本人相关的近期成局记录（最多 10 局）。
func (s *matchmakingService) Status(ctx context.Context, uid int64) (*MatchmakingStatus, error) {
	st := &MatchmakingStatus{RecentGroups: []FormedGroup{}}

	var entry QueueEntry
	err := s.db.QueryRowContext(ctx,
		"SELECT user_uid, match_type, rank_score, enqueued_at FROM matchmaking_queue WHERE user_uid = ?", uid,
	).Scan(&entry.UID, &entry.MatchType, &entry.RankScore, &entry.EnqueuedAt)
	if err == nil {
		st.InQueue = true
		st.QueueEntry = &entry
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("查询排队状态失败: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT g.id, g.match_type, g.formed_at
		 FROM matchmaking_groups g
		 JOIN matchmaking_group_members gm ON gm.group_id = g.id
		 WHERE gm.user_uid = ?
		 ORDER BY g.formed_at DESC, g.id DESC LIMIT 10`,
		uid,
	)
	if err != nil {
		return nil, fmt.Errorf("查询成局记录失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var g FormedGroup
		if err := rows.Scan(&g.GroupID, &g.MatchType, &g.FormedAt); err != nil {
			return nil, fmt.Errorf("解析成局记录失败: %w", err)
		}
		st.RecentGroups = append(st.RecentGroups, g)
	}

	for i := range st.RecentGroups {
		members, err := s.groupMembers(ctx, st.RecentGroups[i].GroupID)
		if err != nil {
			return nil, err
		}
		st.RecentGroups[i].Members = members
	}
	return st, nil
}

func (s *matchmakingService) groupMembers(ctx context.Context, groupID int64) ([]GroupMember, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT user_uid, team_id, rank_score FROM matchmaking_group_members WHERE group_id = ? ORDER BY id",
		groupID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询成局成员失败: %w", err)
	}
	defer rows.Close()

	members := []GroupMember{}
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.UID, &m.TeamID, &m.RankScore); err != nil {
			return nil, fmt.Errorf("解析成局成员失败: %w", err)
		}
		members = append(members, m)
	}
	return members, nil
}

// MatchmakingEngine 后台匹配引擎：周期性移除超时排队并按分差窗口成局。
// 成局写入 matchmaking_groups / matchmaking_group_members 并从队列移除。
// 超时与等待时长一律在数据库侧计算（NOW()/TIMESTAMPDIFF），
// 避免驱动时区与服务器时区不一致导致的时钟偏差。
type MatchmakingEngine struct {
	db  *sql.DB
	cfg config.MatchmakingConfig
}

// NewMatchmakingEngine 创建匹配引擎。
func NewMatchmakingEngine(db *sql.DB, cfg config.MatchmakingConfig) *MatchmakingEngine {
	return &MatchmakingEngine{db: db, cfg: cfg}
}

// Start 启动后台匹配循环，随 ctx 取消退出。
func (e *MatchmakingEngine) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Duration(e.cfg.TickSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := e.Tick(ctx); err != nil {
					log.Printf("匹配引擎 tick 失败: %v", err)
				}
			}
		}
	}()
}

// Tick 执行一轮匹配：先移除超时条目，再逐模式尝试成局。
func (e *MatchmakingEngine) Tick(ctx context.Context) error {
	if _, err := e.db.ExecContext(ctx,
		"DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF(SECOND, enqueued_at, NOW()) >= ?",
		e.cfg.QueueTimeoutSeconds,
	); err != nil {
		return fmt.Errorf("移除超时排队失败: %w", err)
	}
	for _, mode := range e.cfg.Modes {
		if err := e.formGroups(ctx, mode); err != nil {
			return fmt.Errorf("模式 %s 匹配失败: %w", mode.MatchType, err)
		}
	}
	return nil
}

// ratingWindow 计算等待 waited 时长后的分差窗口：
// 基础窗口 + 每分钟增长，封顶最大窗口。
func (e *MatchmakingEngine) ratingWindow(waited time.Duration) int {
	w := float64(e.cfg.BaseRatingWindow) + waited.Minutes()*float64(e.cfg.RatingWindowGrowthPerMinute)
	if w > float64(e.cfg.MaxRatingWindow) {
		w = float64(e.cfg.MaxRatingWindow)
	}
	return int(w)
}

type queueRow struct {
	ID            int64
	UID           int64
	RankScore     int
	WaitedSeconds int64
}

// formGroups 对单个模式反复成局，直到剩余人数不足一局。
// 贪心策略：以等待最久者为轴，窗口取其等待时长对应的分差窗口，
// 在窗口内按队列顺序取满所需人数。
func (e *MatchmakingEngine) formGroups(ctx context.Context, mode config.MatchMode) error {
	need := mode.TotalPlayers()
	for {
		rows, err := e.db.QueryContext(ctx,
			`SELECT id, user_uid, rank_score, TIMESTAMPDIFF(SECOND, enqueued_at, NOW()) AS waited_seconds
			 FROM matchmaking_queue WHERE match_type = ?
			 ORDER BY enqueued_at ASC, rank_score DESC`,
			mode.MatchType,
		)
		if err != nil {
			return fmt.Errorf("查询队列失败: %w", err)
		}
		queue := make([]queueRow, 0, need)
		for rows.Next() {
			var r queueRow
			if err := rows.Scan(&r.ID, &r.UID, &r.RankScore, &r.WaitedSeconds); err != nil {
				rows.Close()
				return fmt.Errorf("解析队列条目失败: %w", err)
			}
			queue = append(queue, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("遍历队列失败: %w", err)
		}

		if len(queue) < need {
			return nil
		}

		window := e.ratingWindow(time.Duration(queue[0].WaitedSeconds) * time.Second)
		selected := []queueRow{queue[0]}
		for _, r := range queue[1:] {
			diff := r.RankScore - queue[0].RankScore
			if diff < 0 {
				diff = -diff
			}
			if diff <= window {
				selected = append(selected, r)
			}
			if len(selected) == need {
				break
			}
		}
		if len(selected) < need {
			return nil // 窗口内凑不齐，等待窗口放宽或新成员入队
		}

		if err := e.commitGroup(ctx, mode, selected); err != nil {
			return err
		}
	}
}

// teamAssignment 成局时的队伍分配结果。
type teamAssignment struct {
	row    queueRow
	teamID string
}

// assignTeams 按评分降序蛇形分队（1 号位 A 队、2 号位 B 队、3 号位 B 队、
// 4 号位 A 队……），尽量平衡各队实力。队伍标识为 A/B/C/...。
func assignTeams(mode config.MatchMode, selected []queueRow) []teamAssignment {
	sorted := make([]queueRow, len(selected))
	copy(sorted, selected)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].RankScore > sorted[j-1].RankScore; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	teams := len(mode.TeamSizes)
	assignments := make([]teamAssignment, 0, len(sorted))
	for i, r := range sorted {
		cycle := i / teams
		pos := i % teams
		if cycle%2 == 1 {
			pos = teams - 1 - pos
		}
		assignments = append(assignments, teamAssignment{
			row:    r,
			teamID: string(rune('A' + pos)),
		})
	}
	return assignments
}

// commitGroup 在单个事务内写入成局记录并移出队列。
func (e *MatchmakingEngine) commitGroup(ctx context.Context, mode config.MatchMode, selected []queueRow) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		"INSERT INTO matchmaking_groups (match_type) VALUES (?)", mode.MatchType,
	)
	if err != nil {
		return fmt.Errorf("写入成局记录失败: %w", err)
	}
	groupID, _ := res.LastInsertId()

	for _, a := range assignTeams(mode, selected) {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO matchmaking_group_members (group_id, user_uid, team_id, rank_score) VALUES (?, ?, ?, ?)",
			groupID, a.row.UID, a.teamID, a.row.RankScore,
		); err != nil {
			return fmt.Errorf("写入成局成员失败: %w", err)
		}
	}

	ids := make([]string, 0, len(selected))
	args := make([]any, 0, len(selected))
	for _, r := range selected {
		ids = append(ids, "?")
		args = append(args, r.ID)
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM matchmaking_queue WHERE id IN ("+strings.Join(ids, ",")+")", args...,
	); err != nil {
		return fmt.Errorf("移出队列失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}
