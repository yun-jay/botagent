package bot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yun-jay/botagent/alert"
	"github.com/yun-jay/botagent/config"
	botlog "github.com/yun-jay/botagent/log"
	"github.com/yun-jay/botagent/risk"
)

// Bot is the interface every trading bot implements.
type Bot interface {
	// Name returns a human-readable identifier.
	Name() string
	// Init sets up bot-specific infrastructure.
	Init(ctx context.Context, deps *Deps) error
	// Run is the main loop. Must respect ctx cancellation.
	Run(ctx context.Context) error
	// Shutdown is called on SIGINT/SIGTERM with a timeout context.
	Shutdown(ctx context.Context) error
}

// Deps is what the framework provides to the bot during Init.
type Deps struct {
	Logger     *slog.Logger
	Alerter    alert.Alerter
	KillSwitch *risk.KillSwitch
	Guard      *risk.PositionGuard
	DryRun     *risk.DryRun
}

// Config controls framework behavior.
type Config struct {
	Name           string
	Verbose        bool
	TelegramToken  string
	TelegramChatID string
	MaxDrawdownPct float64 // 0 = no kill switch
	PortfolioValue float64
	DryRunEnabled  bool
	ShutdownTimeout time.Duration // 0 defaults to 10s
}

func (c Config) shutdownTimeout() time.Duration {
	if c.ShutdownTimeout > 0 {
		return c.ShutdownTimeout
	}
	return 10 * time.Second
}

// Run is the framework entry point. Call from main().
// It loads .env, sets up logging, and manages the full bot lifecycle
// including graceful shutdown on SIGINT/SIGTERM.
func Run(b Bot, cfg Config) {
	if err := config.LoadEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}

	logger := botlog.New(cfg.Name, cfg.Verbose)
	deps := BuildDeps(cfg, logger)

	// Wire OS signals into a channel
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if err := RunLifecycle(context.Background(), b, cfg, deps, logger, sigCh); err != nil {
		logger.Error("bot exited with error", "error", err)
		os.Exit(1)
	}
}

// RunLifecycle executes the full bot lifecycle with an injectable signal channel.
// This is the testable core of the framework. In production, Run() passes OS signals.
// In tests, you pass a channel you control.
//
// Lifecycle:
//  1. Init the bot
//  2. Send startup alert
//  3. Run the bot in a goroutine
//  4. Wait for: OS signal, context cancellation, or bot error
//  5. Cancel the bot's context
//  6. Call Shutdown with a timeout
//  7. Send shutdown alert
func RunLifecycle(parent context.Context, b Bot, cfg Config, deps *Deps, logger *slog.Logger, stopCh <-chan os.Signal) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// 1. Init
	logger.Info("initializing bot", "name", cfg.Name)
	if err := b.Init(ctx, deps); err != nil {
		return fmt.Errorf("bot init failed: %w", err)
	}

	// 2. Startup alert
	mode := "PAPER"
	if !cfg.DryRunEnabled {
		mode = "LIVE"
	}
	startMsg := fmt.Sprintf("Bot %s started (%s mode, portfolio: $%.2f)", cfg.Name, mode, cfg.PortfolioValue)
	if err := deps.Alerter.Send(ctx, startMsg); err != nil {
		logger.Warn("startup alert failed", "error", err)
	}

	// 3. Run bot in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Run(ctx)
	}()

	logger.Info("bot running", "name", cfg.Name, "mode", mode)

	// 4. Wait for stop signal, parent cancellation, or bot error
	var runErr error
	runDone := false
	select {
	case sig := <-stopCh:
		logger.Info("received signal, shutting down", "signal", sig.String())
	case <-parent.Done():
		logger.Info("context cancelled, shutting down")
	case err := <-errCh:
		runDone = true
		if err != nil && err != context.Canceled {
			runErr = err
			logger.Error("bot run error", "error", err)
		}
	}

	// 5. Cancel bot context (tells b.Run to stop)
	cancel()

	// Wait for Run to actually return (with timeout), unless it already did
	if !runDone {
		select {
		case <-errCh:
		case <-time.After(cfg.shutdownTimeout()):
			logger.Warn("bot.Run did not return within shutdown timeout")
		}
	}

	// 6. Shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout())
	defer shutdownCancel()

	logger.Info("shutting down bot")
	if err := b.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
		if runErr == nil {
			runErr = fmt.Errorf("shutdown failed: %w", err)
		}
	}

	// 7. Shutdown alert
	shutdownMsg := fmt.Sprintf("Bot %s stopped", cfg.Name)
	if err := deps.Alerter.Send(shutdownCtx, shutdownMsg); err != nil {
		logger.Warn("shutdown alert failed", "error", err)
	}

	logger.Info("bot stopped")
	return runErr
}

// BuildDeps creates the framework dependencies from a Config.
func BuildDeps(cfg Config, logger *slog.Logger) *Deps {
	deps := &Deps{
		Logger: logger,
		Guard:  risk.NewPositionGuard(),
		DryRun: risk.NewDryRun(cfg.DryRunEnabled, botlog.WithComponent(logger, "dryrun")),
	}

	if cfg.TelegramToken != "" && cfg.TelegramChatID != "" {
		deps.Alerter = alert.NewTelegram(cfg.TelegramToken, cfg.TelegramChatID,
			botlog.WithComponent(logger, "alert"))
	} else {
		deps.Alerter = &alert.NopAlerter{}
	}

	if cfg.MaxDrawdownPct > 0 && cfg.PortfolioValue > 0 {
		deps.KillSwitch = risk.NewKillSwitch(cfg.MaxDrawdownPct, cfg.PortfolioValue,
			botlog.WithComponent(logger, "killswitch"))
	}

	return deps
}
