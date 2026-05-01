package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestGenerateTimestamps_5m(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cd := NewContractDiscovery(logger, 0, nil)

	// Use a known time: 2026-03-28 23:47:00 UTC.
	now := time.Date(2026, 3, 28, 23, 47, 0, 0, time.UTC)
	timestamps := cd.generateTimestamps(now, 5*time.Minute, 3)

	if len(timestamps) != 4 { // -1, 0, 1, 2
		t.Fatalf("expected 4 timestamps, got %d", len(timestamps))
	}

	// The base should be rounded down to the 5-min boundary.
	// 23:47 -> base = 23:45 (1774741500 for that date).
	// Check that timestamps are 5 minutes apart.
	for i := 1; i < len(timestamps); i++ {
		diff := timestamps[i] - timestamps[i-1]
		if diff != 300 {
			t.Fatalf("expected 300s gap, got %d between index %d and %d", diff, i-1, i)
		}
	}
}

func TestGenerateTimestamps_15m(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cd := NewContractDiscovery(logger, 0, nil)

	now := time.Date(2026, 3, 28, 23, 47, 0, 0, time.UTC)
	timestamps := cd.generateTimestamps(now, 15*time.Minute, 2)

	if len(timestamps) != 3 { // -1, 0, 1
		t.Fatalf("expected 3 timestamps, got %d", len(timestamps))
	}

	for i := 1; i < len(timestamps); i++ {
		diff := timestamps[i] - timestamps[i-1]
		if diff != 900 {
			t.Fatalf("expected 900s gap, got %d between index %d and %d", diff, i-1, i)
		}
	}
}

func TestGenerateTimestamps_AlignsToBoundary(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cd := NewContractDiscovery(logger, 0, nil)

	// 23:48:30 should round down to 23:45:00 for 5-minute intervals.
	now := time.Date(2026, 3, 28, 23, 48, 30, 0, time.UTC)
	timestamps := cd.generateTimestamps(now, 5*time.Minute, 1)

	// Base = 23:45 timestamp.
	base := time.Date(2026, 3, 28, 23, 45, 0, 0, time.UTC).Unix()
	// First timestamp should be base - 300 (previous window).
	if timestamps[0] != base-300 {
		t.Fatalf("expected first timestamp %d, got %d", base-300, timestamps[0])
	}
	// Second should be base itself.
	if timestamps[1] != base {
		t.Fatalf("expected base timestamp %d, got %d", base, timestamps[1])
	}
}

// newTestDiscoveryWithHandler creates a ContractDiscovery pointed at a test server.
func newTestDiscoveryWithHandler(handler http.Handler, minVolume float64) *ContractDiscovery {
	server := httptest.NewServer(handler)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cd := NewContractDiscovery(logger, minVolume, nil)
	cd.baseURL = server.URL
	return cd
}

// makeGammaResponse builds a JSON response for the Gamma events API.
func makeGammaResponse(m gammaMarket) []byte {
	events := []gammaEvent{{
		Slug:    "test-slug",
		Title:   "Test Event",
		Active:  true,
		Closed:  false,
		Markets: []gammaMarket{m},
	}}
	b, _ := json.Marshal(events)
	return b
}

func TestFetchEvent_SkipsClosedMarket(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gammaMarket{
			Question:        "Will BTC go up?",
			ConditionID:     "cond-1",
			ClobTokenIDs:    `["tok1","tok2"]`,
			Outcomes:        `["Up","Down"]`,
			Active:          true,
			Closed:          true,
			EndDate:         time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			AcceptingOrders: true,
			Volume:          100000,
		}
		w.Write(makeGammaResponse(m))
	})
	cd := newTestDiscoveryWithHandler(handler, 0)

	result, err := cd.fetchEvent(context.Background(), "test-slug", "BTC", "5m")
	if err == nil {
		t.Fatal("expected error for closed market")
	}
	if result != nil {
		t.Fatal("expected nil result for closed market")
	}
}

func TestFetchEvent_SkipsLowVolume(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gammaMarket{
			Question:        "Will BTC go up?",
			ConditionID:     "cond-1",
			ClobTokenIDs:    `["tok1","tok2"]`,
			Outcomes:        `["Up","Down"]`,
			Active:          true,
			Closed:          false,
			EndDate:         time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			AcceptingOrders: true,
			Volume:          500, // below threshold
		}
		w.Write(makeGammaResponse(m))
	})
	cd := newTestDiscoveryWithHandler(handler, 50000) // MinVolume = 50000

	result, err := cd.fetchEvent(context.Background(), "test-slug", "BTC", "5m")
	if err == nil {
		t.Fatal("expected error for low volume market")
	}
	if result != nil {
		t.Fatal("expected nil result for low volume market")
	}
}

func TestFetchEvent_SkipsExpiredMarket(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gammaMarket{
			Question:        "Will BTC go up?",
			ConditionID:     "cond-1",
			ClobTokenIDs:    `["tok1","tok2"]`,
			Outcomes:        `["Up","Down"]`,
			Active:          true,
			Closed:          false,
			EndDate:         time.Now().Add(-1 * time.Hour).Format(time.RFC3339), // in the past
			AcceptingOrders: true,
			Volume:          100000,
		}
		w.Write(makeGammaResponse(m))
	})
	cd := newTestDiscoveryWithHandler(handler, 0)

	result, err := cd.fetchEvent(context.Background(), "test-slug", "BTC", "5m")
	if err == nil {
		t.Fatal("expected error for expired market")
	}
	if result != nil {
		t.Fatal("expected nil result for expired market")
	}
}

func TestFetchEvent_SkipsNotAcceptingOrders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gammaMarket{
			Question:        "Will BTC go up?",
			ConditionID:     "cond-1",
			ClobTokenIDs:    `["tok1","tok2"]`,
			Outcomes:        `["Up","Down"]`,
			Active:          true,
			Closed:          false,
			EndDate:         time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			AcceptingOrders: false,
			Volume:          100000,
		}
		w.Write(makeGammaResponse(m))
	})
	cd := newTestDiscoveryWithHandler(handler, 0)

	result, err := cd.fetchEvent(context.Background(), "test-slug", "BTC", "5m")
	if err == nil {
		t.Fatal("expected error for market not accepting orders")
	}
	if result != nil {
		t.Fatal("expected nil result for market not accepting orders")
	}
}

func TestFetchEvent_ParsesTokenIDs(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gammaMarket{
			Question:        "Will ETH go up in 5m?",
			ConditionID:     "cond-eth-1",
			ClobTokenIDs:    `["first-token-up","second-token-down"]`,
			Outcomes:        `["Up","Down"]`,
			Active:          true,
			Closed:          false,
			EndDate:         time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			AcceptingOrders: true,
			Volume:          100000,
		}
		w.Write(makeGammaResponse(m))
	})
	cd := newTestDiscoveryWithHandler(handler, 0)

	result, err := cd.fetchEvent(context.Background(), "test-slug", "ETH", "5m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Per discovery.go: tokenIDs[0] is "Up", tokenIDs[1] is "Down".
	if result.TokenIDUp != "first-token-up" {
		t.Errorf("expected TokenIDUp=first-token-up, got %s", result.TokenIDUp)
	}
	if result.TokenIDDown != "second-token-down" {
		t.Errorf("expected TokenIDDown=second-token-down, got %s", result.TokenIDDown)
	}
	if result.Asset != "ETH" {
		t.Errorf("expected Asset=ETH, got %s", result.Asset)
	}
	if result.Timeframe != "5m" {
		t.Errorf("expected Timeframe=5m, got %s", result.Timeframe)
	}
	if result.ConditionID != "cond-eth-1" {
		t.Errorf("expected ConditionID=cond-eth-1, got %s", result.ConditionID)
	}
}

func TestDiscoverActive_SkipsClosedMarket(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gammaMarket{
			Question:        "Will BTC go up?",
			ConditionID:     "cond-closed",
			ClobTokenIDs:    `["tok1","tok2"]`,
			Outcomes:        `["Up","Down"]`,
			Active:          true,
			Closed:          true,
			EndDate:         time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			AcceptingOrders: true,
			Volume:          100000,
		}
		w.Write(makeGammaResponse(m))
	})
	cd := newTestDiscoveryWithHandler(handler, 0)

	contracts, err := cd.DiscoverActive(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// fetchEvent returns error for closed markets, so DiscoverActive skips them.
	for _, c := range contracts {
		if c.Closed {
			t.Errorf("expected no closed contracts, got %+v", c)
		}
	}
}

func TestDiscoverActive_SkipsLowVolume(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gammaMarket{
			Question:        "Will BTC go up?",
			ConditionID:     "cond-lowvol",
			ClobTokenIDs:    `["tok1","tok2"]`,
			Outcomes:        `["Up","Down"]`,
			Active:          true,
			Closed:          false,
			EndDate:         time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			AcceptingOrders: true,
			Volume:          100,
		}
		w.Write(makeGammaResponse(m))
	})
	cd := newTestDiscoveryWithHandler(handler, 50000)

	contracts, err := cd.DiscoverActive(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contracts) != 0 {
		t.Errorf("expected 0 contracts for low volume, got %d", len(contracts))
	}
}

func TestDiscoverActive_SkipsExpiredMarket(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gammaMarket{
			Question:        "Will BTC go up?",
			ConditionID:     "cond-expired",
			ClobTokenIDs:    `["tok1","tok2"]`,
			Outcomes:        `["Up","Down"]`,
			Active:          true,
			Closed:          false,
			EndDate:         time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
			AcceptingOrders: true,
			Volume:          100000,
		}
		w.Write(makeGammaResponse(m))
	})
	cd := newTestDiscoveryWithHandler(handler, 0)

	contracts, err := cd.DiscoverActive(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contracts) != 0 {
		t.Errorf("expected 0 contracts for expired market, got %d", len(contracts))
	}
}

func TestDiscoverActive_SkipsNotAcceptingOrders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gammaMarket{
			Question:        "Will BTC go up?",
			ConditionID:     "cond-noorders",
			ClobTokenIDs:    `["tok1","tok2"]`,
			Outcomes:        `["Up","Down"]`,
			Active:          true,
			Closed:          false,
			EndDate:         time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			AcceptingOrders: false,
			Volume:          100000,
		}
		w.Write(makeGammaResponse(m))
	})
	cd := newTestDiscoveryWithHandler(handler, 0)

	contracts, err := cd.DiscoverActive(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contracts) != 0 {
		t.Errorf("expected 0 contracts for not accepting orders, got %d", len(contracts))
	}
}

// Silence the "imported and not used" error for fmt in case it's needed.
var _ = fmt.Sprintf

func TestAssetUpper(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"btc", "BTC"},
		{"eth", "ETH"},
		{"sol", "sol"},
	}
	for _, tc := range cases {
		got := assetUpper(tc.input)
		if got != tc.expected {
			t.Errorf("assetUpper(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
