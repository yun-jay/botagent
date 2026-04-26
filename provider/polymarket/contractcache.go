package polymarket

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ContractCache maintains an in-memory cache of active contracts,
// refreshed by a background worker. The scan loop reads from memory
// with zero latency via RWMutex.
//
// Adaptive polling: within a configurable window around each 5-minute
// boundary (when new contracts are created), the cache polls at BurstInterval
// (default 1s). Outside that window it uses the normal RefreshInterval.
type ContractCache struct {
	discovery *ContractDiscovery
	log       *slog.Logger

	mu        sync.RWMutex
	contracts []DiscoveredContract
	lastFetch time.Time
	fetchErr  error

	refreshInterval time.Duration
	burstInterval   time.Duration // polling interval inside the creation window
	burstWindow     time.Duration // ± duration around 5m boundary to use burst polling
	done            chan struct{}

	// Callback invoked after each successful refresh.
	onRefresh   func(contracts []DiscoveredContract)
	onRefreshMu sync.Mutex // serializes async onRefresh callbacks

	// Metrics
	metricsMu        sync.Mutex
	lastFetchDuration time.Duration
	fetchCount       int64
	fetchErrCount    int64
}

func NewContractCache(discovery *ContractDiscovery, refreshInterval time.Duration, logger *slog.Logger) *ContractCache {
	return &ContractCache{
		discovery:       discovery,
		refreshInterval: refreshInterval,
		burstInterval:   1 * time.Second,
		burstWindow:     15 * time.Second,
		log:             logger,
		done:            make(chan struct{}),
	}
}

// Start performs an initial fetch, then starts the background refresh worker.
func (cc *ContractCache) Start(ctx context.Context) error {
	// Initial fetch — block until we have contracts or fail.
	if err := cc.refresh(ctx); err != nil {
		cc.log.Warn("initial contract fetch failed, will retry in background", "error", err)
	}

	go cc.worker(ctx)
	return nil
}

// GetContracts returns the current cached contracts. This is the hot path —
// called every scan cycle. Only acquires a read lock (nanoseconds).
func (cc *ContractCache) GetContracts() []DiscoveredContract {
	cc.mu.RLock()
	contracts := cc.contracts
	cc.mu.RUnlock()
	return contracts
}

// GetLastFetchTime returns when contracts were last successfully refreshed.
func (cc *ContractCache) GetLastFetchTime() time.Time {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return cc.lastFetch
}

// GetLastError returns the last fetch error, if any.
func (cc *ContractCache) GetLastError() error {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return cc.fetchErr
}

// Metrics returns cache performance metrics.
func (cc *ContractCache) Metrics() CacheMetrics {
	cc.metricsMu.Lock()
	defer cc.metricsMu.Unlock()
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return CacheMetrics{
		ContractCount:     len(cc.contracts),
		LastFetchDuration: cc.lastFetchDuration,
		LastFetchTime:     cc.lastFetch,
		FetchCount:        cc.fetchCount,
		FetchErrCount:     cc.fetchErrCount,
		InBurst:           cc.nearBoundary(time.Now()),
	}
}

type CacheMetrics struct {
	ContractCount     int
	LastFetchDuration time.Duration
	LastFetchTime     time.Time
	FetchCount        int64
	FetchErrCount     int64
	InBurst           bool
}

// nearBoundary reports whether now is within ± burstWindow of a 5-minute boundary.
func (cc *ContractCache) nearBoundary(now time.Time) bool {
	const boundary = 5 * 60 // 5 minutes in seconds
	sec := now.Unix() % int64(boundary)
	// sec is 0..299; near boundary means sec < window OR sec > (boundary - window).
	window := int64(cc.burstWindow.Seconds())
	return sec < window || sec >= int64(boundary)-window
}

func (cc *ContractCache) worker(ctx context.Context) {
	interval := cc.currentInterval(time.Now())
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-cc.done:
			return
		case <-timer.C:
			if err := cc.refresh(ctx); err != nil {
				cc.log.Warn("contract refresh failed, keeping stale cache", "error", err)
			}
			interval = cc.currentInterval(time.Now())
			timer.Reset(interval)
		}
	}
}

// currentInterval returns burstInterval near a 5m boundary, refreshInterval otherwise.
func (cc *ContractCache) currentInterval(now time.Time) time.Duration {
	if cc.nearBoundary(now) {
		return cc.burstInterval
	}
	return cc.refreshInterval
}

func (cc *ContractCache) refresh(ctx context.Context) error {
	start := time.Now()
	contracts, err := cc.discovery.DiscoverActive(ctx)
	duration := time.Since(start)

	cc.metricsMu.Lock()
	cc.lastFetchDuration = duration
	cc.fetchCount++
	if err != nil {
		cc.fetchErrCount++
	}
	cc.metricsMu.Unlock()

	if err != nil {
		cc.mu.Lock()
		cc.fetchErr = err
		cc.mu.Unlock()
		return err
	}

	// Atomic swap — build the full list, then replace the pointer.
	cc.mu.Lock()
	cc.contracts = contracts
	cc.lastFetch = time.Now()
	cc.fetchErr = nil
	cc.mu.Unlock()

	cc.log.Info("contract cache refreshed",
		"count", len(contracts),
		"duration_ms", duration.Milliseconds(),
	)

	if cc.onRefresh != nil {
		cb := cc.onRefresh
		contracts := contracts // capture for goroutine
		go func() {
			// Serialize callbacks so two refreshes don't race inside the
			// callback, but never block the worker's timer loop.
			cc.onRefreshMu.Lock()
			defer cc.onRefreshMu.Unlock()
			cb(contracts)
		}()
	}
	return nil
}

// OnRefresh registers a callback invoked after each successful contract refresh.
// Use this to update WebSocket subscriptions when the contract set changes.
func (cc *ContractCache) OnRefresh(fn func([]DiscoveredContract)) {
	cc.onRefresh = fn
}

// Stop halts the background worker.
func (cc *ContractCache) Stop() {
	close(cc.done)
}
