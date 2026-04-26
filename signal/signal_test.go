package signal_test

import (
	"testing"
	"time"

	"github.com/yun-jay/botagent/signal"
)

func TestDirection_String(t *testing.T) {
	tests := []struct {
		dir  signal.Direction
		want string
	}{
		{signal.Buy, "BUY"},
		{signal.Sell, "SELL"},
		{signal.Direction(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		if got := tt.dir.String(); got != tt.want {
			t.Errorf("Direction(%d).String() = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

func TestSignal_Fields(t *testing.T) {
	now := time.Now()
	sig := signal.Signal{
		ID:         "sig-001",
		Timestamp:  now,
		Source:     "test",
		MarketID:   "market-1",
		Instrument: "TOKEN-A",
		Direction:  signal.Buy,
		TrueProb:   0.65,
		MarketProb: 0.55,
		Edge:       0.10,
		Confidence: 0.9,
		Metadata:   map[string]any{"key": "value"},
	}

	if sig.ID != "sig-001" {
		t.Errorf("ID = %q, want %q", sig.ID, "sig-001")
	}
	if sig.Direction != signal.Buy {
		t.Errorf("Direction = %v, want Buy", sig.Direction)
	}
	if sig.Edge != 0.10 {
		t.Errorf("Edge = %v, want 0.10", sig.Edge)
	}
	if sig.Metadata["key"] != "value" {
		t.Errorf("Metadata[key] = %v, want %q", sig.Metadata["key"], "value")
	}
}
