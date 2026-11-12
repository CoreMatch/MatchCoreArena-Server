package reportlog

import (
	"fmt"
	"strings"
	"testing"

	"MatchCoreArena-Server/internal/apperr"
)

// TestRingBounded 覆盖有界性：超出容量丢弃最旧记录。
func TestRingBounded(t *testing.T) {
	r := NewRing()
	for i := 0; i < MaxEntries+50; i++ {
		r.Record(apperr.CodeMCAReportInconsistent, fmt.Sprintf("g-%d", i), "reason")
	}
	snap := r.Snapshot()
	if len(snap) != MaxEntries {
		t.Fatalf("entries = %d, want %d", len(snap), MaxEntries)
	}
	if snap[0].GameID != "g-50" {
		t.Errorf("oldest = %q, want g-50 (first 50 evicted)", snap[0].GameID)
	}
	if snap[len(snap)-1].GameID != fmt.Sprintf("g-%d", MaxEntries+49) {
		t.Errorf("newest = %q, want last inserted", snap[len(snap)-1].GameID)
	}
}

// TestRingSanitizesUUIDs 覆盖脱敏：原因摘要中的 Minecraft UUID 打码，
// 不向调试端点泄露玩家身份。
func TestRingSanitizesUUIDs(t *testing.T) {
	r := NewRing()
	const uuid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	r.Record(apperr.CodeMCAReportInconsistent, "", "参与者 uuid 重复: "+uuid)

	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("entries = %d, want 1", len(snap))
	}
	if strings.Contains(snap[0].Reason, uuid) {
		t.Errorf("reason 泄露 UUID: %q", snap[0].Reason)
	}
	if !strings.Contains(snap[0].Reason, "<uuid>") {
		t.Errorf("reason 未打码: %q", snap[0].Reason)
	}
}

// TestRingSnapshotIsCopy 覆盖快照独立性：修改快照不影响环。
func TestRingSnapshotIsCopy(t *testing.T) {
	r := NewRing()
	r.Record(apperr.CodeMCAGameIDConflict, "g-1", "conflict")
	snap := r.Snapshot()
	snap[0].Reason = "tampered"
	if got := r.Snapshot()[0].Reason; got != "conflict" {
		t.Errorf("ring mutated via snapshot: %q", got)
	}
}

// TestRingTruncatesLongReason 覆盖超长摘要截断。
func TestRingTruncatesLongReason(t *testing.T) {
	r := NewRing()
	r.Record(apperr.CodeMCAReportInconsistent, "", strings.Repeat("x", maxReasonLen+50))
	if got := len(r.Snapshot()[0].Reason); got != maxReasonLen {
		t.Errorf("reason len = %d, want %d", got, maxReasonLen)
	}
}
