// Package metrics 提供轻量内存指标注册表（无外部依赖）。
// 计数器按带标签的键累加（如 "matches_rejected_total:mca_user_not_found"），
// 由 GET /api/metrics 输出快照。
package metrics

import (
	"sort"
	"sync"
)

// 常用计数器键前缀。
const (
	MatchesReportedTotal       = "matches_reported_total"
	MatchesReportedReplayTotal = "matches_reported_replay_total"
	MatchesRejectedPrefix      = "matches_rejected_total:"
	GroupsFormedPrefix         = "matchmaking_groups_formed_total:"
	MatchmakingJoinTotal       = "matchmaking_join_total"
	MatchmakingLeaveTotal      = "matchmaking_leave_total"
	QueueTimeoutRemovedTotal   = "matchmaking_queue_timeout_removed_total"
	PartiesCreatedTotal        = "matchmaking_parties_created_total"
	RoomsReservedTotal         = "matchmaking_rooms_reserved_total"
	RoomsExpiredTotal          = "matchmaking_rooms_expired_total"
	RoomsReleasedSettledTotal  = "matchmaking_rooms_released_settled_total"
	RoomlessFormationsBlocked  = "matchmaking_roomless_formations_blocked_total"
)

// Registry 并发安全的计数器集合。
type Registry struct {
	mu       sync.Mutex
	counters map[string]int64
}

// NewRegistry 创建指标注册表。
func NewRegistry() *Registry {
	return &Registry{counters: make(map[string]int64)}
}

// Default 进程级默认注册表。
var Default = NewRegistry()

// Inc 将键对应的计数器增加 n。
func (r *Registry) Inc(key string, n int64) {
	r.mu.Lock()
	r.counters[key] += n
	r.mu.Unlock()
}

// Snapshot 返回全部计数器的副本（按键排序由调用方处理）。
func (r *Registry) Snapshot() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int64, len(r.counters))
	for k, v := range r.counters {
		out[k] = v
	}
	return out
}

// SortedKeys 返回快照键的排序结果（稳定输出）。
func SortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
