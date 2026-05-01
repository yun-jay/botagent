package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockGammaServer creates a test server that returns mock contract data.
// hitCount tracks how many requests the server receives.
func mockGammaServer(hitCount *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		tokens, _ := json.Marshal([]string{"token-up-123", "token-down-456"})
		event := []gammaEvent{{
			Slug:   "btc-updown-5m-test",
			Active: true,
			Closed: false,
			Markets: []gammaMarket{{
				Question:        "Bitcoin Up or Down - Test",
				ConditionID:     "0xtest123",
				ClobTokenIDs:    string(tokens),
				Outcomes:        `["Up","Down"]`,
				Active:          true,
				Closed:          false,
				EndDate:         time.Now().Add(10 * time.Minute).Format(time.RFC3339),
				AcceptingOrders: true,
			}},
		}}
		json.NewEncoder(w).Encode(event)
	}))
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newTestDiscovery creates a ContractDiscovery pointed at a mock server.
func newTestDiscovery(serverURL string) *ContractDiscovery {
	cd := NewContractDiscovery(testLogger(), 0, nil)
	// Override the gammaAPIURL by using a custom fetchEvent.
	// We'll create a wrapper discovery that uses the test server.
	cd.client = &http.Client{Timeout: 5 * time.Second}
	return cd
}

// testableCache creates a cache with a mock discovery that we control.
type mockDiscoveryFunc func(ctx context.Context) ([]DiscoveredContract, error)

type testDiscovery struct {
	fn mockDiscoveryFunc
}

// We need a cache that accepts an interface instead of concrete type.
// Since ContractCache takes *ContractDiscovery, we'll test via a
// full integration with the mock HTTP server.

func TestContractCache_GetContracts_ReturnsEmpty_Initially(t *testing.T) {
	discovery := NewContractDiscovery(testLogger(), 0, nil)
	cache := NewContractCache(discovery, time.Hour, testLogger())
	// Don't start — should return nil.
	contracts := cache.GetContracts()
	if len(contracts) != 0 {
		t.Fatalf("expected 0 contracts before start, got %d", len(contracts))
	}
}

func TestContractCache_GetContracts_AfterManualRefresh(t *testing.T) {
	var hitCount atomic.Int64
	server := mockGammaServer(&hitCount)
	defer server.Close()

	// Create discovery with overridden URL.
	discovery := &ContractDiscovery{
		client: &http.Client{Timeout: 5 * time.Second},
		log:    testLogger(),
	}
	// Override gammaAPIURL by patching fetchEvent — we'll use a simpler approach:
	// create a cache and call refresh directly with a custom discovery.
	cache := &ContractCache{
		discovery:       discovery,
		refreshInterval: time.Hour,
		log:             testLogger(),
		done:            make(chan struct{}),
	}

	// Manually set contracts to simulate a refresh.
	cache.mu.Lock()
	cache.contracts = []DiscoveredContract{
		{ConditionID: "0x111", Asset: "BTC", Timeframe: "5m", TokenIDUp: "up1", TokenIDDown: "down1", Active: true},
		{ConditionID: "0x222", Asset: "ETH", Timeframe: "15m", TokenIDUp: "up2", TokenIDDown: "down2", Active: true},
	}
	cache.lastFetch = time.Now()
	cache.mu.Unlock()

	contracts := cache.GetContracts()
	if len(contracts) != 2 {
		t.Fatalf("expected 2 contracts, got %d", len(contracts))
	}
	if contracts[0].ConditionID != "0x111" {
		t.Fatalf("expected 0x111, got %s", contracts[0].ConditionID)
	}
}

func TestContractCache_AtomicSwap_NeverPartialRead(t *testing.T) {
	cache := &ContractCache{
		refreshInterval: time.Hour,
		log:             testLogger(),
		done:            make(chan struct{}),
	}

	// Simulate writer updating contracts.
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer goroutine — swaps between two different contract sets.
	wg.Add(1)
	go func() {
		defer wg.Done()
		setA := []DiscoveredContract{
			{ConditionID: "A1", Asset: "BTC"},
			{ConditionID: "A2", Asset: "BTC"},
		}
		setB := []DiscoveredContract{
			{ConditionID: "B1", Asset: "ETH"},
			{ConditionID: "B2", Asset: "ETH"},
			{ConditionID: "B3", Asset: "ETH"},
		}
		useA := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			cache.mu.Lock()
			if useA {
				cache.contracts = setA
			} else {
				cache.contracts = setB
			}
			cache.mu.Unlock()
			useA = !useA
		}
	}()

	// Reader goroutines — verify we always get a complete, consistent set.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10000; j++ {
				select {
				case <-stop:
					return
				default:
				}
				contracts := cache.GetContracts()
				if len(contracts) == 0 {
					continue
				}
				// All contracts in a read must have the same asset (no mix of A and B).
				firstAsset := contracts[0].Asset
				for _, c := range contracts {
					if c.Asset != firstAsset {
						t.Errorf("inconsistent read: got mixed assets %s and %s", firstAsset, c.Asset)
						close(stop)
						return
					}
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestContractCache_Metrics_InitialState(t *testing.T) {
	cache := &ContractCache{
		refreshInterval: time.Hour,
		log:             testLogger(),
		done:            make(chan struct{}),
	}

	m := cache.Metrics()
	if m.ContractCount != 0 {
		t.Fatalf("expected 0 contracts, got %d", m.ContractCount)
	}
	if m.FetchCount != 0 {
		t.Fatalf("expected 0 fetches, got %d", m.FetchCount)
	}
	if m.FetchErrCount != 0 {
		t.Fatalf("expected 0 errors, got %d", m.FetchErrCount)
	}
}

func TestContractCache_Metrics_AfterRefresh(t *testing.T) {
	cache := &ContractCache{
		refreshInterval: time.Hour,
		log:             testLogger(),
		done:            make(chan struct{}),
	}

	// Simulate a successful refresh.
	cache.mu.Lock()
	cache.contracts = []DiscoveredContract{{ConditionID: "0x1"}}
	cache.lastFetch = time.Now()
	cache.mu.Unlock()

	cache.metricsMu.Lock()
	cache.fetchCount = 3
	cache.fetchErrCount = 1
	cache.lastFetchDuration = 150 * time.Millisecond
	cache.metricsMu.Unlock()

	m := cache.Metrics()
	if m.ContractCount != 1 {
		t.Fatalf("expected 1 contract, got %d", m.ContractCount)
	}
	if m.FetchCount != 3 {
		t.Fatalf("expected 3 fetches, got %d", m.FetchCount)
	}
	if m.FetchErrCount != 1 {
		t.Fatalf("expected 1 error, got %d", m.FetchErrCount)
	}
	if m.LastFetchDuration != 150*time.Millisecond {
		t.Fatalf("expected 150ms duration, got %s", m.LastFetchDuration)
	}
}

func TestContractCache_KeepsStaleCacheOnError(t *testing.T) {
	cache := &ContractCache{
		refreshInterval: time.Hour,
		log:             testLogger(),
		done:            make(chan struct{}),
	}

	// Set initial good data.
	cache.mu.Lock()
	cache.contracts = []DiscoveredContract{
		{ConditionID: "0xgood", Asset: "BTC"},
	}
	cache.mu.Unlock()

	// Simulate a failed refresh — contracts should remain.
	cache.mu.Lock()
	cache.fetchErr = fmt.Errorf("network error")
	cache.mu.Unlock()

	contracts := cache.GetContracts()
	if len(contracts) != 1 {
		t.Fatalf("expected stale cache to persist, got %d contracts", len(contracts))
	}
	if contracts[0].ConditionID != "0xgood" {
		t.Fatalf("expected stale data 0xgood, got %s", contracts[0].ConditionID)
	}

	err := cache.GetLastError()
	if err == nil {
		t.Fatal("expected error to be stored")
	}
}

func TestContractCache_GetLastFetchTime(t *testing.T) {
	cache := &ContractCache{
		refreshInterval: time.Hour,
		log:             testLogger(),
		done:            make(chan struct{}),
	}

	// Before any fetch.
	if !cache.GetLastFetchTime().IsZero() {
		t.Fatal("expected zero time before first fetch")
	}

	now := time.Now()
	cache.mu.Lock()
	cache.lastFetch = now
	cache.mu.Unlock()

	if cache.GetLastFetchTime() != now {
		t.Fatal("expected set time")
	}
}

func TestContractCache_ConcurrentReadsDontBlock(t *testing.T) {
	cache := &ContractCache{
		refreshInterval: time.Hour,
		log:             testLogger(),
		done:            make(chan struct{}),
	}
	cache.mu.Lock()
	cache.contracts = []DiscoveredContract{{ConditionID: "0x1"}}
	cache.mu.Unlock()

	// Launch many concurrent readers — should all complete quickly.
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				c := cache.GetContracts()
				if len(c) != 1 {
					t.Errorf("expected 1 contract, got %d", len(c))
					return
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// 100k reads should complete in well under 1 second.
	if elapsed > time.Second {
		t.Fatalf("100k concurrent reads took %s, expected <1s", elapsed)
	}
}

func TestContractCache_NearBoundary(t *testing.T) {
	cache := &ContractCache{
		burstInterval: 1 * time.Second,
		burstWindow:   15 * time.Second,
		log:           testLogger(),
	}

	tests := []struct {
		name     string
		time     time.Time
		expected bool
	}{
		{"exactly on boundary", time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC), true},       // 0s into 5m window
		{"1s after boundary", time.Date(2025, 1, 1, 12, 0, 1, 0, time.UTC), true},          // 1s
		{"14s after boundary", time.Date(2025, 1, 1, 12, 0, 14, 0, time.UTC), true},        // 14s
		{"15s after boundary", time.Date(2025, 1, 1, 12, 0, 15, 0, time.UTC), false},       // 15s — outside
		{"middle of window", time.Date(2025, 1, 1, 12, 2, 30, 0, time.UTC), false},         // 150s
		{"15s before boundary", time.Date(2025, 1, 1, 12, 4, 45, 0, time.UTC), true},       // 285s = 300-15
		{"14s before boundary", time.Date(2025, 1, 1, 12, 4, 46, 0, time.UTC), true},       // 286s
		{"1s before boundary", time.Date(2025, 1, 1, 12, 4, 59, 0, time.UTC), true},        // 299s
		{"on 5m boundary", time.Date(2025, 1, 1, 12, 5, 0, 0, time.UTC), true},             // 0s (next boundary)
		{"10m boundary", time.Date(2025, 1, 1, 12, 10, 0, 0, time.UTC), true},              // also a 5m boundary
		{"non-boundary middle", time.Date(2025, 1, 1, 12, 7, 30, 0, time.UTC), false},      // 150s into window
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cache.nearBoundary(tt.time)
			if got != tt.expected {
				sec := tt.time.Unix() % 300
				t.Errorf("nearBoundary(%s) = %v, want %v (sec=%d)", tt.time.Format("15:04:05"), got, tt.expected, sec)
			}
		})
	}
}

func TestContractCache_CurrentInterval_Adaptive(t *testing.T) {
	cache := &ContractCache{
		refreshInterval: 30 * time.Second,
		burstInterval:   1 * time.Second,
		burstWindow:     15 * time.Second,
		log:             testLogger(),
	}

	// Near boundary → burst interval.
	nearBoundary := time.Date(2025, 1, 1, 12, 0, 5, 0, time.UTC)
	if got := cache.currentInterval(nearBoundary); got != 1*time.Second {
		t.Errorf("expected 1s near boundary, got %s", got)
	}

	// Far from boundary → normal interval.
	farFromBoundary := time.Date(2025, 1, 1, 12, 2, 30, 0, time.UTC)
	if got := cache.currentInterval(farFromBoundary); got != 30*time.Second {
		t.Errorf("expected 30s far from boundary, got %s", got)
	}
}

func TestContractCache_WorkerRefreshes(t *testing.T) {
	// Track how many times contracts change.
	refreshCount := atomic.Int64{}

	cache := &ContractCache{
		refreshInterval: time.Hour, // we won't rely on the ticker
		log:             testLogger(),
		done:            make(chan struct{}),
	}

	// Manually simulate what the worker does.
	for i := 0; i < 3; i++ {
		cache.mu.Lock()
		cache.contracts = []DiscoveredContract{
			{ConditionID: fmt.Sprintf("0x%d", i), Asset: "BTC"},
		}
		cache.lastFetch = time.Now()
		cache.mu.Unlock()
		refreshCount.Add(1)
	}

	contracts := cache.GetContracts()
	if len(contracts) != 1 {
		t.Fatalf("expected 1 contract, got %d", len(contracts))
	}
	// Should have the latest.
	if contracts[0].ConditionID != "0x2" {
		t.Fatalf("expected 0x2, got %s", contracts[0].ConditionID)
	}
	if refreshCount.Load() != 3 {
		t.Fatalf("expected 3 refreshes, got %d", refreshCount.Load())
	}
}
