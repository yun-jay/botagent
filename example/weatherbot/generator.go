package weatherbot

import (
	"context"
	"time"

	"github.com/yunus/botagent/signal"
)

// WeatherGenerator generates weather prediction signals.
// In a real bot this would fetch data from NOAA or a weather API.
type WeatherGenerator struct {
	// Predefined signals for deterministic testing
	signals []signal.Signal
}

// NewGenerator creates a generator with predefined signals.
func NewGenerator(signals []signal.Signal) *WeatherGenerator {
	return &WeatherGenerator{signals: signals}
}

// NewDefaultGenerator creates a generator with sample weather signals.
func NewDefaultGenerator() *WeatherGenerator {
	now := time.Now()
	return &WeatherGenerator{
		signals: []signal.Signal{
			{
				ID:         "weather-nyc-hot-001",
				Timestamp:  now,
				Source:     "noaa_forecast",
				MarketID:   "weather-markets",
				Instrument: "WEATHER-NYC-HOT",
				Direction:  signal.Buy,
				TrueProb:   0.70,
				MarketProb: 0.60,
				Edge:       0.10,
				Confidence: 0.85,
				Metadata:   map[string]any{"city": "NYC", "condition": "hot", "forecast_temp": 95},
			},
			{
				ID:         "weather-la-rain-001",
				Timestamp:  now,
				Source:     "noaa_forecast",
				MarketID:   "weather-markets",
				Instrument: "WEATHER-LA-RAIN",
				Direction:  signal.Buy,
				TrueProb:   0.35,
				MarketProb: 0.30,
				Edge:       0.05,
				Confidence: 0.60,
				Metadata:   map[string]any{"city": "LA", "condition": "rain", "forecast_mm": 12},
			},
			{
				ID:         "weather-chi-snow-001",
				Timestamp:  now,
				Source:     "noaa_forecast",
				MarketID:   "weather-markets",
				Instrument: "WEATHER-CHI-SNOW",
				Direction:  signal.Buy,
				TrueProb:   0.20,
				MarketProb: 0.25,
				Edge:       -0.05, // negative edge — should be filtered
				Confidence: 0.90,
				Metadata:   map[string]any{"city": "Chicago", "condition": "snow"},
			},
		},
	}
}

// Generate returns the current weather signals.
func (g *WeatherGenerator) Generate(_ context.Context) ([]signal.Signal, error) {
	return g.signals, nil
}
