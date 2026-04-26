package risk

import (
	"log/slog"

	"github.com/yun-jay/botagent/signal"
)

// DryRun wraps execution with a paper-trading check.
type DryRun struct {
	Enabled bool
	Logger  *slog.Logger
}

// NewDryRun creates a new DryRun.
func NewDryRun(enabled bool, logger *slog.Logger) *DryRun {
	return &DryRun{Enabled: enabled, Logger: logger}
}

// ShouldSkip returns true if dry-run mode is enabled.
// Logs the signal details when skipping.
func (dr *DryRun) ShouldSkip(sig signal.Signal, size float64) bool {
	if !dr.Enabled {
		return false
	}
	dr.Logger.Info("DRY RUN: skipping execution",
		"signal_id", sig.ID,
		"instrument", sig.Instrument,
		"direction", sig.Direction.String(),
		"edge", sig.Edge,
		"size", size,
	)
	return true
}
