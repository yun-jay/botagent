package sizing

import "math"

// Sizer computes position size from signal parameters.
type Sizer interface {
	Size(params SizeParams) float64
}

// SizeParams holds the inputs for position sizing.
type SizeParams struct {
	TrueProb       float64 // estimated true probability / fair value [0,1]
	MarketProb     float64 // current market price / implied probability [0,1]
	PortfolioValue float64 // current bankroll in base currency
	Confidence     float64 // signal confidence [0,1], default 1.0
}

// KellySizer implements fractional Kelly criterion sizing.
type KellySizer struct {
	Fraction       float64 // Kelly fraction (e.g., 0.25 for quarter-Kelly)
	MaxPositionPct float64 // max position as fraction of portfolio (e.g., 0.07)
	MinBetSize     float64 // minimum bet size in base currency (e.g., $1)
}

// NewKelly creates a new KellySizer.
func NewKelly(fraction, maxPositionPct, minBetSize float64) *KellySizer {
	return &KellySizer{
		Fraction:       fraction,
		MaxPositionPct: maxPositionPct,
		MinBetSize:     minBetSize,
	}
}

// Size computes the recommended position size using the Kelly criterion.
//
// Kelly formula: f* = (b*p - q) / b
// where b = odds - 1, p = trueProb, q = 1 - p, odds = 1/marketProb
//
// This is mathematically equivalent to: f* = (p*odds - 1) / (odds - 1)
// Both the edge+odds form (claude-bot) and trueProb+marketProb form (soccerzock)
// reduce to this same computation.
func (ks *KellySizer) Size(p SizeParams) float64 {
	if p.MarketProb <= 0 || p.MarketProb >= 1 || p.TrueProb <= 0 || p.PortfolioValue <= 0 {
		return 0
	}

	// Edge must be positive
	if p.TrueProb <= p.MarketProb {
		return 0
	}

	odds := 1.0 / p.MarketProb
	b := odds - 1
	if b <= 0 {
		return 0
	}

	trueProb := p.TrueProb
	if trueProb >= 1.0 {
		trueProb = 0.99
	}
	q := 1.0 - trueProb

	kellyFraction := (b*trueProb - q) / b
	if kellyFraction <= 0 {
		return 0
	}

	// Apply fractional Kelly
	adjusted := kellyFraction * ks.Fraction

	// Apply confidence scaling
	confidence := p.Confidence
	if confidence <= 0 {
		confidence = 1.0
	}
	adjusted *= confidence

	// Cap at maximum position
	if ks.MaxPositionPct > 0 && adjusted > ks.MaxPositionPct {
		adjusted = ks.MaxPositionPct
	}

	size := adjusted * p.PortfolioValue

	// Round to cents
	size = math.Round(size*100) / 100

	// Apply minimum
	if size < ks.MinBetSize {
		return 0
	}

	return size
}
