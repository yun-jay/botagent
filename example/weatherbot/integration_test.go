package weatherbot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/yunus/botagent/alert"
	"github.com/yunus/botagent/example/mockexchange"
	"github.com/yunus/botagent/example/weatherbot"
	"github.com/yunus/botagent/pipeline"
	"github.com/yunus/botagent/risk"
	"github.com/yunus/botagent/signal"
	"github.com/yunus/botagent/sizing"
	"github.com/yunus/botagent/trade"
)

func setupExchange(logger *slog.Logger) *mockexchange.Exchange {
	ex := mockexchange.New(logger)
	ex.AddMarket("WEATHER-NYC-HOT", 0.60, 0.58, 0.62)
	ex.AddMarket("WEATHER-LA-RAIN", 0.30, 0.28, 0.32)
	ex.AddMarket("WEATHER-CHI-SNOW", 0.25, 0.23, 0.27)
	return ex
}

func setupEvaluator() *weatherbot.WeatherEvaluator {
	return &weatherbot.WeatherEvaluator{
		Sizer:          sizing.NewKelly(0.25, 0.10, 1.0),
		MinEdge:        0.03,
		MinConfidence:  0.5,
		PortfolioValue: 1000,
	}
}

// TestIntegration_FullPipeline tests the complete signal -> evaluate -> execute -> record flow.
func TestIntegration_FullPipeline(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()
	gen := weatherbot.NewDefaultGenerator()

	p := weatherbot.NewTestPipeline(gen, setupEvaluator(), ex, rec,
		pipeline.WithLogger(logger),
	)

	err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// Default generator produces 3 signals:
	// 1. NYC-HOT: edge=0.10, confidence=0.85 -> should trade
	// 2. LA-RAIN: edge=0.05, confidence=0.60 -> should trade
	// 3. CHI-SNOW: edge=-0.05 -> should be filtered (negative edge)
	orders := ex.FilledOrders()
	if len(orders) != 2 {
		t.Fatalf("expected 2 filled orders, got %d", len(orders))
	}

	// Verify first order is NYC-HOT
	if orders[0].Instrument != "WEATHER-NYC-HOT" {
		t.Errorf("order[0].Instrument = %q, want WEATHER-NYC-HOT", orders[0].Instrument)
	}
	if orders[0].Side != "BUY" {
		t.Errorf("order[0].Side = %q, want BUY", orders[0].Side)
	}

	// Verify trades are recorded
	trades := rec.Trades()
	if len(trades) != 2 {
		t.Fatalf("expected 2 recorded trades, got %d", len(trades))
	}
	for _, tr := range trades {
		if tr.Status != "filled" {
			t.Errorf("trade status = %q, want filled", tr.Status)
		}
		if tr.OrderID == "" {
			t.Error("trade should have an order ID")
		}
		if tr.Size <= 0 {
			t.Errorf("trade size should be positive, got %v", tr.Size)
		}
	}

	// Verify structured JSON logs
	logLines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	for _, line := range logLines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("log line is not valid JSON: %s", line)
		}
	}
}

// TestIntegration_KillSwitchStopsTrading verifies the kill switch halts all trading.
func TestIntegration_KillSwitchStopsTrading(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()

	ks := risk.NewKillSwitch(0.10, 1000, logger)
	ks.TriggerSilently()

	p := weatherbot.NewTestPipeline(
		weatherbot.NewDefaultGenerator(), setupEvaluator(), ex, rec,
		pipeline.WithKillSwitch(ks),
		pipeline.WithLogger(logger),
	)

	p.RunOnce(context.Background())

	if len(ex.FilledOrders()) != 0 {
		t.Error("no orders should be filled when kill switch is active")
	}
	if len(rec.Trades()) != 0 {
		t.Error("no trades should be recorded when kill switch is active")
	}
}

// TestIntegration_PositionGuardPreventsDouble verifies no duplicate entries.
func TestIntegration_PositionGuardPreventsDouble(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()
	pg := risk.NewPositionGuard()

	// Pre-acquire a position on NYC-HOT
	pg.Acquire("WEATHER-NYC-HOT")

	p := weatherbot.NewTestPipeline(
		weatherbot.NewDefaultGenerator(), setupEvaluator(), ex, rec,
		pipeline.WithPositionGuard(pg),
		pipeline.WithLogger(logger),
	)

	p.RunOnce(context.Background())

	// Only LA-RAIN should trade (NYC-HOT blocked, CHI-SNOW filtered by evaluator)
	orders := ex.FilledOrders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 order (LA-RAIN only), got %d", len(orders))
	}
	if orders[0].Instrument != "WEATHER-LA-RAIN" {
		t.Errorf("expected LA-RAIN, got %s", orders[0].Instrument)
	}
}

// TestIntegration_DryRunMode verifies dry-run records but doesn't execute.
func TestIntegration_DryRunMode(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()
	dr := risk.NewDryRun(true, logger)

	p := weatherbot.NewTestPipeline(
		weatherbot.NewDefaultGenerator(), setupEvaluator(), ex, rec,
		pipeline.WithDryRun(dr),
		pipeline.WithLogger(logger),
	)

	p.RunOnce(context.Background())

	if len(ex.FilledOrders()) != 0 {
		t.Error("no orders should be filled in dry-run mode")
	}

	trades := rec.Trades()
	// All 3 signals go through dry-run (even the negative edge one, since dry-run is before evaluation)
	for _, tr := range trades {
		if tr.Status != "dry_run" {
			t.Errorf("trade status = %q, want dry_run", tr.Status)
		}
	}
}

// TestIntegration_EvaluatorFiltering verifies the evaluator correctly filters signals.
func TestIntegration_EvaluatorFiltering(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()

	// Use strict evaluator that only accepts high-edge signals
	eval := &weatherbot.WeatherEvaluator{
		Sizer:          sizing.NewKelly(0.25, 0.10, 1.0),
		MinEdge:        0.08,   // only NYC-HOT (0.10) passes
		MinConfidence:  0.5,
		PortfolioValue: 1000,
	}

	p := weatherbot.NewTestPipeline(
		weatherbot.NewDefaultGenerator(), eval, ex, rec,
		pipeline.WithLogger(logger),
	)

	p.RunOnce(context.Background())

	orders := ex.FilledOrders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 order (NYC-HOT only), got %d", len(orders))
	}
	if orders[0].Instrument != "WEATHER-NYC-HOT" {
		t.Errorf("expected NYC-HOT, got %s", orders[0].Instrument)
	}
}

// TestIntegration_KellySizing verifies Kelly sizes are reasonable.
func TestIntegration_KellySizing(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()

	p := weatherbot.NewTestPipeline(
		weatherbot.NewDefaultGenerator(), setupEvaluator(), ex, rec,
		pipeline.WithLogger(logger),
	)

	p.RunOnce(context.Background())

	trades := rec.Trades()
	for _, tr := range trades {
		if tr.Size > 100 { // 10% of $1000
			t.Errorf("trade size %v exceeds max position (10%% of $1000)", tr.Size)
		}
		if tr.Size < 1.0 {
			t.Errorf("trade size %v below minimum $1", tr.Size)
		}
	}
}

// TestIntegration_EventDrivenMode verifies the pipeline works with signal channel input.
func TestIntegration_EventDrivenMode(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()

	// In event-driven mode, generator is unused — signals come from channel
	p := weatherbot.NewTestPipeline(
		weatherbot.NewGenerator(nil), setupEvaluator(), ex, rec,
		pipeline.WithLogger(logger),
	)

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan []signal.Signal, 2)

	// Send a batch
	ch <- []signal.Signal{
		{
			ID: "evt-1", Instrument: "WEATHER-NYC-HOT", Direction: signal.Buy,
			TrueProb: 0.70, MarketProb: 0.60, Edge: 0.10, Confidence: 0.85,
		},
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := p.Run(ctx, ch)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	if len(ex.FilledOrders()) != 1 {
		t.Errorf("expected 1 filled order in event-driven mode, got %d", len(ex.FilledOrders()))
	}
}

// TestIntegration_PollingMode verifies RunEvery works with short intervals.
func TestIntegration_PollingMode(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()

	p := weatherbot.NewTestPipeline(
		weatherbot.NewDefaultGenerator(), setupEvaluator(), ex, rec,
		pipeline.WithLogger(logger),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()

	err := p.RunEvery(ctx, 20*time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	// Should have at least 1 cycle of trades
	if len(ex.FilledOrders()) < 2 {
		t.Errorf("expected at least 2 filled orders from polling, got %d", len(ex.FilledOrders()))
	}
}

// TestIntegration_AlertsFired verifies alerts are sent on trades.
func TestIntegration_AlertsFired(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()
	a := &testAlerter{}

	p := weatherbot.NewTestPipeline(
		weatherbot.NewDefaultGenerator(), setupEvaluator(), ex, rec,
		pipeline.WithAlerter(a),
		pipeline.WithLogger(logger),
	)

	p.RunOnce(context.Background())

	if len(a.messages) != 2 {
		t.Errorf("expected 2 alerts (2 trades), got %d", len(a.messages))
	}
	for _, msg := range a.messages {
		if !strings.Contains(msg, "Trade:") {
			t.Errorf("alert should contain 'Trade:', got: %s", msg)
		}
	}
}

// TestIntegration_StructuredLogs verifies all log output is valid JSON with expected fields.
func TestIntegration_StructuredLogs(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()

	p := weatherbot.NewTestPipeline(
		weatherbot.NewDefaultGenerator(), setupEvaluator(), ex, rec,
		pipeline.WithLogger(logger),
	)

	p.RunOnce(context.Background())

	logLines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(logLines) == 0 {
		t.Fatal("expected log output")
	}

	for i, line := range logLines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d is not valid JSON: %s", i, line)
			continue
		}
		// All log lines must have time, level, msg
		for _, field := range []string{"time", "level", "msg"} {
			if _, ok := entry[field]; !ok {
				t.Errorf("line %d missing field %q: %s", i, field, line)
			}
		}
	}
}

// TestIntegration_ExecutionFailure verifies the pipeline handles execution errors gracefully.
func TestIntegration_ExecutionFailure(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ex := setupExchange(logger)
	rec := trade.NewInMemoryRecorder()
	pg := risk.NewPositionGuard()

	// Make first execution fail
	ex.SetFailNext(true)

	p := weatherbot.NewTestPipeline(
		weatherbot.NewDefaultGenerator(), setupEvaluator(), ex, rec,
		pipeline.WithPositionGuard(pg),
		pipeline.WithLogger(logger),
	)

	p.RunOnce(context.Background())

	// First signal (NYC-HOT) should fail, guard should be released
	if pg.IsActive("WEATHER-NYC-HOT") {
		t.Error("guard should be released after execution failure")
	}

	// Second signal (LA-RAIN) should still succeed
	orders := ex.FilledOrders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 filled order (LA-RAIN), got %d", len(orders))
	}
	if orders[0].Instrument != "WEATHER-LA-RAIN" {
		t.Errorf("expected LA-RAIN, got %s", orders[0].Instrument)
	}
}

// --- test helpers ---

type testAlerter struct {
	messages []string
}

func (a *testAlerter) Send(_ context.Context, msg string) error {
	a.messages = append(a.messages, msg)
	return nil
}
func (a *testAlerter) IsConfigured() bool { return true }

// Verify interface compliance
var _ alert.Alerter = (*testAlerter)(nil)
