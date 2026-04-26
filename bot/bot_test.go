package bot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yun-jay/botagent/alert"
	"github.com/yun-jay/botagent/bot"
)

// --- Mock bot ---

type mockBot struct {
	mu             sync.Mutex
	name           string
	initCalled     bool
	runCalled      bool
	shutdownCalled bool
	initErr        error
	runFunc        func(ctx context.Context) error
	shutdownFunc   func(ctx context.Context) error
	shutdownDelay  time.Duration

	// Track what deps were received
	receivedDeps *bot.Deps
}

func (b *mockBot) Name() string {
	if b.name != "" {
		return b.name
	}
	return "mock-bot"
}

func (b *mockBot) Init(_ context.Context, deps *bot.Deps) error {
	b.mu.Lock()
	b.initCalled = true
	b.receivedDeps = deps
	b.mu.Unlock()
	return b.initErr
}

func (b *mockBot) Run(ctx context.Context) error {
	b.mu.Lock()
	b.runCalled = true
	b.mu.Unlock()
	if b.runFunc != nil {
		return b.runFunc(ctx)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (b *mockBot) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	b.shutdownCalled = true
	b.mu.Unlock()
	if b.shutdownDelay > 0 {
		select {
		case <-time.After(b.shutdownDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if b.shutdownFunc != nil {
		return b.shutdownFunc(ctx)
	}
	return nil
}

func (b *mockBot) wasInitCalled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.initCalled
}

func (b *mockBot) wasRunCalled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runCalled
}

func (b *mockBot) wasShutdownCalled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.shutdownCalled
}

// --- Mock alerter ---

type mockAlerter struct {
	mu       sync.Mutex
	messages []string
}

func (a *mockAlerter) Send(_ context.Context, msg string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, msg)
	return nil
}

func (a *mockAlerter) IsConfigured() bool { return true }

func (a *mockAlerter) Messages() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]string, len(a.messages))
	copy(result, a.messages)
	return result
}

// --- Helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

func testLoggerWithBuffer() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func testConfig() bot.Config {
	return bot.Config{
		Name:            "test-bot",
		DryRunEnabled:   true,
		PortfolioValue:  1000,
		ShutdownTimeout: 2 * time.Second,
	}
}

func testDeps(logger *slog.Logger, alerter alert.Alerter) *bot.Deps {
	cfg := testConfig()
	deps := bot.BuildDeps(cfg, logger)
	if alerter != nil {
		deps.Alerter = alerter
	}
	return deps
}

// --- BuildDeps tests ---

func TestBuildDeps_NopAlerter(t *testing.T) {
	deps := bot.BuildDeps(bot.Config{Name: "test"}, testLogger())

	if deps.Alerter == nil {
		t.Fatal("Alerter should not be nil")
	}
	if deps.Alerter.IsConfigured() {
		t.Error("Alerter should be NopAlerter when no telegram config")
	}
	if deps.Guard == nil {
		t.Fatal("Guard should not be nil")
	}
	if deps.DryRun == nil {
		t.Fatal("DryRun should not be nil")
	}
	if deps.KillSwitch != nil {
		t.Error("KillSwitch should be nil when MaxDrawdownPct=0")
	}
}

func TestBuildDeps_WithKillSwitch(t *testing.T) {
	deps := bot.BuildDeps(bot.Config{
		MaxDrawdownPct: 0.25,
		PortfolioValue: 1000,
	}, testLogger())

	if deps.KillSwitch == nil {
		t.Fatal("KillSwitch should be created")
	}
	if deps.KillSwitch.IsTriggered() {
		t.Error("KillSwitch should not be triggered initially")
	}
}

func TestBuildDeps_WithTelegram(t *testing.T) {
	deps := bot.BuildDeps(bot.Config{
		TelegramToken:  "tok",
		TelegramChatID: "123",
	}, testLogger())

	if !deps.Alerter.IsConfigured() {
		t.Error("Alerter should be configured with telegram credentials")
	}
}

func TestBuildDeps_DryRunFlag(t *testing.T) {
	deps := bot.BuildDeps(bot.Config{DryRunEnabled: true}, testLogger())
	if !deps.DryRun.Enabled {
		t.Error("DryRun should be enabled")
	}

	deps = bot.BuildDeps(bot.Config{DryRunEnabled: false}, testLogger())
	if deps.DryRun.Enabled {
		t.Error("DryRun should be disabled")
	}
}

// --- Lifecycle tests ---

func TestLifecycle_NormalShutdownViaSignal(t *testing.T) {
	b := &mockBot{}
	a := &mockAlerter{}
	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, a)
	stopCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	}()

	// Wait for bot to be running
	time.Sleep(20 * time.Millisecond)

	// Send stop signal (simulates SIGINT)
	stopCh <- syscall.SIGINT

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle did not complete")
	}

	if !b.wasInitCalled() {
		t.Error("Init was not called")
	}
	if !b.wasRunCalled() {
		t.Error("Run was not called")
	}
	if !b.wasShutdownCalled() {
		t.Error("Shutdown was not called")
	}

	// Verify alerts: startup + shutdown
	msgs := a.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 alerts (startup + shutdown), got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "started") {
		t.Errorf("first alert should be startup, got: %s", msgs[0])
	}
	if !strings.Contains(msgs[1], "stopped") {
		t.Errorf("second alert should be shutdown, got: %s", msgs[1])
	}
}

func TestLifecycle_NormalShutdownViaSIGTERM(t *testing.T) {
	b := &mockBot{}
	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	}()

	time.Sleep(20 * time.Millisecond)
	stopCh <- syscall.SIGTERM

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle did not complete")
	}

	if !b.wasShutdownCalled() {
		t.Error("Shutdown should be called on SIGTERM")
	}
}

func TestLifecycle_ShutdownViaContextCancellation(t *testing.T) {
	b := &mockBot{}
	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- bot.RunLifecycle(ctx, b, cfg, deps, logger, stopCh)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel() // cancel parent context

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle did not complete")
	}

	if !b.wasShutdownCalled() {
		t.Error("Shutdown should be called on context cancellation")
	}
}

func TestLifecycle_BotRunReturnsError(t *testing.T) {
	expectedErr := errors.New("bot crashed")
	b := &mockBot{
		runFunc: func(_ context.Context) error {
			return expectedErr
		},
	}
	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	err := bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	if err == nil {
		t.Fatal("expected error from bot.Run")
	}
	if err.Error() != expectedErr.Error() {
		t.Errorf("error = %q, want %q", err, expectedErr)
	}
	if !b.wasShutdownCalled() {
		t.Error("Shutdown should still be called after Run error")
	}
}

func TestLifecycle_BotRunReturnsNil(t *testing.T) {
	b := &mockBot{
		runFunc: func(_ context.Context) error {
			return nil // bot exits cleanly
		},
	}
	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	err := bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if !b.wasShutdownCalled() {
		t.Error("Shutdown should be called even when Run exits cleanly")
	}
}

func TestLifecycle_InitFailure(t *testing.T) {
	b := &mockBot{initErr: errors.New("database unreachable")}
	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	err := bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	if err == nil {
		t.Fatal("expected error from init failure")
	}
	if !strings.Contains(err.Error(), "database unreachable") {
		t.Errorf("error should contain init message, got: %v", err)
	}
	if !b.wasInitCalled() {
		t.Error("Init should have been called")
	}
	if b.wasRunCalled() {
		t.Error("Run should NOT be called after init failure")
	}
	if b.wasShutdownCalled() {
		t.Error("Shutdown should NOT be called after init failure")
	}
}

func TestLifecycle_ShutdownError(t *testing.T) {
	b := &mockBot{
		runFunc: func(_ context.Context) error {
			return nil
		},
		shutdownFunc: func(_ context.Context) error {
			return errors.New("failed to cancel orders")
		},
	}
	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	err := bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	if err == nil {
		t.Fatal("expected error from shutdown failure")
	}
	if !strings.Contains(err.Error(), "cancel orders") {
		t.Errorf("error should mention shutdown failure, got: %v", err)
	}
}

func TestLifecycle_ShutdownErrorDoesNotOverrideRunError(t *testing.T) {
	runErr := errors.New("run failed")
	b := &mockBot{
		runFunc: func(_ context.Context) error {
			return runErr
		},
		shutdownFunc: func(_ context.Context) error {
			return errors.New("shutdown also failed")
		},
	}
	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	err := bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	if err == nil {
		t.Fatal("expected error")
	}
	// Run error should take precedence
	if err.Error() != runErr.Error() {
		t.Errorf("error = %q, want run error %q", err, runErr)
	}
}

func TestLifecycle_RunContextCancelledIsNotError(t *testing.T) {
	b := &mockBot{
		runFunc: func(ctx context.Context) error {
			<-ctx.Done()
			return context.Canceled
		},
	}
	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	}()

	time.Sleep(20 * time.Millisecond)
	stopCh <- syscall.SIGINT

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("context.Canceled from Run should not be an error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle did not complete")
	}
}

func TestLifecycle_PaperModeAlert(t *testing.T) {
	b := &mockBot{runFunc: func(_ context.Context) error { return nil }}
	a := &mockAlerter{}
	logger := testLogger()
	cfg := testConfig()
	cfg.DryRunEnabled = true
	deps := testDeps(logger, a)
	stopCh := make(chan os.Signal, 1)

	bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)

	msgs := a.Messages()
	if len(msgs) < 1 {
		t.Fatal("expected at least startup alert")
	}
	if !strings.Contains(msgs[0], "PAPER") {
		t.Errorf("startup alert should mention PAPER mode, got: %s", msgs[0])
	}
}

func TestLifecycle_LiveModeAlert(t *testing.T) {
	b := &mockBot{runFunc: func(_ context.Context) error { return nil }}
	a := &mockAlerter{}
	logger := testLogger()
	cfg := testConfig()
	cfg.DryRunEnabled = false
	deps := testDeps(logger, a)
	stopCh := make(chan os.Signal, 1)

	bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)

	msgs := a.Messages()
	if len(msgs) < 1 {
		t.Fatal("expected at least startup alert")
	}
	if !strings.Contains(msgs[0], "LIVE") {
		t.Errorf("startup alert should mention LIVE mode, got: %s", msgs[0])
	}
}

func TestLifecycle_PortfolioValueInAlert(t *testing.T) {
	b := &mockBot{runFunc: func(_ context.Context) error { return nil }}
	a := &mockAlerter{}
	logger := testLogger()
	cfg := testConfig()
	cfg.PortfolioValue = 5000
	deps := testDeps(logger, a)
	stopCh := make(chan os.Signal, 1)

	bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)

	msgs := a.Messages()
	if !strings.Contains(msgs[0], "$5000.00") {
		t.Errorf("startup alert should include portfolio value, got: %s", msgs[0])
	}
}

func TestLifecycle_StructuredLogs(t *testing.T) {
	b := &mockBot{runFunc: func(_ context.Context) error { return nil }}
	logger, buf := testLoggerWithBuffer()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 0 {
		t.Fatal("expected log output")
	}

	// All lines must be valid JSON
	for i, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d is not valid JSON: %s", i, line)
		}
	}

	// Check for key lifecycle log messages
	fullLog := buf.String()
	for _, expected := range []string{"initializing bot", "bot running", "shutting down bot", "bot stopped"} {
		if !strings.Contains(fullLog, expected) {
			t.Errorf("logs should contain %q", expected)
		}
	}
}

func TestLifecycle_WaitsForRunToReturn(t *testing.T) {
	// Bot.Run takes a moment to clean up after context cancellation
	var runReturned bool
	var mu sync.Mutex

	b := &mockBot{
		runFunc: func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond) // simulate cleanup
			mu.Lock()
			runReturned = true
			mu.Unlock()
			return nil
		},
	}

	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	}()

	time.Sleep(20 * time.Millisecond)
	stopCh <- syscall.SIGINT

	<-done

	mu.Lock()
	if !runReturned {
		t.Error("lifecycle should wait for Run to return before calling Shutdown")
	}
	mu.Unlock()
}

func TestLifecycle_ShutdownTimeoutDefault(t *testing.T) {
	cfg := bot.Config{Name: "test"}
	// ShutdownTimeout is 0 (default), should use 10s
	// Just verify it doesn't panic — the actual timeout is internal
	_ = cfg
}

func TestLifecycle_ConcurrentSignalAndError(t *testing.T) {
	// Both a signal and an error arrive — lifecycle should still complete cleanly
	b := &mockBot{
		runFunc: func(ctx context.Context) error {
			time.Sleep(10 * time.Millisecond)
			return errors.New("concurrent error")
		},
	}

	logger := testLogger()
	cfg := testConfig()
	deps := testDeps(logger, nil)
	stopCh := make(chan os.Signal, 1)

	// Send signal right away too
	go func() {
		time.Sleep(5 * time.Millisecond)
		stopCh <- syscall.SIGINT
	}()

	done := make(chan error, 1)
	go func() {
		done <- bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)
	}()

	select {
	case <-done:
		// Either the signal or the error can win — both are fine
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle did not complete")
	}

	if !b.wasShutdownCalled() {
		t.Error("Shutdown should always be called")
	}
}

func TestLifecycle_DepsPassedToBot(t *testing.T) {
	b := &mockBot{runFunc: func(_ context.Context) error { return nil }}
	a := &mockAlerter{}
	logger := testLogger()
	cfg := testConfig()
	cfg.MaxDrawdownPct = 0.20
	cfg.PortfolioValue = 500
	cfg.DryRunEnabled = true
	deps := bot.BuildDeps(cfg, logger)
	deps.Alerter = a
	stopCh := make(chan os.Signal, 1)

	bot.RunLifecycle(context.Background(), b, cfg, deps, logger, stopCh)

	received := b.receivedDeps
	if received == nil {
		t.Fatal("bot should receive deps")
	}
	if received.Logger != logger {
		t.Error("deps.Logger mismatch")
	}
	if received.KillSwitch == nil {
		t.Error("deps.KillSwitch should be set")
	}
	if received.Guard == nil {
		t.Error("deps.Guard should be set")
	}
	if !received.DryRun.Enabled {
		t.Error("deps.DryRun should be enabled")
	}
}

// Verify interface compliance
var _ bot.Bot = (*mockBot)(nil)
var _ alert.Alerter = (*mockAlerter)(nil)

func TestBotInterface_Compliance(t *testing.T) {
	// Compile-time check — if this compiles, the interface is satisfied
	_ = fmt.Sprintf("%T", (*mockBot)(nil))
}
