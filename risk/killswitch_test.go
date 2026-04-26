package risk_test

import (
	"bytes"
	"log/slog"
	"math"
	"testing"

	"github.com/yunus/botagent/risk"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func TestKillSwitch_TriggersAtThreshold(t *testing.T) {
	ks := risk.NewKillSwitch(0.25, 1000, testLogger())

	if ks.IsTriggered() {
		t.Error("should not be triggered initially")
	}

	// 20% drawdown — below threshold
	triggered := ks.UpdatePortfolioValue(800)
	if triggered || ks.IsTriggered() {
		t.Error("should not trigger at 20% (threshold is 25%)")
	}

	// 25% drawdown — at threshold
	triggered = ks.UpdatePortfolioValue(750)
	if !triggered || !ks.IsTriggered() {
		t.Error("should trigger at 25% drawdown")
	}
}

func TestKillSwitch_DoesNotRetrigger(t *testing.T) {
	ks := risk.NewKillSwitch(0.25, 1000, testLogger())
	ks.UpdatePortfolioValue(750) // trigger

	// Further drawdown should not re-trigger
	triggered := ks.UpdatePortfolioValue(500)
	if triggered {
		t.Error("should not re-trigger once already triggered")
	}
	if !ks.IsTriggered() {
		t.Error("should still be triggered")
	}
}

func TestKillSwitch_TriggerSilently(t *testing.T) {
	ks := risk.NewKillSwitch(0.25, 1000, testLogger())
	ks.TriggerSilently()

	if !ks.IsTriggered() {
		t.Error("TriggerSilently should set triggered state")
	}
}

func TestKillSwitch_ResetDaily(t *testing.T) {
	ks := risk.NewKillSwitch(0.25, 1000, testLogger())
	ks.UpdatePortfolioValue(750) // trigger

	ks.ResetDaily(900)
	if ks.IsTriggered() {
		t.Error("ResetDaily should clear triggered state")
	}
	stats := ks.Stats()
	if stats.StartOfDayValue != 900 {
		t.Errorf("start of day = %v, want 900", stats.StartOfDayValue)
	}
}

func TestKillSwitch_CurrentDrawdown(t *testing.T) {
	ks := risk.NewKillSwitch(0.25, 1000, testLogger())
	ks.UpdatePortfolioValue(850)

	dd := ks.CurrentDrawdown()
	if math.Abs(dd-0.15) > 0.001 {
		t.Errorf("drawdown = %v, want 0.15", dd)
	}
}

func TestKillSwitch_CurrentDrawdown_Profit(t *testing.T) {
	ks := risk.NewKillSwitch(0.25, 1000, testLogger())
	ks.UpdatePortfolioValue(1100) // profit

	dd := ks.CurrentDrawdown()
	if dd != 0 {
		t.Errorf("drawdown should be 0 in profit, got %v", dd)
	}
}

func TestKillSwitch_RiskMultiplier_FullSize(t *testing.T) {
	ks := risk.NewKillSwitch(0.20, 1000, testLogger())

	// No drawdown
	if rm := ks.RiskMultiplier(); rm != 1.0 {
		t.Errorf("RiskMultiplier = %v, want 1.0 (no drawdown)", rm)
	}

	// 5% drawdown (below 50% of 20% = 10%)
	ks.UpdatePortfolioValue(950)
	if rm := ks.RiskMultiplier(); rm != 1.0 {
		t.Errorf("RiskMultiplier = %v, want 1.0 (5%% drawdown)", rm)
	}
}

func TestKillSwitch_RiskMultiplier_Ramp(t *testing.T) {
	ks := risk.NewKillSwitch(0.20, 1000, testLogger())

	// 15% drawdown = 75% of threshold, in ramp zone
	ks.UpdatePortfolioValue(850)
	rm := ks.RiskMultiplier()
	if rm >= 1.0 || rm <= 0.0 {
		t.Errorf("RiskMultiplier = %v, should be between 0 and 1 in ramp zone", rm)
	}
	// At 15% with threshold 20% and ramp starting at 10%:
	// rm = 1.0 - (0.15 - 0.10) / (0.20 - 0.10) = 1.0 - 0.5 = 0.5
	if math.Abs(rm-0.5) > 0.01 {
		t.Errorf("RiskMultiplier = %v, want ~0.5", rm)
	}
}

func TestKillSwitch_RiskMultiplier_Zero(t *testing.T) {
	ks := risk.NewKillSwitch(0.20, 1000, testLogger())
	ks.UpdatePortfolioValue(800) // exactly at threshold

	rm := ks.RiskMultiplier()
	if rm != 0.0 {
		t.Errorf("RiskMultiplier = %v, want 0.0 at threshold", rm)
	}
}

func TestKillSwitch_Stats(t *testing.T) {
	ks := risk.NewKillSwitch(0.25, 1000, testLogger())
	ks.UpdatePortfolioValue(850)

	stats := ks.Stats()
	if stats.Triggered {
		t.Error("should not be triggered")
	}
	if stats.StartOfDayValue != 1000 {
		t.Errorf("start of day = %v, want 1000", stats.StartOfDayValue)
	}
	if stats.CurrentValue != 850 {
		t.Errorf("current value = %v, want 850", stats.CurrentValue)
	}
	if math.Abs(stats.CurrentDrawdown-0.15) > 0.001 {
		t.Errorf("drawdown = %v, want 0.15", stats.CurrentDrawdown)
	}
	if stats.MaxDrawdown != 0.25 {
		t.Errorf("max drawdown = %v, want 0.25", stats.MaxDrawdown)
	}
}

func TestKillSwitch_ZeroStartValue(t *testing.T) {
	ks := risk.NewKillSwitch(0.25, 0, testLogger())
	triggered := ks.UpdatePortfolioValue(100)
	if triggered {
		t.Error("should not trigger with zero start value")
	}
	if ks.CurrentDrawdown() != 0 {
		t.Error("drawdown should be 0 with zero start value")
	}
	if ks.RiskMultiplier() != 1.0 {
		t.Error("risk multiplier should be 1.0 with zero start value")
	}
}
