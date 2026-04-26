package risk_test

import (
	"sync"
	"testing"

	"github.com/yun-jay/botagent/risk"
)

func TestPositionGuard_AcquireRelease(t *testing.T) {
	pg := risk.NewPositionGuard()

	if !pg.Acquire("AAPL") {
		t.Error("first acquire should succeed")
	}
	if pg.Acquire("AAPL") {
		t.Error("second acquire should fail (already active)")
	}
	if !pg.IsActive("AAPL") {
		t.Error("AAPL should be active")
	}
	if pg.ActiveCount() != 1 {
		t.Errorf("count = %d, want 1", pg.ActiveCount())
	}

	pg.Release("AAPL")
	if pg.IsActive("AAPL") {
		t.Error("AAPL should not be active after release")
	}
	if pg.ActiveCount() != 0 {
		t.Errorf("count = %d, want 0", pg.ActiveCount())
	}

	// Can acquire again after release
	if !pg.Acquire("AAPL") {
		t.Error("acquire after release should succeed")
	}
}

func TestPositionGuard_MultipleInstruments(t *testing.T) {
	pg := risk.NewPositionGuard()

	pg.Acquire("AAPL")
	pg.Acquire("GOOG")
	pg.Acquire("TSLA")

	if pg.ActiveCount() != 3 {
		t.Errorf("count = %d, want 3", pg.ActiveCount())
	}

	if pg.IsActive("MSFT") {
		t.Error("MSFT should not be active")
	}

	pg.Release("GOOG")
	if pg.ActiveCount() != 2 {
		t.Errorf("count = %d, want 2", pg.ActiveCount())
	}
}

func TestPositionGuard_ReleaseNonExistent(t *testing.T) {
	pg := risk.NewPositionGuard()
	pg.Release("NONEXISTENT") // should not panic
	if pg.ActiveCount() != 0 {
		t.Errorf("count = %d, want 0", pg.ActiveCount())
	}
}

func TestPositionGuard_ConcurrentAccess(t *testing.T) {
	pg := risk.NewPositionGuard()
	const n = 100
	var wg sync.WaitGroup

	// Concurrent acquires on same instrument — only one should succeed
	successes := make(chan bool, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			successes <- pg.Acquire("RACE")
		}()
	}
	wg.Wait()
	close(successes)

	count := 0
	for s := range successes {
		if s {
			count++
		}
	}
	if count != 1 {
		t.Errorf("exactly 1 goroutine should acquire, got %d", count)
	}

	// Concurrent acquires on different instruments — all should succeed
	wg.Add(n)
	for i := 0; i < n; i++ {
		id := string(rune('A' + i))
		go func(id string) {
			defer wg.Done()
			pg.Acquire(id)
		}(id)
	}
	wg.Wait()
}
