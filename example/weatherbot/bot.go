package weatherbot

import (
	"context"
	"time"

	"github.com/yun-jay/botagent/bot"
	"github.com/yun-jay/botagent/example/mockexchange"
	"github.com/yun-jay/botagent/order"
	"github.com/yun-jay/botagent/pipeline"
	"github.com/yun-jay/botagent/sizing"
	"github.com/yun-jay/botagent/trade"
)

// WeatherBot is an example bot that trades weather prediction markets
// on a mock exchange.
type WeatherBot struct {
	pipe     *pipeline.Pipeline
	exchange *mockexchange.Exchange
	recorder *trade.InMemoryRecorder
}

// Name returns the bot name.
func (b *WeatherBot) Name() string { return "weather-bot" }

// Init sets up the weather bot with a mock exchange.
func (b *WeatherBot) Init(_ context.Context, deps *bot.Deps) error {
	b.exchange = mockexchange.New(deps.Logger)
	b.exchange.AddMarket("WEATHER-NYC-HOT", 0.60, 0.58, 0.62)
	b.exchange.AddMarket("WEATHER-LA-RAIN", 0.30, 0.28, 0.32)
	b.exchange.AddMarket("WEATHER-CHI-SNOW", 0.25, 0.23, 0.27)

	b.recorder = trade.NewInMemoryRecorder()

	b.pipe = pipeline.New(
		NewDefaultGenerator(),
		&WeatherEvaluator{
			Sizer:          sizing.NewKelly(0.25, 0.10, 1.0),
			MinEdge:        0.03,
			MinConfidence:  0.5,
			PortfolioValue: 1000,
		},
		b.exchange,
		b.recorder,
		pipeline.WithKillSwitch(deps.KillSwitch),
		pipeline.WithPositionGuard(deps.Guard),
		pipeline.WithDryRun(deps.DryRun),
		pipeline.WithLogger(deps.Logger),
		pipeline.WithAlerter(deps.Alerter),
	)

	return nil
}

// Run executes the bot's main loop.
func (b *WeatherBot) Run(ctx context.Context) error {
	return b.pipe.RunEvery(ctx, 10*time.Second)
}

// Shutdown cleans up resources.
func (b *WeatherBot) Shutdown(ctx context.Context) error {
	return b.exchange.CancelAll(ctx)
}

// NewTestPipeline creates a pipeline for testing without the bot.Run lifecycle.
func NewTestPipeline(
	gen *WeatherGenerator,
	eval *WeatherEvaluator,
	exec order.Executor,
	rec trade.Recorder,
	opts ...pipeline.Option,
) *pipeline.Pipeline {
	return pipeline.New(gen, eval, exec, rec, opts...)
}
