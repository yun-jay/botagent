package weatherbot

import (
	"context"

	"github.com/yun-jay/botagent/signal"
	"github.com/yun-jay/botagent/sizing"
	"github.com/yun-jay/botagent/strategy"
)

// WeatherEvaluator applies weather-specific filters and sizing.
type WeatherEvaluator struct {
	Sizer          sizing.Sizer
	MinEdge        float64 // minimum edge to trade (e.g., 0.03)
	MinConfidence  float64 // minimum confidence (e.g., 0.5)
	PortfolioValue float64
}

// Evaluate decides whether a weather signal should be traded.
func (e *WeatherEvaluator) Evaluate(_ context.Context, sig signal.Signal) (strategy.Decision, error) {
	// Filter: minimum edge
	if sig.Edge < e.MinEdge {
		return strategy.Decision{
			Signal:        sig,
			ReasonSkipped: "edge below minimum",
		}, nil
	}

	// Filter: minimum confidence
	if sig.Confidence < e.MinConfidence {
		return strategy.Decision{
			Signal:        sig,
			ReasonSkipped: "confidence below minimum",
		}, nil
	}

	// Size using the provided sizer
	size := e.Sizer.Size(sizing.SizeParams{
		TrueProb:       sig.TrueProb,
		MarketProb:     sig.MarketProb,
		PortfolioValue: e.PortfolioValue,
		Confidence:     sig.Confidence,
	})

	if size <= 0 {
		return strategy.Decision{
			Signal:        sig,
			ReasonSkipped: "sizing returned zero",
		}, nil
	}

	return strategy.Decision{
		Approved:     true,
		Signal:       sig,
		PositionSize: size,
	}, nil
}
