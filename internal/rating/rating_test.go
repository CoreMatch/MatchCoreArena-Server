package rating

import (
	"math"
	"testing"
)

func TestCalculateEloExpected(t *testing.T) {
	// 相同分数，胜率应为 0.5
	e := CalculateEloExpected(1000, 1000)
	if math.Abs(e-0.5) > 0.001 {
		t.Errorf("Expected 0.5, got %f", e)
	}

	// 玩家分数更高，胜率应 > 0.5
	e = CalculateEloExpected(2000, 1000)
	if e <= 0.5 {
		t.Errorf("Expected > 0.5, got %f", e)
	}
}

func TestGetKFactor(t *testing.T) {
	// 第 1 场胜场
	k1 := GetKFactor(0)
	if k1 != 520.0 {
		t.Errorf("Expected 520.0, got %f", k1)
	}

	// 第 10 场胜场
	k10 := GetKFactor(9)
	if math.Abs(k10-390.0) > 0.001 {
		t.Errorf("Expected 390.0, got %f", k10)
	}

	// 稳定期
	kStable := GetKFactor(10)
	if kStable != 190.0 {
		t.Errorf("Expected 190.0, got %f", kStable)
	}
}

func TestGetRoundWeight(t *testing.T) {
	// 13-11 (diff=2)
	w := GetRoundWeight(13, 11)
	if w != 1.05 {
		t.Errorf("Expected 1.05, got %f", w)
	}

	// 13-0 (diff=13)
	w = GetRoundWeight(13, 0)
	if w != 1.30 {
		t.Errorf("Expected 1.30, got %f", w)
	}
}

func TestGetStreakWeight(t *testing.T) {
	// 连胜 3 场
	w := GetStreakWeight(3)
	if w != 1.15 {
		t.Errorf("Expected 1.15, got %f", w)
	}

	// 连败 5 场
	w = GetStreakWeight(-5)
	if w != 0.73 {
		t.Errorf("Expected 0.73, got %f", w)
	}
}

func TestCalculateDelta(t *testing.T) {
	// 示例推导：K=480, S=1, E=0.35, W_round=1.20, W_streak=1.10, W_perf=1.00
	// delta = 480 * (1 - 0.35) * 1.20 * 1.10 * 1.00 = 411.84 -> 412
	delta := CalculateDelta(480, 1.0, 0.35, 1.20, 1.10, 1.00)
	if delta != 412 {
		t.Errorf("Expected 412, got %d", delta)
	}
}
