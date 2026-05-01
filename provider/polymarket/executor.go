package polymarket

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yun-jay/botagent/order"
)

// ExecutorOption configures the PolymarketExecutor.
type ExecutorOption func(*Executor)

// WithFOK sets the executor to use Fill-or-Kill orders.
func WithFOK() ExecutorOption {
	return func(e *Executor) { e.orderType = "FOK" }
}

// WithGTC sets the executor to use Good-Till-Cancel orders.
func WithGTC() ExecutorOption {
	return func(e *Executor) { e.orderType = "GTC" }
}

// WithLimitFallback posts a GTC limit order and falls back to FOK after timeout.
func WithLimitFallback(timeout time.Duration) ExecutorOption {
	return func(e *Executor) {
		e.orderType = "LIMIT_FALLBACK"
		e.limitTimeout = timeout
	}
}

// WithNegRisk marks all orders as neg-risk (required for some markets like Bundesliga).
func WithNegRisk(enabled bool) ExecutorOption {
	return func(e *Executor) { e.negRisk = enabled }
}

// WithSpreadTolerance sets the spread tolerance for FOK pricing.
// The execution price is adjusted by spread * tolerance (e.g., 0.25 = 25% of spread).
func WithSpreadTolerance(tolerance float64) ExecutorOption {
	return func(e *Executor) { e.spreadTolerance = tolerance }
}

// WithBuilderCode attaches a Polymarket builder code (0x-prefixed bytes32 hex)
// to every order placed by this executor. See the Polymarket Builder Program.
func WithBuilderCode(code string) ExecutorOption {
	return func(e *Executor) { e.builderCode = code }
}

// Executor implements order.Executor for Polymarket.
// Supports FOK, GTC, and limit-with-fallback order modes.
type Executor struct {
	client          *Client
	logger          *slog.Logger
	orderType       string // "FOK", "GTC", "LIMIT_FALLBACK"
	limitTimeout    time.Duration
	negRisk         bool
	spreadTolerance float64
	builderCode     string
}

// NewExecutor creates a Polymarket executor.
func NewExecutor(client *Client, logger *slog.Logger, opts ...ExecutorOption) *Executor {
	e := &Executor{
		client:          client,
		logger:          logger,
		orderType:       "FOK",
		spreadTolerance: 0.25,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Execute places an order on Polymarket.
func (e *Executor) Execute(ctx context.Context, req order.Request) (order.Result, error) {
	if e.client == nil {
		return order.Result{Status: "failed"}, fmt.Errorf("polymarket client not configured")
	}

	switch e.orderType {
	case "GTC":
		return e.executeGTC(ctx, req)
	case "LIMIT_FALLBACK":
		return e.executeLimitFallback(ctx, req)
	default: // FOK
		return e.executeFOK(ctx, req)
	}
}

// CancelAll cancels all open orders.
func (e *Executor) CancelAll(ctx context.Context) error {
	if e.client == nil {
		return nil
	}
	return e.client.CancelAllOrders(ctx)
}

func (e *Executor) executeFOK(ctx context.Context, req order.Request) (order.Result, error) {
	pmOrder := &OrderRequest{
		TokenID:   req.Instrument,
		Price:     req.Price,
		Size:      req.Size,
		Side:      OrderSide(req.Side),
		OrderType: FOKOrder,
	}

	if e.negRisk {
		pmOrder.NegRisk = true
	}
	if e.builderCode != "" {
		pmOrder.BuilderCode = e.builderCode
	}

	resp, err := e.client.PlaceOrder(ctx, pmOrder)
	if err != nil {
		return order.Result{Status: "failed"}, fmt.Errorf("FOK order failed: %w", err)
	}

	e.logger.Info("FOK order placed",
		"order_id", resp.OrderID,
		"status", resp.Status,
		"instrument", req.Instrument,
		"side", req.Side,
		"price", req.Price,
		"size", req.Size,
	)

	return order.Result{
		OrderID:   resp.OrderID,
		Status:    resp.Status,
		FillPrice: req.Price,
	}, nil
}

func (e *Executor) executeGTC(ctx context.Context, req order.Request) (order.Result, error) {
	pmOrder := &OrderRequest{
		TokenID:   req.Instrument,
		Price:     req.Price,
		Size:      req.Size,
		Side:      OrderSide(req.Side),
		OrderType: LimitOrder,
	}

	if e.negRisk {
		pmOrder.NegRisk = true
	}
	if e.builderCode != "" {
		pmOrder.BuilderCode = e.builderCode
	}

	resp, err := e.client.PlaceOrder(ctx, pmOrder)
	if err != nil {
		return order.Result{Status: "failed"}, fmt.Errorf("GTC order failed: %w", err)
	}

	e.logger.Info("GTC order placed",
		"order_id", resp.OrderID,
		"status", resp.Status,
		"instrument", req.Instrument,
		"side", req.Side,
		"price", req.Price,
		"size", req.Size,
	)

	return order.Result{
		OrderID:   resp.OrderID,
		Status:    resp.Status,
		FillPrice: req.Price,
	}, nil
}

func (e *Executor) executeLimitFallback(ctx context.Context, req order.Request) (order.Result, error) {
	// Step 1: Place GTC limit order at the requested price.
	pmOrder := &OrderRequest{
		TokenID:   req.Instrument,
		Price:     req.Price,
		Size:      req.Size,
		Side:      OrderSide(req.Side),
		OrderType: LimitOrder,
	}

	if e.negRisk {
		pmOrder.NegRisk = true
	}
	if e.builderCode != "" {
		pmOrder.BuilderCode = e.builderCode
	}

	resp, err := e.client.PlaceOrder(ctx, pmOrder)
	if err != nil {
		return order.Result{Status: "failed"}, fmt.Errorf("limit order failed: %w", err)
	}

	if resp.Status == "MATCHED" || resp.Status == "FILLED" {
		e.logger.Info("limit order filled immediately",
			"order_id", resp.OrderID,
			"instrument", req.Instrument,
		)
		return order.Result{
			OrderID:   resp.OrderID,
			Status:    "filled",
			FillPrice: req.Price,
		}, nil
	}

	// Step 2: Poll for fill up to timeout.
	deadline := time.After(e.limitTimeout)
	pollTicker := time.NewTicker(500 * time.Millisecond)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.client.CancelOrder(ctx, resp.OrderID)
			return order.Result{Status: "cancelled"}, ctx.Err()

		case <-deadline:
			// Timeout: cancel limit order and fall back to FOK.
			e.client.CancelOrder(ctx, resp.OrderID)
			e.logger.Info("limit order timed out, falling back to FOK",
				"order_id", resp.OrderID,
				"timeout", e.limitTimeout,
			)
			return e.executeFOK(ctx, req)

		case <-pollTicker.C:
			status, err := e.client.GetOrder(ctx, resp.OrderID)
			if err != nil {
				continue
			}
			if status.Status == "MATCHED" || status.Status == "FILLED" {
				e.logger.Info("limit order filled",
					"order_id", resp.OrderID,
					"instrument", req.Instrument,
				)
				return order.Result{
					OrderID:   resp.OrderID,
					Status:    "filled",
					FillPrice: req.Price,
				}, nil
			}
		}
	}
}
