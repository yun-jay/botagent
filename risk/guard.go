package risk

import "sync"

// PositionGuard prevents double-entry on the same instrument.
// Thread-safe for concurrent use by multiple goroutines.
type PositionGuard struct {
	mu        sync.RWMutex
	positions map[string]bool
}

// NewPositionGuard creates a new PositionGuard.
func NewPositionGuard() *PositionGuard {
	return &PositionGuard{
		positions: make(map[string]bool),
	}
}

// Acquire attempts to acquire a position on the given instrument.
// Returns true if acquired (no existing position), false if already active.
func (pg *PositionGuard) Acquire(instrumentID string) bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	if pg.positions[instrumentID] {
		return false
	}
	pg.positions[instrumentID] = true
	return true
}

// Release releases a position on the given instrument.
func (pg *PositionGuard) Release(instrumentID string) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	delete(pg.positions, instrumentID)
}

// IsActive returns true if there is an active position on the instrument.
func (pg *PositionGuard) IsActive(instrumentID string) bool {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	return pg.positions[instrumentID]
}

// ActiveCount returns the number of active positions.
func (pg *PositionGuard) ActiveCount() int {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	return len(pg.positions)
}
