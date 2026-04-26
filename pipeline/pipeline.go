package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yunus/botagent/alert"
	"github.com/yunus/botagent/order"
	"github.com/yunus/botagent/risk"
	"github.com/yunus/botagent/signal"
	"github.com/yunus/botagent/strategy"
	"github.com/yunus/botagent/trade"
)

// Pipeline wires Generator -> Evaluator -> Executor -> Recorder
// with risk gates (kill switch, position guard, dry-run) in between.
type Pipeline struct {
	generator signal.Generator
	evaluator strategy.Evaluator
	executor  order.Executor
	recorder  trade.Recorder

	killSwitch *risk.KillSwitch
	guard      *risk.PositionGuard
	dryRun     *risk.DryRun

	logger  *slog.Logger
	alerter alert.Alerter
}

// Option configures the pipeline.
type Option func(*Pipeline)

// WithKillSwitch adds a kill switch to the pipeline.
func WithKillSwitch(ks *risk.KillSwitch) Option {
	return func(p *Pipeline) { p.killSwitch = ks }
}

// WithPositionGuard adds a position guard to the pipeline.
func WithPositionGuard(pg *risk.PositionGuard) Option {
	return func(p *Pipeline) { p.guard = pg }
}

// WithDryRun adds dry-run mode to the pipeline.
func WithDryRun(dr *risk.DryRun) Option {
	return func(p *Pipeline) { p.dryRun = dr }
}

// WithLogger sets the pipeline logger.
func WithLogger(l *slog.Logger) Option {
	return func(p *Pipeline) { p.logger = l }
}

// WithAlerter sets the pipeline alerter.
func WithAlerter(a alert.Alerter) Option {
	return func(p *Pipeline) { p.alerter = a }
}

// New creates a new Pipeline wiring all stages together.
func New(
	gen signal.Generator,
	eval strategy.Evaluator,
	exec order.Executor,
	rec trade.Recorder,
	opts ...Option,
) *Pipeline {
	p := &Pipeline{
		generator: gen,
		evaluator: eval,
		executor:  exec,
		recorder:  rec,
		logger:    slog.Default(),
		alerter:   &alert.NopAlerter{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Run listens for signals on a channel (event-driven, primary mode).
// The pipeline processes each received signal batch through the full pipeline.
func (p *Pipeline) Run(ctx context.Context, signals <-chan []signal.Signal) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case batch, ok := <-signals:
			if !ok {
				return nil // channel closed
			}
			p.processBatch(ctx, batch)
		}
	}
}

// RunEvery polls the Generator on a fixed interval.
func (p *Pipeline) RunEvery(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := p.RunOnce(ctx); err != nil {
				p.logger.Error("pipeline cycle failed", "error", err)
			}
		}
	}
}

// RunOnce executes one pipeline cycle: generate -> evaluate -> execute -> record.
func (p *Pipeline) RunOnce(ctx context.Context) error {
	signals, err := p.generator.Generate(ctx)
	if err != nil {
		return fmt.Errorf("generate signals: %w", err)
	}
	p.processBatch(ctx, signals)
	return nil
}

func (p *Pipeline) processBatch(ctx context.Context, signals []signal.Signal) {
	for _, sig := range signals {
		p.processSignal(ctx, sig)
	}
}

func (p *Pipeline) processSignal(ctx context.Context, sig signal.Signal) {
	// Risk gate 1: Kill switch
	if p.killSwitch != nil && p.killSwitch.IsTriggered() {
		p.logger.Warn("kill switch active, skipping signal",
			"signal_id", sig.ID, "instrument", sig.Instrument)
		return
	}

	// Risk gate 2: Position guard
	if p.guard != nil {
		if !p.guard.Acquire(sig.Instrument) {
			p.logger.Info("position already active, skipping",
				"signal_id", sig.ID, "instrument", sig.Instrument)
			return
		}
	}

	// Risk gate 3: Dry-run
	if p.dryRun != nil && p.dryRun.ShouldSkip(sig, 0) {
		rec := &trade.Record{
			SignalID:   sig.ID,
			MarketID:   sig.MarketID,
			Instrument: sig.Instrument,
			Side:       sig.Direction.String(),
			Edge:       sig.Edge,
			Status:     "dry_run",
		}
		if _, err := p.recorder.Insert(ctx, rec); err != nil {
			p.logger.Error("failed to record dry-run trade", "error", err)
		}
		// Release guard for dry-run so instrument can be re-evaluated
		if p.guard != nil {
			p.guard.Release(sig.Instrument)
		}
		return
	}

	// Evaluate
	decision, err := p.evaluator.Evaluate(ctx, sig)
	if err != nil {
		p.logger.Error("evaluator error", "signal_id", sig.ID, "error", err)
		if p.guard != nil {
			p.guard.Release(sig.Instrument)
		}
		return
	}
	if !decision.Approved {
		p.logger.Debug("signal rejected",
			"signal_id", sig.ID, "instrument", sig.Instrument,
			"reason", decision.ReasonSkipped)
		if p.guard != nil {
			p.guard.Release(sig.Instrument)
		}
		return
	}

	// Execute
	req := order.Request{
		Signal:     decision.Signal,
		Side:       decision.Signal.Direction.String(),
		Instrument: decision.Signal.Instrument,
		Price:      decision.Signal.MarketProb,
		Size:       decision.PositionSize,
	}

	result, err := p.executor.Execute(ctx, req)
	if err != nil {
		p.logger.Error("execution failed",
			"signal_id", sig.ID, "instrument", sig.Instrument, "error", err)
		if p.guard != nil {
			p.guard.Release(sig.Instrument)
		}
		return
	}

	// Record
	rec := &trade.Record{
		SignalID:   sig.ID,
		MarketID:   sig.MarketID,
		Instrument: sig.Instrument,
		Side:       req.Side,
		Price:      result.FillPrice,
		Size:       decision.PositionSize,
		Edge:       sig.Edge,
		Status:     result.Status,
		OrderID:    result.OrderID,
	}

	tradeID, err := p.recorder.Insert(ctx, rec)
	if err != nil {
		p.logger.Error("failed to record trade", "error", err)
	}

	p.logger.Info("trade executed",
		"trade_id", tradeID,
		"signal_id", sig.ID,
		"instrument", sig.Instrument,
		"side", req.Side,
		"size", decision.PositionSize,
		"price", result.FillPrice,
		"order_id", result.OrderID,
		"status", result.Status,
	)

	// Alert
	if p.alerter != nil && p.alerter.IsConfigured() {
		msg := fmt.Sprintf("Trade: %s %s $%.2f @ %.4f (edge: %.2f%%)",
			req.Side, sig.Instrument, decision.PositionSize, result.FillPrice, sig.Edge*100)
		if err := p.alerter.Send(ctx, msg); err != nil {
			p.logger.Warn("alert failed", "error", err)
		}
	}

	// If execution failed, release guard
	if result.Status == "failed" {
		if p.guard != nil {
			p.guard.Release(sig.Instrument)
		}
	}
}
