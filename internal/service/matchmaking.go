package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	goredis "github.com/redis/go-redis/v9"

	"MatchCoreArena-Server/config"
	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/metrics"
)

// QueueEntry 匹配队列条目。
type QueueEntry struct {
	UID        int64  `json:"uid"`
	MatchType  string `json:"match_type"`
	RankScore  int    `json:"rank_score"`
	PartyID    int64  `json:"party_id,omitempty"`
	EnqueuedAt string `json:"enqueued_at"`
}

// GroupMember 成局成员。
type GroupMember struct {
	UID       int64  `json:"uid"`
	TeamID    string `json:"team_id"`
	RankScore int    `json:"rank_score"`
	PartyID   int64  `json:"party_id,omitempty"`
}

// JoinTarget 成局下发的游戏服务器地址（交接链路用）。
// 字段名与 MCA 客户端 interop 载荷的 join_target 对齐；服务端未配置
// 时整个字段省略（omitempty），客户端回落本地配置。
type JoinTarget struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// FormedGroup 匹配成局记录。
type FormedGroup struct {
	GroupID    int64         `json:"group_id"`
	MatchType  string        `json:"match_type"`
	FormedAt   string        `json:"formed_at"`
	Members    []GroupMember `json:"members"`
	JoinTarget *JoinTarget   `json:"join_target,omitempty"`
}

// QueueStats 队列规模统计（只读展示用，不参与入队/成局逻辑）。
type QueueStats struct {
	Players            int64 `json:"players"`              // 当前排队玩家数
	Parties            int64 `json:"parties"`              // 队伍数（按 party 计，单人不计）
	AverageWaitSeconds int64 `json:"average_wait_seconds"` // 平均等待时长（秒）
}

// MatchmakingStatus 用户当前匹配状态。
type MatchmakingStatus struct {
	InQueue      bool          `json:"in_queue"`
	QueueEntry   *QueueEntry   `json:"queue_entry,omitempty"`
	RecentGroups []FormedGroup `json:"recent_groups"`
	QueueStats   QueueStats    `json:"queue_stats"`
}

// MatchmakingService 匹配业务接口。
type MatchmakingService interface {
	// Join 加入匹配队列。partyMembers 为成员 UUID 列表（与运维台字段
	// 同名同义）：非空时解析为 uid、建立临时队伍并整队入队；为空时
	// 单人入队（调用者不得已在队伍中）。
	Join(ctx context.Context, uid int64, matchType string, partyMembers []string) ([]*QueueEntry, error)
	// Leave 离开匹配队列；以队伍入队时整队移出并清理临时队伍。
	Leave(ctx context.Context, uid int64) error
	// Status 查询用户排队状态与本人相关的近期成局记录。
	Status(ctx context.Context, uid int64) (*MatchmakingStatus, error)
	// QueueDepth 按模式统计队列深度（可观测性用）。
	QueueDepth(ctx context.Context) (map[string]int64, error)
}

type matchmakingService struct {
	db         *sql.DB
	cfg        config.MatchmakingConfig
	authClient *auth.Client
	redis      *goredis.Client
}

// NewMatchmakingService 创建 MatchmakingService 实例。
// authClient/redis 用于组队入队时的 UUID→uid 解析（与对局上报同一
// 信任边界：uid 只来自 HRPAuth 解析结果）。
func NewMatchmakingService(db *sql.DB, cfg config.MatchmakingConfig, authClient *auth.Client, redis *goredis.Client) MatchmakingService {
	return &matchmakingService{db: db, cfg: cfg, authClient: authClient, redis: redis}
}

// maxTeamSize 返回模式的最大队伍人数。
func maxTeamSize(mode config.MatchMode) int {
	maxSize := 0
	for _, n := range mode.TeamSizes {
		if n > maxSize {
			maxSize = n
		}
	}
	return maxSize
}

// Join 加入匹配队列。
//
// 单人入队：不在任何队伍中才可单独排队。
// 队伍入队（partyMembers 非空）：成员 UUID 解析为 uid 后建立临时队伍，
// 全队作为一个单位写入队列；人数须在 [2, 模式单队上限] 内。
// 返回本次入队的全部队列条目（队伍入队时为多条）。
func (s *matchmakingService) Join(ctx context.Context, uid int64, matchType string, partyMembers []string) ([]*QueueEntry, error) {
	if !s.cfg.Enabled {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest, "匹配系统未启用")
	}
	mode, ok := s.cfg.FindMode(matchType)
	if !ok {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest,
			fmt.Sprintf("未知匹配模式: %s", matchType))
	}

	if len(partyMembers) > 0 {
		return s.joinAsParty(ctx, mode, partyMembers)
	}

	if in, _, err := s.partyOf(ctx, uid); err != nil {
		return nil, err
	} else if in {
		return nil, apperr.New(apperr.CodeMCAPartyAlreadyInParty, "已在队伍中，请以队伍为单位排队")
	}

	score, err := s.getUserRankScore(ctx, uid)
	if err != nil {
		return nil, err
	}
	entry, err := s.insertQueueEntry(ctx, uid, mode.MatchType, score, nil)
	if err != nil {
		return nil, err
	}
	metrics.Default.Inc(metrics.MatchmakingJoinTotal, 1)
	return []*QueueEntry{entry}, nil
}

// joinAsParty 以队伍为单位整体入队（事务）：解析成员 UUID、建立临时
// 队伍、全队写入队列。临时队伍在成局或整队离队时清理。
func (s *matchmakingService) joinAsParty(ctx context.Context, mode config.MatchMode, members []string) ([]*QueueEntry, error) {
	if len(members) < 2 {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest, "party_members 至少需要 2 名成员")
	}
	if len(members) > maxTeamSize(mode) {
		return nil, apperr.New(apperr.CodeMCAPartyFull,
			fmt.Sprintf("队伍人数超出模式 %s 单队上限 %d 人", mode.MatchType, maxTeamSize(mode)))
	}
	seenUUID := make(map[string]bool, len(members))
	for _, uuid := range members {
		if uuid == "" {
			return nil, apperr.New(apperr.CodeMCAInvalidRequest, "party_members 含空 UUID")
		}
		if seenUUID[uuid] {
			return nil, apperr.New(apperr.CodeMCAInvalidRequest,
				fmt.Sprintf("party_members 含重复成员: %s", uuid))
		}
		seenUUID[uuid] = true
	}

	uids := make([]int64, 0, len(members))
	for _, uuid := range members {
		memberUID, err := resolveUUID(ctx, s.authClient, s.redis, uuid)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrUserNotFound):
				return nil, apperr.New(apperr.CodeMCAUserNotFound,
					fmt.Sprintf("队伍成员 (uuid: %s) 在 HRPAuth 中不存在", uuid))
			case errors.Is(err, auth.ErrServiceAuth):
				return nil, apperr.New(apperr.CodeOAuthHRPAuthDown,
					"HRPAuth 服务凭据不可用，无法解析队伍成员身份")
			default:
				return nil, apperr.New(apperr.CodeOAuthHRPAuthDown,
					fmt.Sprintf("解析队伍成员 (uuid: %s) 身份失败", uuid))
			}
		}
		uids = append(uids, memberUID)
	}

	// 成员不得已在其他队伍中（队伍入队期间必然在队）。
	for _, memberUID := range uids {
		if in, _, err := s.partyOf(ctx, memberUID); err != nil {
			return nil, err
		} else if in {
			return nil, apperr.New(apperr.CodeMCAPartyAlreadyInParty,
				fmt.Sprintf("成员 (uid: %d) 已在其他队伍中", memberUID))
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	// 临时队伍：leader_uid 记录名单首位（成局/离队时按 party_id 清理）。
	res, err := tx.ExecContext(ctx,
		"INSERT INTO matchmaking_parties (leader_uid, match_type) VALUES (?, ?)",
		uids[0], mode.MatchType,
	)
	if err != nil {
		return nil, fmt.Errorf("创建队伍失败: %w", err)
	}
	partyID, _ := res.LastInsertId()

	entries := make([]*QueueEntry, 0, len(uids))
	for _, memberUID := range uids {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO matchmaking_party_members (party_id, user_uid) VALUES (?, ?)",
			partyID, memberUID,
		); err != nil {
			var me *gomysql.MySQLError
			if errors.As(err, &me) && me.Number == 1062 {
				return nil, apperr.New(apperr.CodeMCAPartyAlreadyInParty, "队伍中有成员已在其他队伍中")
			}
			return nil, fmt.Errorf("写入队伍成员失败: %w", err)
		}
		score, err := s.getUserRankScore(ctx, memberUID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO matchmaking_queue (user_uid, match_type, rank_score, party_id) VALUES (?, ?, ?, ?)",
			memberUID, mode.MatchType, score, partyID,
		); err != nil {
			var me *gomysql.MySQLError
			if errors.As(err, &me) && me.Number == 1062 {
				return nil, apperr.New(apperr.CodeMCAQueueAlreadyJoined, "队伍中有成员已在匹配队列中")
			}
			return nil, fmt.Errorf("加入匹配队列失败: %w", err)
		}
		entries = append(entries, &QueueEntry{UID: memberUID, MatchType: mode.MatchType, RankScore: score, PartyID: partyID})
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	metrics.Default.Inc(metrics.PartiesCreatedTotal, 1)
	metrics.Default.Inc(metrics.MatchmakingJoinTotal, int64(len(entries)))
	return entries, nil
}

// insertQueueEntry 写入单条队列记录并回读 enqueued_at。
func (s *matchmakingService) insertQueueEntry(ctx context.Context, uid int64, matchType string, score int, partyID *int64) (*QueueEntry, error) {
	var partyArg any
	if partyID != nil {
		partyArg = *partyID
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO matchmaking_queue (user_uid, match_type, rank_score, party_id) VALUES (?, ?, ?, ?)",
		uid, matchType, score, partyArg,
	); err != nil {
		var me *gomysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			return nil, apperr.New(apperr.CodeMCAQueueAlreadyJoined, "已在匹配队列中")
		}
		return nil, fmt.Errorf("加入匹配队列失败: %w", err)
	}
	entry := &QueueEntry{UID: uid, MatchType: matchType, RankScore: score}
	if partyID != nil {
		entry.PartyID = *partyID
	}
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

// partyOf 返回用户当前所在队伍（若有）。
func (s *matchmakingService) partyOf(ctx context.Context, uid int64) (bool, int64, error) {
	var partyID int64
	err := s.db.QueryRowContext(ctx,
		"SELECT party_id FROM matchmaking_party_members WHERE user_uid = ?", uid,
	).Scan(&partyID)
	if err == sql.ErrNoRows {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("查询队伍归属失败: %w", err)
	}
	return true, partyID, nil
}

// Leave 离开匹配队列；以队伍入队时整队移出并清理临时队伍。
// 不在队列中返回 404。
func (s *matchmakingService) Leave(ctx context.Context, uid int64) error {
	var partyID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT party_id FROM matchmaking_queue WHERE user_uid = ?", uid,
	).Scan(&partyID)
	if err == sql.ErrNoRows {
		return apperr.New(apperr.CodeMCAQueueNotJoined, "未在匹配队列中")
	}
	if err != nil {
		return fmt.Errorf("查询排队记录失败: %w", err)
	}

	var res sql.Result
	if partyID.Valid {
		res, err = s.db.ExecContext(ctx,
			"DELETE FROM matchmaking_queue WHERE party_id = ?", partyID.Int64,
		)
	} else {
		res, err = s.db.ExecContext(ctx,
			"DELETE FROM matchmaking_queue WHERE user_uid = ?", uid,
		)
	}
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
	if partyID.Valid {
		// 整队离队即清理临时队伍（成员级联移除）。
		if _, err := s.db.ExecContext(ctx,
			"DELETE FROM matchmaking_parties WHERE id = ?", partyID.Int64,
		); err != nil {
			return fmt.Errorf("清理队伍失败: %w", err)
		}
	}
	metrics.Default.Inc(metrics.MatchmakingLeaveTotal, n)
	return nil
}

// Status 查询用户排队状态与本人相关的近期成局记录（最多 10 局）。
func (s *matchmakingService) Status(ctx context.Context, uid int64) (*MatchmakingStatus, error) {
	st := &MatchmakingStatus{RecentGroups: []FormedGroup{}}

	var entry QueueEntry
	var partyID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT user_uid, match_type, rank_score, party_id, enqueued_at FROM matchmaking_queue WHERE user_uid = ?", uid,
	).Scan(&entry.UID, &entry.MatchType, &entry.RankScore, &partyID, &entry.EnqueuedAt)
	if err == nil {
		st.InQueue = true
		if partyID.Valid {
			entry.PartyID = partyID.Int64
		}
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
	s.attachJoinTarget(st.RecentGroups)

	stats, err := s.queueStats(ctx)
	if err != nil {
		return nil, err
	}
	st.QueueStats = stats
	return st, nil
}

// queueStats 全局队列规模统计（只读）：排队玩家数、队伍数（按 party
// 计，单人不计）、平均等待时长（秒，数据库侧计算避免时区偏差）。
func (s *matchmakingService) queueStats(ctx context.Context) (QueueStats, error) {
	var stats QueueStats
	var avgWait float64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(DISTINCT party_id), COALESCE(AVG(TIMESTAMPDIFF(SECOND, enqueued_at, NOW())), 0)
		 FROM matchmaking_queue`,
	).Scan(&stats.Players, &stats.Parties, &avgWait)
	if err != nil {
		return QueueStats{}, fmt.Errorf("查询队列统计失败: %w", err)
	}
	stats.AverageWaitSeconds = int64(math.Round(avgWait))
	return stats, nil
}

// attachJoinTarget 为成局记录附加游戏服务器地址（仅当配置了
// matchmaking.join_target 时）；未配置则字段省略，客户端行为不变。
func (s *matchmakingService) attachJoinTarget(groups []FormedGroup) {
	if !s.cfg.JoinTarget.Configured() {
		return
	}
	jt := &JoinTarget{Host: s.cfg.JoinTarget.Host, Port: s.cfg.JoinTarget.Port}
	for i := range groups {
		groups[i].JoinTarget = jt
	}
}

func (s *matchmakingService) groupMembers(ctx context.Context, groupID int64) ([]GroupMember, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT user_uid, team_id, rank_score, party_id FROM matchmaking_group_members WHERE group_id = ? ORDER BY id",
		groupID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询成局成员失败: %w", err)
	}
	defer rows.Close()

	members := []GroupMember{}
	for rows.Next() {
		var m GroupMember
		var partyID sql.NullInt64
		if err := rows.Scan(&m.UID, &m.TeamID, &m.RankScore, &partyID); err != nil {
			return nil, fmt.Errorf("解析成局成员失败: %w", err)
		}
		if partyID.Valid {
			m.PartyID = partyID.Int64
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// QueueDepth 按模式统计队列深度。
func (s *matchmakingService) QueueDepth(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT match_type, COUNT(*) FROM matchmaking_queue GROUP BY match_type",
	)
	if err != nil {
		return nil, fmt.Errorf("查询队列深度失败: %w", err)
	}
	defer rows.Close()
	depth := make(map[string]int64)
	for rows.Next() {
		var matchType string
		var n int64
		if err := rows.Scan(&matchType, &n); err != nil {
			return nil, fmt.Errorf("解析队列深度失败: %w", err)
		}
		depth[matchType] = n
	}
	return depth, rows.Err()
}

// MatchmakingEngine 后台匹配引擎：周期性移除超时排队并按分差窗口成局。
//
// 匹配单位：单人或整队（party）。队伍成员必定进入同一成局并分配在同一
// 队伍（team）内；成局按模式各队人数装满为止。超时与等待时长一律在
// 数据库侧计算（NOW()/TIMESTAMPDIFF），避免驱动时区偏差。
//
// 并发安全：成局事务以"按 id 删除队列行并校验影响行数"作竞态守卫，
// 行数不符即回滚（另一实例已抢先成局）。队列存于 MySQL，进程重启后
// 由首轮 tick 按超时规则清理，天然恢复。
type MatchmakingEngine struct {
	db  *sql.DB
	cfg config.MatchmakingConfig
}

// NewMatchmakingEngine 创建匹配引擎。
func NewMatchmakingEngine(db *sql.DB, cfg config.MatchmakingConfig) *MatchmakingEngine {
	return &MatchmakingEngine{db: db, cfg: cfg}
}

// Start 启动后台匹配循环：先立即执行一轮（进程重启后及时清理超时
// 条目），随后按周期执行，随 ctx 取消退出。
func (e *MatchmakingEngine) Start(ctx context.Context) {
	go func() {
		if err := e.Tick(ctx); err != nil {
			log.Printf("匹配引擎首轮 tick 失败: %v", err)
		}
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
	res, err := e.db.ExecContext(ctx,
		"DELETE FROM matchmaking_queue WHERE TIMESTAMPDIFF(SECOND, enqueued_at, NOW()) >= ?",
		e.cfg.QueueTimeoutSeconds,
	)
	if err != nil {
		return fmt.Errorf("移除超时排队失败: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		metrics.Default.Inc(metrics.QueueTimeoutRemovedTotal, n)
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
	PartyID       int64 // 0 = 单人
	WaitedSeconds int64
}

// matchUnit 匹配单位：单人或整队。
type matchUnit struct {
	rows    []queueRow
	rating  int // 单人=个人分；队伍=成员平均分
	waited  int64
	partyID int64
}

// formGroups 对单个模式反复成局，直到剩余队列无法凑满一局。
//
// 贪心策略：以等待最久的单位为轴，窗口取其等待时长对应的分差窗口；
// 在窗口内的单位按等待时长优先装入各队（队伍必须整队装入同一 team），
// 装满模式所需的全部 team 即成局。
func (e *MatchmakingEngine) formGroups(ctx context.Context, mode config.MatchMode) error {
	need := mode.TotalPlayers()
	for {
		rows, err := e.db.QueryContext(ctx,
			`SELECT id, user_uid, rank_score, COALESCE(party_id, 0), TIMESTAMPDIFF(SECOND, enqueued_at, NOW()) AS waited_seconds
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
			if err := rows.Scan(&r.ID, &r.UID, &r.RankScore, &r.PartyID, &r.WaitedSeconds); err != nil {
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

		units := buildUnits(queue)
		// 轴：等待最久（其次评分更高）。
		sort.SliceStable(units, func(i, j int) bool {
			if units[i].waited != units[j].waited {
				return units[i].waited > units[j].waited
			}
			return units[i].rating > units[j].rating
		})

		window := e.ratingWindow(time.Duration(units[0].waited) * time.Second)
		candidates := make([]*matchUnit, 0, len(units))
		for i := range units {
			diff := units[i].rating - units[0].rating
			if diff < 0 {
				diff = -diff
			}
			if diff <= window {
				candidates = append(candidates, &units[i])
			}
		}

		selected := packUnits(candidates, mode.TeamSizes)
		if selected == nil {
			return nil // 窗口内装不满，等待窗口放宽或新成员入队
		}

		if err := e.commitGroup(ctx, mode, selected); err != nil {
			return err
		}
	}
}

// buildUnits 将队列行聚合为匹配单位（同 party_id 的行合并为一队）。
func buildUnits(queue []queueRow) []matchUnit {
	var units []matchUnit
	partyIndex := make(map[int64]int) // party_id → units 下标
	for _, r := range queue {
		if r.PartyID != 0 {
			if idx, ok := partyIndex[r.PartyID]; ok {
				u := &units[idx]
				u.rows = append(u.rows, r)
				u.waited = maxInt64(u.waited, r.WaitedSeconds)
				continue
			}
			partyIndex[r.PartyID] = len(units)
			units = append(units, matchUnit{rows: []queueRow{r}, waited: r.WaitedSeconds, partyID: r.PartyID})
			continue
		}
		units = append(units, matchUnit{rows: []queueRow{r}, waited: r.WaitedSeconds})
	}
	for i := range units {
		sum := 0
		for _, r := range units[i].rows {
			sum += r.RankScore
		}
		units[i].rating = (sum + len(units[i].rows)/2) / len(units[i].rows)
	}
	return units
}

// packedUnit 装箱结果：被选中的单位及其分配的队伍下标。
type packedUnit struct {
	unit    *matchUnit
	teamIdx int
}

// packUnits 贪心装箱：按候选顺序把单位装入各队（单位不可拆分）。
// 装满全部队伍返回装箱结果；装不满返回 nil。
func packUnits(candidates []*matchUnit, teamSizes []int) []packedUnit {
	remaining := make([]int, len(teamSizes))
	copy(remaining, teamSizes)
	var packed []packedUnit
	for _, u := range candidates {
		size := len(u.rows)
		for ti := range remaining {
			if remaining[ti] >= size {
				remaining[ti] -= size
				packed = append(packed, packedUnit{unit: u, teamIdx: ti})
				break
			}
		}
	}
	for _, n := range remaining {
		if n != 0 {
			return nil
		}
	}
	return packed
}

// commitGroup 在单个事务内写入成局记录、移出队列并清理已成局的队伍。
// 并发守卫：删除队列行后校验影响行数，不符即回滚。
func (e *MatchmakingEngine) commitGroup(ctx context.Context, mode config.MatchMode, packed []packedUnit) error {
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

	rowCount := 0
	partyIDs := make(map[int64]bool)
	for _, p := range packed {
		teamID := string(rune('A' + p.teamIdx))
		for _, r := range p.unit.rows {
			var partyArg any
			if p.unit.partyID != 0 {
				partyArg = p.unit.partyID
				partyIDs[p.unit.partyID] = true
			}
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO matchmaking_group_members (group_id, user_uid, team_id, rank_score, party_id) VALUES (?, ?, ?, ?, ?)",
				groupID, r.UID, teamID, r.RankScore, partyArg,
			); err != nil {
				return fmt.Errorf("写入成局成员失败: %w", err)
			}
			rowCount++
		}
	}

	ids := make([]string, 0, rowCount)
	args := make([]any, 0, rowCount)
	for _, p := range packed {
		for _, r := range p.unit.rows {
			ids = append(ids, "?")
			args = append(args, r.ID)
		}
	}
	delRes, err := tx.ExecContext(ctx,
		"DELETE FROM matchmaking_queue WHERE id IN ("+strings.Join(ids, ",")+")", args...,
	)
	if err != nil {
		return fmt.Errorf("移出队列失败: %w", err)
	}
	if n, _ := delRes.RowsAffected(); n != int64(rowCount) {
		// 并发竞态：队列行已被其他实例处理，放弃本次成局。
		return nil
	}

	for partyID := range partyIDs {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM matchmaking_parties WHERE id = ?", partyID,
		); err != nil {
			return fmt.Errorf("清理已成交队伍失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	metrics.Default.Inc(metrics.GroupsFormedPrefix+mode.MatchType, 1)
	return nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
