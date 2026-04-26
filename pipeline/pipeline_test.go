package pipeline_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/yunus/botagent/order"
	"github.com/yunus/botagent/pipeline"
	"github.com/yunus/botagent/risk"
	"github.com/yunus/botagent/signal"
	"github.com/yunus/botagent/strategy"
	"github.com/yunus/botagent/trade"
)

// --- Mock implementations ---

type mockGenerator struct {
	signals []signal.Signal
	err     error
}

func (m *mockGenerator) Generate(_ context.Context) ([]signal.Signal, error) {
	return m.signals, m.err
}

type mockEvaluator struct {
	approve  bool
	size     float64
	rejectReason string
	err     error
}

func (m *mockEvaluator) Evaluate(_ context.Context, sig signal.Signal) (strategy.Decision, error) {
	if m.err != nil {
		return strategy.Decision{}, m.err
	}
	return strategy.Decision{
		Approved:      m.approve,
		Signal:        sig,
		PositionSize:  m.size,
		ReasonSkipped: m.rejectReason,
	}, nil
}

type mockExecutor struct {
	result order.Result
	err    error
	calls  []order.Request
}

func (m *mockExecutor) Execute(_ context.Context, req order.Request) (order.Result, error) {
	m.calls = append(m.calls, req)
	return m.result, m.err
}

func (m *mockExecutor) CancelAll(_ context.Context) error { return nil }

type mockAlerter struct {
	messages []string
}

func (m *mockAlerter) Send(_ context.Context, msg string) error {
	m.messages = append(m.messages, msg)
	return nil
}
func (m *mockAlerter) IsConfigured() bool { return true }

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

func testSignal(instrument string) signal.Signal {
	return signal.Signal{
		ID:         "sig-" + instrument,
		MarketID:   "market-1",
		Instrument: instrument,
		Direction:  signal.Buy,
		TrueProb:   0.65,
		MarketProb: 0.55,
		Edge:       0.10,
		Confidence: 1.0,
	}
}

// --- Tests ---

func TestRunOnce_FullPipeline(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50.0}
	exec := &mockExecutor{result: order.Result{OrderID: "ord-1", Status: "filled", FillPrice: 0.55}}
	rec := trade.NewInMemoryRecorder()

	p := pipeline.New(gen, eval, exec, rec, pipeline.WithLogger(testLogger()))
	err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	if len(exec.calls) != 1 {
		t.Fatalf("executor called %d times, want 1", len(exec.calls))
	}
	if exec.calls[0].Instrument != "TOKEN-A" {
		t.Errorf("instrument = %q, want TOKEN-A", exec.calls[0].Instrument)
	}
	if exec.calls[0].Size != 50.0 {
		t.Errorf("size = %v, want 50", exec.calls[0].Size)
	}

	trades := rec.Trades()
	if len(trades) != 1 {
		t.Fatalf("recorded %d trades, want 1", len(trades))
	}
	if trades[0].Status != "filled" {
		t.Errorf("status = %q, want filled", trades[0].Status)
	}
	if trades[0].OrderID != "ord-1" {
		t.Errorf("orderID = %q, want ord-1", trades[0].OrderID)
	}
}

func TestRunOnce_EvaluatorRejects(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: false, rejectReason: "low edge"}
	exec := &mockExecutor{}
	rec := trade.NewInMemoryRecorder()

	p := pipeline.New(gen, eval, exec, rec, pipeline.WithLogger(testLogger()))
	p.RunOnce(context.Background())

	if len(exec.calls) != 0 {
		t.Error("executor should not be called when evaluator rejects")
	}
	if len(rec.Trades()) != 0 {
		t.Error("no trades should be recorded when rejected")
	}
}

func TestRunOnce_KillSwitchBlocks(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{}
	rec := trade.NewInMemoryRecorder()

	ks := risk.NewKillSwitch(0.10, 1000, testLogger())
	ks.TriggerSilently()

	p := pipeline.New(gen, eval, exec, rec,
		pipeline.WithKillSwitch(ks),
		pipeline.WithLogger(testLogger()),
	)
	p.RunOnce(context.Background())

	if len(exec.calls) != 0 {
		t.Error("executor should not be called when kill switch is triggered")
	}
}

func TestRunOnce_PositionGuardPreventsDouble(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{
		testSignal("TOKEN-A"),
		testSignal("TOKEN-A"), // duplicate
	}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{result: order.Result{Status: "filled", FillPrice: 0.55}}
	rec := trade.NewInMemoryRecorder()

	pg := risk.NewPositionGuard()

	p := pipeline.New(gen, eval, exec, rec,
		pipeline.WithPositionGuard(pg),
		pipeline.WithLogger(testLogger()),
	)
	p.RunOnce(context.Background())

	if len(exec.calls) != 1 {
		t.Errorf("executor called %d times, want 1 (guard should block duplicate)", len(exec.calls))
	}
}

func TestRunOnce_DryRunSkipsExecution(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{}
	rec := trade.NewInMemoryRecorder()

	dr := risk.NewDryRun(true, testLogger())

	p := pipeline.New(gen, eval, exec, rec,
		pipeline.WithDryRun(dr),
		pipeline.WithLogger(testLogger()),
	)
	p.RunOnce(context.Background())

	if len(exec.calls) != 0 {
		t.Error("executor should not be called in dry-run mode")
	}

	trades := rec.Trades()
	if len(trades) != 1 {
		t.Fatalf("dry-run should still record, got %d trades", len(trades))
	}
	if trades[0].Status != "dry_run" {
		t.Errorf("status = %q, want dry_run", trades[0].Status)
	}
}

func TestRunOnce_GeneratorError(t *testing.T) {
	gen := &mockGenerator{err: errors.New("fetch failed")}
	eval := &mockEvaluator{}
	exec := &mockExecutor{}
	rec := trade.NewInMemoryRecorder()

	p := pipeline.New(gen, eval, exec, rec, pipeline.WithLogger(testLogger()))
	err := p.RunOnce(context.Background())
	if err == nil {
		t.Error("RunOnce should propagate generator errors")
	}
}

func TestRunOnce_EvaluatorError(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{err: errors.New("eval failed")}
	exec := &mockExecutor{}
	rec := trade.NewInMemoryRecorder()

	pg := risk.NewPositionGuard()
	p := pipeline.New(gen, eval, exec, rec,
		pipeline.WithPositionGuard(pg),
		pipeline.WithLogger(testLogger()),
	)
	p.RunOnce(context.Background())

	if len(exec.calls) != 0 {
		t.Error("executor should not be called on evaluator error")
	}
	// Guard should be released on error
	if pg.IsActive("TOKEN-A") {
		t.Error("guard should be released on evaluator error")
	}
}

func TestRunOnce_ExecutionFailure_ReleasesGuard(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{err: errors.New("network error")}

	pg := risk.NewPositionGuard()
	p := pipeline.New(gen, eval, exec, trade.NewInMemoryRecorder(),
		pipeline.WithPositionGuard(pg),
		pipeline.WithLogger(testLogger()),
	)
	p.RunOnce(context.Background())

	if pg.IsActive("TOKEN-A") {
		t.Error("guard should be released on execution error")
	}
}

func TestRunOnce_FailedStatus_ReleasesGuard(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{result: order.Result{Status: "failed"}}

	pg := risk.NewPositionGuard()
	p := pipeline.New(gen, eval, exec, trade.NewInMemoryRecorder(),
		pipeline.WithPositionGuard(pg),
		pipeline.WithLogger(testLogger()),
	)
	p.RunOnce(context.Background())

	if pg.IsActive("TOKEN-A") {
		t.Error("guard should be released on failed status")
	}
}

func TestRunOnce_AlertOnTrade(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{result: order.Result{Status: "filled", FillPrice: 0.55}}
	a := &mockAlerter{}

	p := pipeline.New(gen, eval, exec, trade.NewInMemoryRecorder(),
		pipeline.WithAlerter(a),
		pipeline.WithLogger(testLogger()),
	)
	p.RunOnce(context.Background())

	if len(a.messages) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(a.messages))
	}
}

func TestRunOnce_MultipleSignals(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{
		testSignal("TOKEN-A"),
		testSignal("TOKEN-B"),
		testSignal("TOKEN-C"),
	}}
	eval := &mockEvaluator{approve: true, size: 25}
	exec := &mockExecutor{result: order.Result{Status: "filled", FillPrice: 0.55}}
	rec := trade.NewInMemoryRecorder()

	p := pipeline.New(gen, eval, exec, rec, pipeline.WithLogger(testLogger()))
	p.RunOnce(context.Background())

	if len(exec.calls) != 3 {
		t.Errorf("executor called %d times, want 3", len(exec.calls))
	}
	if len(rec.Trades()) != 3 {
		t.Errorf("recorded %d trades, want 3", len(rec.Trades()))
	}
}

func TestRun_EventDriven(t *testing.T) {
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{result: order.Result{Status: "filled", FillPrice: 0.55}}
	rec := trade.NewInMemoryRecorder()

	// Generator is not used in event-driven mode (signals come from channel)
	gen := &mockGenerator{}

	p := pipeline.New(gen, eval, exec, rec, pipeline.WithLogger(testLogger()))

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan []signal.Signal, 2)

	// Send two batches
	ch <- []signal.Signal{testSignal("TOKEN-A")}
	ch <- []signal.Signal{testSignal("TOKEN-B")}

	go func() {
		// Give pipeline time to process, then cancel
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := p.Run(ctx, ch)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run should return context.Canceled, got: %v", err)
	}

	if len(exec.calls) != 2 {
		t.Errorf("executor called %d times, want 2", len(exec.calls))
	}
}

func TestRun_ChannelClosed(t *testing.T) {
	gen := &mockGenerator{}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{result: order.Result{Status: "filled"}}

	p := pipeline.New(gen, eval, exec, trade.NewInMemoryRecorder(), pipeline.WithLogger(testLogger()))

	ch := make(chan []signal.Signal)
	close(ch)

	err := p.Run(context.Background(), ch)
	if err != nil {
		t.Errorf("Run on closed channel should return nil, got: %v", err)
	}
}

func TestRunEvery_Cancellation(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{result: order.Result{Status: "filled", FillPrice: 0.55}}

	p := pipeline.New(gen, eval, exec, trade.NewInMemoryRecorder(), pipeline.WithLogger(testLogger()))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := p.RunEvery(ctx, 10*time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("RunEvery should return context.Canceled, got: %v", err)
	}
}

func TestRunOnce_NoSignals(t *testing.T) {
	gen := &mockGenerator{signals: nil}
	eval := &mockEvaluator{}
	exec := &mockExecutor{}
	rec := trade.NewInMemoryRecorder()

	p := pipeline.New(gen, eval, exec, rec, pipeline.WithLogger(testLogger()))
	err := p.RunOnce(context.Background())
	if err != nil {
		t.Errorf("RunOnce with no signals should not error, got: %v", err)
	}
	if len(exec.calls) != 0 {
		t.Error("no signals should mean no execution")
	}
}

func TestRunOnce_DryRunReleasesGuard(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{}

	pg := risk.NewPositionGuard()
	dr := risk.NewDryRun(true, testLogger())

	p := pipeline.New(gen, eval, exec, trade.NewInMemoryRecorder(),
		pipeline.WithPositionGuard(pg),
		pipeline.WithDryRun(dr),
		pipeline.WithLogger(testLogger()),
	)
	p.RunOnce(context.Background())

	// Guard should be released after dry-run so the same instrument can be re-evaluated
	if pg.IsActive("TOKEN-A") {
		t.Error("guard should be released after dry-run")
	}
}

func TestPipeline_NilOptions(t *testing.T) {
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{result: order.Result{Status: "filled", FillPrice: 0.55}}

	// No options — pipeline should work with defaults (no kill switch, no guard, no dry-run)
	p := pipeline.New(gen, eval, exec, trade.NewInMemoryRecorder())
	err := p.RunOnce(context.Background())
	if err != nil {
		t.Errorf("RunOnce without options should work, got: %v", err)
	}
}

func TestRunOnce_RiskGateOrder(t *testing.T) {
	// Verify: kill switch checked BEFORE guard acquisition
	gen := &mockGenerator{signals: []signal.Signal{testSignal("TOKEN-A")}}
	eval := &mockEvaluator{approve: true, size: 50}
	exec := &mockExecutor{}

	ks := risk.NewKillSwitch(0.10, 1000, testLogger())
	ks.TriggerSilently()
	pg := risk.NewPositionGuard()

	p := pipeline.New(gen, eval, exec, trade.NewInMemoryRecorder(),
		pipeline.WithKillSwitch(ks),
		pipeline.WithPositionGuard(pg),
		pipeline.WithLogger(testLogger()),
	)
	p.RunOnce(context.Background())

	// Guard should NOT have been acquired (kill switch blocks first)
	if pg.IsActive("TOKEN-A") {
		t.Error("guard should not be acquired when kill switch is active")
	}
}
