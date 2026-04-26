package sizing_test

import (
	"math"
	"testing"

	"github.com/yunus/botagent/sizing"
)

func TestKellySizer_BasicCase(t *testing.T) {
	ks := sizing.NewKelly(0.25, 0.10, 1.0)
	size := ks.Size(sizing.SizeParams{
		TrueProb:       0.65,
		MarketProb:     0.55,
		PortfolioValue: 1000,
		Confidence:     1.0,
	})
	if size <= 0 {
		t.Errorf("expected positive size, got %v", size)
	}
	if size > 100 { // 10% of 1000
		t.Errorf("size %v exceeds max position (10%% of 1000 = 100)", size)
	}
}

func TestKellySizer_ZeroEdge(t *testing.T) {
	ks := sizing.NewKelly(0.25, 0.10, 1.0)
	size := ks.Size(sizing.SizeParams{
		TrueProb:       0.50,
		MarketProb:     0.50,
		PortfolioValue: 1000,
		Confidence:     1.0,
	})
	if size != 0 {
		t.Errorf("zero edge should produce zero size, got %v", size)
	}
}

func TestKellySizer_NegativeEdge(t *testing.T) {
	ks := sizing.NewKelly(0.25, 0.10, 1.0)
	size := ks.Size(sizing.SizeParams{
		TrueProb:       0.40,
		MarketProb:     0.55,
		PortfolioValue: 1000,
		Confidence:     1.0,
	})
	if size != 0 {
		t.Errorf("negative edge should produce zero size, got %v", size)
	}
}

func TestKellySizer_MaxPositionCap(t *testing.T) {
	ks := sizing.NewKelly(1.0, 0.05, 0) // full Kelly, 5% cap
	size := ks.Size(sizing.SizeParams{
		TrueProb:       0.90,
		MarketProb:     0.50,
		PortfolioValue: 1000,
		Confidence:     1.0,
	})
	if size > 50.01 { // 5% of 1000 + rounding tolerance
		t.Errorf("size %v exceeds max position cap of 50", size)
	}
}

func TestKellySizer_MinBetSize(t *testing.T) {
	ks := sizing.NewKelly(0.01, 0.10, 5.0) // very small Kelly, $5 minimum
	size := ks.Size(sizing.SizeParams{
		TrueProb:       0.56,
		MarketProb:     0.55,
		PortfolioValue: 100,
		Confidence:     1.0,
	})
	// With 1% Kelly fraction and small edge on $100, size should be well under $5
	if size != 0 {
		t.Errorf("expected 0 (below min bet), got %v", size)
	}
}

func TestKellySizer_ZeroPortfolio(t *testing.T) {
	ks := sizing.NewKelly(0.25, 0.10, 1.0)
	size := ks.Size(sizing.SizeParams{
		TrueProb:       0.65,
		MarketProb:     0.55,
		PortfolioValue: 0,
	})
	if size != 0 {
		t.Errorf("zero portfolio should produce zero size, got %v", size)
	}
}

func TestKellySizer_InvalidMarketProb(t *testing.T) {
	ks := sizing.NewKelly(0.25, 0.10, 1.0)

	for _, mp := range []float64{0, 1, -0.5, 1.5} {
		size := ks.Size(sizing.SizeParams{
			TrueProb:       0.65,
			MarketProb:     mp,
			PortfolioValue: 1000,
			Confidence:     1.0,
		})
		if size != 0 {
			t.Errorf("MarketProb=%v should produce zero size, got %v", mp, size)
		}
	}
}

func TestKellySizer_ConfidenceScaling(t *testing.T) {
	ks := sizing.NewKelly(0.25, 0.50, 0)
	params := sizing.SizeParams{
		TrueProb:       0.70,
		MarketProb:     0.50,
		PortfolioValue: 1000,
	}

	params.Confidence = 1.0
	fullSize := ks.Size(params)

	params.Confidence = 0.5
	halfSize := ks.Size(params)

	if halfSize >= fullSize {
		t.Errorf("half confidence size (%v) should be less than full confidence (%v)", halfSize, fullSize)
	}
	// Should be roughly half (within rounding)
	ratio := halfSize / fullSize
	if math.Abs(ratio-0.5) > 0.02 {
		t.Errorf("size ratio = %v, expected ~0.5", ratio)
	}
}

func TestKellySizer_DefaultConfidence(t *testing.T) {
	ks := sizing.NewKelly(0.25, 0.50, 0)
	params := sizing.SizeParams{
		TrueProb:       0.70,
		MarketProb:     0.50,
		PortfolioValue: 1000,
		Confidence:     0, // should default to 1.0
	}
	size := ks.Size(params)
	if size <= 0 {
		t.Errorf("zero confidence should default to 1.0, got size %v", size)
	}
}

func TestKellySizer_RoundsToCents(t *testing.T) {
	ks := sizing.NewKelly(0.25, 0.50, 0)
	size := ks.Size(sizing.SizeParams{
		TrueProb:       0.65,
		MarketProb:     0.55,
		PortfolioValue: 1000,
		Confidence:     1.0,
	})
	// Check that size is rounded to 2 decimal places
	rounded := math.Round(size*100) / 100
	if size != rounded {
		t.Errorf("size %v is not rounded to cents (expected %v)", size, rounded)
	}
}

// Verify that the claude-bot formula (edge+odds) and soccerzock formula (trueProb+marketProb)
// produce the same result when given equivalent inputs.
func TestKellySizer_EquivalenceWithLegacyFormulas(t *testing.T) {
	trueProb := 0.65
	marketProb := 0.55
	fraction := 0.25
	portfolio := 1000.0
	maxPct := 0.15

	// --- botagent unified formula ---
	ks := sizing.NewKelly(fraction, maxPct, 0)
	unified := ks.Size(sizing.SizeParams{
		TrueProb:       trueProb,
		MarketProb:     marketProb,
		PortfolioValue: portfolio,
		Confidence:     1.0,
	})

	// --- claude-bot formula (edge+odds) ---
	edge := trueProb - (1.0 / (1.0 / marketProb)) // = trueProb - marketProb via implied prob
	odds := 1.0 / marketProb
	b := odds - 1
	impliedProb := 1.0 / odds
	p := impliedProb + edge
	q := 1.0 - p
	kellyF := (b*p - q) / b
	claudeSize := math.Round(math.Min(kellyF*fraction, maxPct)*portfolio*100) / 100

	// --- soccerzock formula ---
	kellyFSoccer := (trueProb*odds - 1) / (odds - 1)
	soccerSize := math.Round(math.Min(kellyFSoccer*fraction, maxPct)*portfolio*100) / 100

	if math.Abs(unified-claudeSize) > 0.01 {
		t.Errorf("unified (%v) != claude-bot formula (%v)", unified, claudeSize)
	}
	if math.Abs(unified-soccerSize) > 0.01 {
		t.Errorf("unified (%v) != soccerzock formula (%v)", unified, soccerSize)
	}
}

func TestKellySizer_TrueProbNearOne(t *testing.T) {
	ks := sizing.NewKelly(0.25, 0.10, 0)
	size := ks.Size(sizing.SizeParams{
		TrueProb:       0.999,
		MarketProb:     0.50,
		PortfolioValue: 1000,
		Confidence:     1.0,
	})
	// Should be capped at max position
	if size > 100.01 {
		t.Errorf("near-certain trueProb should be capped, got %v", size)
	}
	if size <= 0 {
		t.Errorf("near-certain trueProb should produce positive size, got %v", size)
	}
}
