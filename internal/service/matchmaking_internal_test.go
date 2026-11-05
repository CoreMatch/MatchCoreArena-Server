package service

import (
	"testing"
	"time"

	"MatchCoreArena-Server/config"
)

// TestAssignTeamsSnakeDraft 覆盖蛇形分队：评分降序后
// 1 号位→A、2 号位→B、3 号位→B、4 号位→A（2v2）。
func TestAssignTeamsSnakeDraft(t *testing.T) {
	mode := config.MatchMode{MatchType: "2v2", TeamSizes: []int{2, 2}}
	selected := []queueRow{
		{UID: 1, RankScore: 1000},
		{UID: 2, RankScore: 1600},
		{UID: 3, RankScore: 1400},
		{UID: 4, RankScore: 1200},
	}

	got := assignTeams(mode, selected)
	if len(got) != 4 {
		t.Fatalf("assignments = %d, want 4", len(got))
	}
	teamOf := map[int64]string{}
	for _, a := range got {
		teamOf[a.row.UID] = a.teamID
	}
	// 降序：1600(2)→A，1400(3)→B，1200(4)→B，1000(1)→A。
	want := map[int64]string{2: "A", 3: "B", 4: "B", 1: "A"}
	for uid, team := range want {
		if teamOf[uid] != team {
			t.Errorf("uid %d team = %q, want %q", uid, teamOf[uid], team)
		}
	}
}

// TestAssignTeamsSolo 覆盖 1v1：两人各成一队（A、B）。
func TestAssignTeamsSolo(t *testing.T) {
	mode := config.MatchMode{MatchType: "1v1", TeamSizes: []int{1, 1}}
	got := assignTeams(mode, []queueRow{
		{UID: 1, RankScore: 100},
		{UID: 2, RankScore: 200},
	})
	if got[0].teamID != "A" || got[1].teamID != "B" {
		t.Fatalf("teams = %q/%q, want A/B", got[0].teamID, got[1].teamID)
	}
	if got[0].row.UID != 2 {
		t.Errorf("first assignment uid = %d, want 2 (higher rating first)", got[0].row.UID)
	}
}

// TestRatingWindow 覆盖分差窗口随等待时长线性放宽并封顶。
func TestRatingWindow(t *testing.T) {
	e := &MatchmakingEngine{cfg: config.MatchmakingConfig{
		BaseRatingWindow:            200,
		RatingWindowGrowthPerMinute: 100,
		MaxRatingWindow:             1000,
	}}

	cases := []struct {
		waited time.Duration
		want   int
	}{
		{0, 200},
		{30 * time.Second, 250},
		{2 * time.Minute, 400},
		{10 * time.Minute, 1000},  // 200+1000 → 封顶 1000
		{60 * time.Minute, 1000}, // 封顶
	}
	for _, tc := range cases {
		if got := e.ratingWindow(tc.waited); got != tc.want {
			t.Errorf("ratingWindow(%v) = %d, want %d", tc.waited, got, tc.want)
		}
	}
}
