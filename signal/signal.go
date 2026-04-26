package signal

import (
	"context"
	"time"
)

// Direction indicates whether to buy or sell.
type Direction int

const (
	Buy Direction = iota
	Sell
)

// String returns the direction as a string.
func (d Direction) String() string {
	switch d {
	case Buy:
		return "BUY"
	case Sell:
		return "SELL"
	default:
		return "UNKNOWN"
	}
}

// Signal represents "something happened that might be tradeable."
// Works for prediction markets, stocks, options — any tradeable instrument.
type Signal struct {
	// Identity
	ID        string    // unique per signal (UUID or deterministic hash)
	Timestamp time.Time // when generated
	Source    string    // e.g., "binance_arb", "crowd_consensus", "noaa_forecast"

	// Instrument reference (market-agnostic)
	// For Polymarket: MarketID=conditionID, Instrument=tokenID
	// For stocks: MarketID="NASDAQ", Instrument="AAPL"
	MarketID   string
	Instrument string

	// Core trading fields
	Direction  Direction
	TrueProb   float64 // bot's estimated probability / fair value [0,1]
	MarketProb float64 // current market price / implied probability [0,1]
	Edge       float64 // how the bot defines edge (typically trueProb - marketProb)
	Confidence float64 // signal quality [0,1], default 1.0

	// Domain-specific payload — framework never inspects this
	Metadata map[string]any
}

// Generator produces signals. Each bot implements this.
type Generator interface {
	Generate(ctx context.Context) ([]Signal, error)
}
