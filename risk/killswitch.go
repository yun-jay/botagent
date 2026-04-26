package risk

import (
	"log/slog"
	"sync"
	"time"
)

// KillSwitch monitors drawdown and halts trading if thresholds are breached.
type KillSwitch struct {
	mu              sync.RWMutex
	maxDrawdownPct  float64
	startOfDayValue float64
	currentValue    float64
	triggered       bool
	triggeredAt     time.Time
	log             *slog.Logger
}

// NewKillSwitch creates a new KillSwitch.
func NewKillSwitch(maxDrawdownPct float64, initialPortfolioValue float64, logger *slog.Logger) *KillSwitch {
	return &KillSwitch{
		maxDrawdownPct:  maxDrawdownPct,
		startOfDayValue: initialPortfolioValue,
		currentValue:    initialPortfolioValue,
		log:             logger,
	}
}

// UpdatePortfolioValue updates the current portfolio value and checks the kill switch.
// Returns true if the kill switch was just triggered by this update.
func (ks *KillSwitch) UpdatePortfolioValue(value float64) bool {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	ks.currentValue = value

	if ks.startOfDayValue <= 0 {
		return false
	}

	drawdown := (ks.startOfDayValue - value) / ks.startOfDayValue
	if drawdown >= ks.maxDrawdownPct && !ks.triggered {
		ks.triggered = true
		ks.triggeredAt = time.Now()
		ks.log.Error("KILL SWITCH TRIGGERED",
			"drawdown_pct", drawdown*100,
			"threshold_pct", ks.maxDrawdownPct*100,
			"start_of_day", ks.startOfDayValue,
			"current", value,
		)
		return true
	}
	return false
}

// TriggerSilently triggers the kill switch without logging.
func (ks *KillSwitch) TriggerSilently() {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.triggered = true
	ks.triggeredAt = time.Now()
}

// IsTriggered returns whether the kill switch has been triggered.
func (ks *KillSwitch) IsTriggered() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.triggered
}

// CurrentDrawdown returns the current drawdown as a fraction [0,1].
func (ks *KillSwitch) CurrentDrawdown() float64 {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	if ks.startOfDayValue <= 0 {
		return 0
	}
	dd := (ks.startOfDayValue - ks.currentValue) / ks.startOfDayValue
	if dd < 0 {
		return 0
	}
	return dd
}

// ResetDaily resets the kill switch for a new trading day.
func (ks *KillSwitch) ResetDaily(newStartValue float64) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.startOfDayValue = newStartValue
	ks.currentValue = newStartValue
	ks.triggered = false
	ks.log.Info("kill switch reset for new day", "start_value", newStartValue)
}

// RiskMultiplier returns a scaling factor [0,1] for position sizing based on
// current drawdown. Provides gradual risk reduction before the kill switch triggers:
//   - 0% to 50% of maxDrawdown: full size (1.0)
//   - 50% to 100% of maxDrawdown: linear ramp from 1.0 to 0.0
func (ks *KillSwitch) RiskMultiplier() float64 {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	if ks.startOfDayValue <= 0 {
		return 1.0
	}
	drawdown := (ks.startOfDayValue - ks.currentValue) / ks.startOfDayValue
	if drawdown <= 0 {
		return 1.0
	}

	scaleStart := ks.maxDrawdownPct * 0.5
	if drawdown <= scaleStart {
		return 1.0
	}
	if drawdown >= ks.maxDrawdownPct {
		return 0.0
	}
	return 1.0 - (drawdown-scaleStart)/(ks.maxDrawdownPct-scaleStart)
}

// KillSwitchStats holds current kill switch state.
type KillSwitchStats struct {
	Triggered       bool
	TriggeredAt     time.Time
	CurrentDrawdown float64
	MaxDrawdown     float64
	StartOfDayValue float64
	CurrentValue    float64
}

// Stats returns current kill switch stats.
func (ks *KillSwitch) Stats() KillSwitchStats {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	dd := 0.0
	if ks.startOfDayValue > 0 {
		dd = (ks.startOfDayValue - ks.currentValue) / ks.startOfDayValue
		if dd < 0 {
			dd = 0
		}
	}
	return KillSwitchStats{
		Triggered:       ks.triggered,
		TriggeredAt:     ks.triggeredAt,
		CurrentDrawdown: dd,
		MaxDrawdown:     ks.maxDrawdownPct,
		StartOfDayValue: ks.startOfDayValue,
		CurrentValue:    ks.currentValue,
	}
}
