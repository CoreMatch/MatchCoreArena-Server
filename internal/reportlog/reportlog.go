// Package reportlog 以有界内存环记录最近的对局上报拒绝
//（mca_report_inconsistent / mca_game_id_conflict），供 OPS 调试端点
// 只读暴露。记录只含时间、错误码、game_id 与脱敏原因摘要，
// 绝不保存上报载荷或玩家敏感字段。
package reportlog

import (
	"regexp"
	"sync"
	"time"

	"MatchCoreArena-Server/internal/apperr"
)

// MaxEntries 环形容量（有界内存，够 debug 即可）。
const MaxEntries = 200

// maxReasonLen 原因摘要长度上限。
const maxReasonLen = 200

// Record 单条拒绝记录。
type Record struct {
	Time   string `json:"time"`
	Code   string `json:"code"`
	GameID string `json:"game_id,omitempty"`
	Reason string `json:"reason"`
}

// Ring 并发安全的有界环形缓冲。
type Ring struct {
	mu      sync.Mutex
	entries []Record
}

// Default 进程级默认环。
var Default = NewRing()

// NewRing 创建空环。
func NewRing() *Ring {
	return &Ring{entries: make([]Record, 0, MaxEntries)}
}

// uuidPattern Minecraft UUID 形态；原因摘要入库前统一打码。
var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// Record 追加一条拒绝记录：打码 UUID、截断超长摘要；满容量时丢弃最旧。
func (r *Ring) Record(code apperr.Code, gameID, message string) {
	reason := uuidPattern.ReplaceAllString(message, "<uuid>")
	if len(reason) > maxReasonLen {
		reason = reason[:maxReasonLen]
	}
	rec := Record{
		Time:   time.Now().UTC().Format(time.RFC3339),
		Code:   string(code),
		GameID: gameID,
		Reason: reason,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) >= MaxEntries {
		r.entries = r.entries[1:]
	}
	r.entries = append(r.entries, rec)
}

// Snapshot 返回当前记录副本（最旧在前、最新在后）。
func (r *Ring) Snapshot() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, len(r.entries))
	copy(out, r.entries)
	return out
}
