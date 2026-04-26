package trade_test

import (
	"context"
	"sync"
	"testing"

	"github.com/yunus/botagent/trade"
)

func TestInMemoryRecorder_InsertAndTrades(t *testing.T) {
	rec := trade.NewInMemoryRecorder()
	ctx := context.Background()

	id1, err := rec.Insert(ctx, &trade.Record{
		SignalID:   "sig-1",
		Instrument: "TOKEN-A",
		Side:       "BUY",
		Price:      0.55,
		Size:       100,
		Edge:       0.10,
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("Insert error: %v", err)
	}
	if id1 != 1 {
		t.Errorf("id = %d, want 1", id1)
	}

	id2, _ := rec.Insert(ctx, &trade.Record{
		SignalID:   "sig-2",
		Instrument: "TOKEN-B",
		Side:       "SELL",
		Status:     "open",
	})
	if id2 != 2 {
		t.Errorf("id = %d, want 2", id2)
	}

	trades := rec.Trades()
	if len(trades) != 2 {
		t.Fatalf("len = %d, want 2", len(trades))
	}
	if trades[0].Instrument != "TOKEN-A" {
		t.Errorf("trades[0].Instrument = %q, want TOKEN-A", trades[0].Instrument)
	}
	if trades[0].CreatedAt.IsZero() {
		t.Error("CreatedAt should be auto-set")
	}
}

func TestInMemoryRecorder_UpdateStatus(t *testing.T) {
	rec := trade.NewInMemoryRecorder()
	ctx := context.Background()

	id, _ := rec.Insert(ctx, &trade.Record{Status: "open"})
	err := rec.UpdateStatus(ctx, id, "filled", "order-123")
	if err != nil {
		t.Fatalf("UpdateStatus error: %v", err)
	}

	trades := rec.Trades()
	if trades[0].Status != "filled" {
		t.Errorf("status = %q, want filled", trades[0].Status)
	}
	if trades[0].OrderID != "order-123" {
		t.Errorf("orderID = %q, want order-123", trades[0].OrderID)
	}
}

func TestInMemoryRecorder_UpdateStatus_NonExistent(t *testing.T) {
	rec := trade.NewInMemoryRecorder()
	err := rec.UpdateStatus(context.Background(), 999, "failed", "")
	if err != nil {
		t.Errorf("updating non-existent trade should not error, got: %v", err)
	}
}

func TestInMemoryRecorder_Trades_ReturnsCopy(t *testing.T) {
	rec := trade.NewInMemoryRecorder()
	rec.Insert(context.Background(), &trade.Record{Status: "open"})

	trades := rec.Trades()
	trades[0].Status = "MUTATED"

	original := rec.Trades()
	if original[0].Status == "MUTATED" {
		t.Error("Trades() should return a copy, not a reference")
	}
}

func TestInMemoryRecorder_ConcurrentAccess(t *testing.T) {
	rec := trade.NewInMemoryRecorder()
	ctx := context.Background()
	const n = 100
	var wg sync.WaitGroup

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec.Insert(ctx, &trade.Record{Status: "open"})
		}()
	}
	wg.Wait()

	trades := rec.Trades()
	if len(trades) != n {
		t.Errorf("len = %d, want %d", len(trades), n)
	}
}
