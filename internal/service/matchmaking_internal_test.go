package service

import (
	"testing"
	"time"

	"MatchCoreArena-Server/config"
)

// TestBuildUnits 覆盖队列行聚合为匹配单位：同 party_id 的行合并为
// 一队（评分取平均、等待取最长），单人各自成单位。
func TestBuildUnits(t *testing.T) {
	queue := []queueRow{
		{ID: 1, UID: 11, RankScore: 1000, PartyID: 5, WaitedSeconds: 40},
		{ID: 2, UID: 12, RankScore: 1100, PartyID: 5, WaitedSeconds: 30},
		{ID: 3, UID: 13, RankScore: 1500, PartyID: 0, WaitedSeconds: 20},
	}

	units := buildUnits(queue)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2", len(units))
	}

	party := units[0]
	if party.partyID != 5 || len(party.rows) != 2 {
		t.Fatalf("party unit = %+v, want party 5 with 2 rows", party)
	}
	if party.rating != 1050 {
		t.Errorf("party rating = %d, want 1050 (average)", party.rating)
	}
	if party.waited != 40 {
		t.Errorf("party waited = %d, want 40 (max of members)", party.waited)
	}

	solo := units[1]
	if solo.partyID != 0 || len(solo.rows) != 1 || solo.rating != 1500 {
		t.Errorf("solo unit = %+v, want single row rating 1500", solo)
	}
}

// TestPackUnits 覆盖贪心装箱：队伍不可拆分、装满全部队伍才成功。
func TestPackUnits(t *testing.T) {
	t.Run("party stays together", func(t *testing.T) {
		party := &matchUnit{rows: []queueRow{{UID: 1}, {UID: 2}}, rating: 1000}
		soloA := &matchUnit{rows: []queueRow{{UID: 3}}, rating: 1000}
		soloB := &matchUnit{rows: []queueRow{{UID: 4}}, rating: 1000}

		packed := packUnits([]*matchUnit{party, soloA, soloB}, []int{2, 2})
		if packed == nil {
			t.Fatal("expected a full packing")
		}
		if len(packed) != 3 {
			t.Fatalf("packed = %d units, want 3", len(packed))
		}
		if packed[0].unit != party || packed[0].teamIdx != 0 {
			t.Errorf("party must fill team 0 whole, got %+v", packed[0])
		}
		if packed[1].teamIdx != 1 || packed[2].teamIdx != 1 {
			t.Errorf("solos must share team 1, got %d/%d", packed[1].teamIdx, packed[2].teamIdx)
		}
	})

	t.Run("oversized party cannot be split", func(t *testing.T) {
		big := &matchUnit{rows: []queueRow{{UID: 1}, {UID: 2}, {UID: 3}}, rating: 1000}
		solo := &matchUnit{rows: []queueRow{{UID: 4}}, rating: 1000}

		// 2v2：3 人队伍装不进任何单队（上限 2），无法装满 → nil。
		if got := packUnits([]*matchUnit{big, solo}, []int{2, 2}); got != nil {
			t.Fatalf("packing must fail for an oversized party, got %+v", got)
		}
	})

	t.Run("not enough candidates", func(t *testing.T) {
		solo := &matchUnit{rows: []queueRow{{UID: 1}}, rating: 1000}
		if got := packUnits([]*matchUnit{solo}, []int{1, 1}); got != nil {
			t.Fatalf("packing must fail when candidates cannot fill all teams, got %+v", got)
		}
	})
}

// TestAttachJoinTarget 覆盖成局记录附加游戏服务器地址：配置了
// join_target 时每条成局记录都携带；未配置时字段保持 nil（JSON 省略，
// 客户端回落本地配置）。
func TestAttachJoinTarget(t *testing.T) {
	t.Run("configured attaches to every group", func(t *testing.T) {
		s := &matchmakingService{cfg: config.MatchmakingConfig{
			JoinTarget: config.JoinTargetConfig{Host: "play.example.com", Port: 25565},
		}}
		groups := []FormedGroup{{GroupID: 1}, {GroupID: 2}}
		s.attachJoinTarget(groups)
		for _, g := range groups {
			if g.JoinTarget == nil || g.JoinTarget.Host != "play.example.com" || g.JoinTarget.Port != 25565 {
				t.Errorf("group %d join_target = %+v, want play.example.com:25565", g.GroupID, g.JoinTarget)
			}
		}
	})

	t.Run("unconfigured leaves nil", func(t *testing.T) {
		s := &matchmakingService{cfg: config.MatchmakingConfig{}}
		groups := []FormedGroup{{GroupID: 1}}
		s.attachJoinTarget(groups)
		if groups[0].JoinTarget != nil {
			t.Errorf("join_target = %+v, want nil when unconfigured", groups[0].JoinTarget)
		}
	})

	t.Run("empty host with port stays unconfigured", func(t *testing.T) {
		s := &matchmakingService{cfg: config.MatchmakingConfig{
			JoinTarget: config.JoinTargetConfig{Host: "", Port: 25565},
		}}
		groups := []FormedGroup{{GroupID: 1}}
		s.attachJoinTarget(groups)
		if groups[0].JoinTarget != nil {
			t.Errorf("join_target = %+v, want nil when host empty", groups[0].JoinTarget)
		}
	})
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
