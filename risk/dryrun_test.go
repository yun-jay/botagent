package risk_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/yunus/botagent/risk"
	"github.com/yunus/botagent/signal"
)

func TestDryRun_Enabled(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	dr := risk.NewDryRun(true, logger)

	sig := signal.Signal{
		ID:         "sig-1",
		Instrument: "TOKEN-A",
		Direction:  signal.Buy,
		Edge:       0.05,
	}

	if !dr.ShouldSkip(sig, 50.0) {
		t.Error("ShouldSkip should return true when enabled")
	}

	// Verify log output
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON log: %v", err)
	}
	if !strings.Contains(entry["msg"].(string), "DRY RUN") {
		t.Errorf("log should contain DRY RUN, got: %v", entry["msg"])
	}
	if entry["instrument"] != "TOKEN-A" {
		t.Errorf("instrument = %v, want TOKEN-A", entry["instrument"])
	}
}

func TestDryRun_Disabled(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	dr := risk.NewDryRun(false, logger)

	sig := signal.Signal{ID: "sig-1"}
	if dr.ShouldSkip(sig, 50.0) {
		t.Error("ShouldSkip should return false when disabled")
	}
	if buf.Len() != 0 {
		t.Error("should not log when disabled")
	}
}
