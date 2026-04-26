package trade

import (
	"context"
	"sync"
	"time"
)

// Record is the minimal trade record the pipeline produces.
type Record struct {
	ID         int64
	SignalID   string
	MarketID   string
	Instrument string
	Side       string
	Price      float64
	Size       float64
	Edge       float64
	Status     string // "open", "filled", "closed", "failed", "dry_run"
	OrderID    string
	CreatedAt  time.Time
	Metadata   map[string]any
}

// Recorder persists trade records. Each bot implements this with its own schema.
type Recorder interface {
	Insert(ctx context.Context, rec *Record) (int64, error)
	UpdateStatus(ctx context.Context, id int64, status, orderID string) error
}

// InMemoryRecorder is a simple implementation for tests and examples.
type InMemoryRecorder struct {
	mu     sync.Mutex
	trades []Record
	nextID int64
}

// NewInMemoryRecorder creates a new InMemoryRecorder.
func NewInMemoryRecorder() *InMemoryRecorder {
	return &InMemoryRecorder{nextID: 1}
}

// Insert records a trade and returns its ID.
func (r *InMemoryRecorder) Insert(_ context.Context, rec *Record) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec.ID = r.nextID
	r.nextID++
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	r.trades = append(r.trades, *rec)
	return rec.ID, nil
}

// UpdateStatus updates the status and order ID for a trade.
func (r *InMemoryRecorder) UpdateStatus(_ context.Context, id int64, status, orderID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.trades {
		if r.trades[i].ID == id {
			r.trades[i].Status = status
			if orderID != "" {
				r.trades[i].OrderID = orderID
			}
			return nil
		}
	}
	return nil
}

// Trades returns a copy of all recorded trades.
func (r *InMemoryRecorder) Trades() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]Record, len(r.trades))
	copy(result, r.trades)
	return result
}
