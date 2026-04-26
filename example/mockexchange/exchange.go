package mockexchange

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/yun-jay/botagent/order"
)

// Market represents a simulated market for an instrument.
type Market struct {
	Instrument string
	TruePrice  float64 // "real" fair value (for simulating fills)
	BidPrice   float64
	AskPrice   float64
}

// FilledOrder records details of a filled order.
type FilledOrder struct {
	OrderID    string
	Instrument string
	Side       string
	Price      float64
	Size       float64
}

// Exchange simulates a trading venue with an order book.
// Implements order.Executor.
type Exchange struct {
	mu       sync.Mutex
	markets  map[string]*Market
	orders   []FilledOrder
	nextID   int
	logger   *slog.Logger
	failNext bool // for testing: next execution will fail
}

// New creates a new mock exchange.
func New(logger *slog.Logger) *Exchange {
	return &Exchange{
		markets: make(map[string]*Market),
		logger:  logger,
	}
}

// AddMarket adds a simulated market.
func (e *Exchange) AddMarket(instrument string, truePrice, bid, ask float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.markets[instrument] = &Market{
		Instrument: instrument,
		TruePrice:  truePrice,
		BidPrice:   bid,
		AskPrice:   ask,
	}
}

// SetFailNext causes the next Execute call to return an error.
func (e *Exchange) SetFailNext(fail bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failNext = fail
}

// Execute places an order on the mock exchange.
func (e *Exchange) Execute(_ context.Context, req order.Request) (order.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.failNext {
		e.failNext = false
		return order.Result{Status: "failed"}, fmt.Errorf("simulated execution failure")
	}

	market, ok := e.markets[req.Instrument]
	if !ok {
		return order.Result{Status: "failed"}, fmt.Errorf("unknown instrument: %s", req.Instrument)
	}

	// Determine fill price based on side.
	// The mock always fills at the current bid/ask — it simulates a market that
	// accepts any order (like a prediction market or dark pool).
	var fillPrice float64
	switch req.Side {
	case "BUY":
		fillPrice = market.AskPrice
	case "SELL":
		fillPrice = market.BidPrice
	default:
		return order.Result{Status: "failed"}, fmt.Errorf("unknown side: %s", req.Side)
	}

	e.nextID++
	orderID := fmt.Sprintf("mock-ord-%d", e.nextID)

	filled := FilledOrder{
		OrderID:    orderID,
		Instrument: req.Instrument,
		Side:       req.Side,
		Price:      fillPrice,
		Size:       req.Size,
	}
	e.orders = append(e.orders, filled)

	e.logger.Info("mock order filled",
		"order_id", orderID,
		"instrument", req.Instrument,
		"side", req.Side,
		"price", fillPrice,
		"size", req.Size,
	)

	return order.Result{
		OrderID:   orderID,
		Status:    "filled",
		FillPrice: fillPrice,
	}, nil
}

// CancelAll is a no-op for the mock exchange.
func (e *Exchange) CancelAll(_ context.Context) error {
	e.logger.Info("mock cancel all orders")
	return nil
}

// FilledOrders returns a copy of all filled orders.
func (e *Exchange) FilledOrders() []FilledOrder {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]FilledOrder, len(e.orders))
	copy(result, e.orders)
	return result
}
