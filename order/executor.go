package order

import (
	"context"

	"github.com/yun-jay/botagent/signal"
)

// Request is what the pipeline sends to the market.
type Request struct {
	Signal     signal.Signal
	Side       string  // "BUY" or "SELL"
	Instrument string  // what to trade (tokenID, ticker symbol, etc.)
	Price      float64 // limit price (0 = market order, if provider supports it)
	Size       float64 // position size in base currency
	OrderType  string  // provider-specific: "FOK", "GTC", "LIMIT", "MARKET"
}

// Result is what comes back from the market.
type Result struct {
	OrderID   string
	Status    string // "filled", "open", "failed", "cancelled"
	FillPrice float64
	Error     error
}

// Executor places orders on a market. Each provider implements this.
type Executor interface {
	Execute(ctx context.Context, req Request) (Result, error)
	CancelAll(ctx context.Context) error
}
