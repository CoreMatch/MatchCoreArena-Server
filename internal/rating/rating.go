package rating

import (
	"math"
)

// Constants based on CS2 Premier Rating research report
const (
	// Placement phase (拟定级阶段)
	PlacementWinsThreshold = 10
	KPlacementStart        = 520.0
	KPlacementEnd          = 390.0

	// Stability phase (稳定期)
	KStabilityMin = 165.0
	KStabilityMax = 215.0
)

// CalculateEloExpected 计算期望胜率 (Elo 公式)
// E = 1 / (1 + 10^((R_opponent - R_player) / 400))
func CalculateEloExpected(playerRating, opponentRating float64) float64 {
	return 1.0 / (1.0 + math.Pow(10, (opponentRating-playerRating)/400.0))
}

// GetKFactor 获取基础权重 (K-factor)
// n: 当前已获胜场次
func GetKFactor(wins int) float64 {
	if wins < PlacementWinsThreshold {
		// 定级期权重随场次递减: K = K_start - (n * (K_start - K_end) / N)
		// 注意：这里 n 是指当前正在进行的第几场胜利，文档中 n 为 1-10
		n := float64(wins + 1)
		N := float64(PlacementWinsThreshold)
		return KPlacementStart - (n-1)*(KPlacementStart-KPlacementEnd)/(N-1)
	}
	// 稳定期取平均值，实际可能随活跃度微调，这里简化取中间值
	return (KStabilityMin + KStabilityMax) / 2.0
}

// GetRoundWeight 获取回合差权重 (W_round)
func GetRoundWeight(winnerScore, loserScore int) float64 {
	diff := int(math.Abs(float64(winnerScore - loserScore)))
	switch {
	case diff <= 1:
		return 1.00
	case diff <= 3:
		return 1.05
	case diff <= 6:
		return 1.10
	case diff <= 10:
		return 1.20
	default:
		return 1.30
	}
}

// GetStreakWeight 获取连胜/连败权重 (W_streak)
// streak: 连胜为正，连败为负
func GetStreakWeight(streak int) float64 {
	absStreak := int(math.Abs(float64(streak)))
	if streak >= 0 { // 连胜
		switch {
		case absStreak <= 2:
			return 1.00
		case absStreak <= 4:
			return 1.15 // 取 1.10-1.20 中间值
		default:
			return 1.28 // 取 1.25-1.30 中间值
		}
	} else { // 连败
		switch {
		case absStreak <= 2:
			return 1.00
		case absStreak <= 4:
			return 0.85 // 取 0.90-0.80 中间值
		default:
			return 0.73 // 取 0.75-0.70 中间值
		}
	}
}

// CalculateDelta 计算分数变化
// delta = K * (S - E) * W_round * W_streak * W_perf
func CalculateDelta(k, s, e, wRound, wStreak, wPerf float64) int {
	delta := k * (s - e) * wRound * wStreak * wPerf
	return int(math.Round(delta))
}
