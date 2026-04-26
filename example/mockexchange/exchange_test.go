package mockexchange_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/yun-jay/botagent/example/mockexchange"
	"github.com/yun-jay/botagent/order"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

func TestExchange_BuyFill(t *testing.T) {
	ex := mockexchange.New(testLogger())
	ex.AddMarket("TOKEN-A", 0.60, 0.58, 0.62)

	result, err := ex.Execute(context.Background(), order.Request{
		Side:       "BUY",
		Instrument: "TOKEN-A",
		Price:      0.62,
		Size:       100,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != "filled" {
		t.Errorf("status = %q, want filled", result.Status)
	}
	if result.FillPrice != 0.62 {
		t.Errorf("fill price = %v, want 0.62", result.FillPrice)
	}
	if result.OrderID == "" {
		t.Error("orderID should not be empty")
	}

	orders := ex.FilledOrders()
	if len(orders) != 1 {
		t.Fatalf("len = %d, want 1", len(orders))
	}
}

func TestExchange_SellFill(t *testing.T) {
	ex := mockexchange.New(testLogger())
	ex.AddMarket("TOKEN-A", 0.60, 0.58, 0.62)

	result, err := ex.Execute(context.Background(), order.Request{
		Side:       "SELL",
		Instrument: "TOKEN-A",
		Price:      0.58,
		Size:       50,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.FillPrice != 0.58 {
		t.Errorf("fill price = %v, want 0.58", result.FillPrice)
	}
}

func TestExchange_UnknownInstrument(t *testing.T) {
	ex := mockexchange.New(testLogger())
	_, err := ex.Execute(context.Background(), order.Request{
		Side:       "BUY",
		Instrument: "UNKNOWN",
		Size:       100,
	})
	if err == nil {
		t.Error("should error on unknown instrument")
	}
}

func TestExchange_FailNext(t *testing.T) {
	ex := mockexchange.New(testLogger())
	ex.AddMarket("TOKEN-A", 0.60, 0.58, 0.62)
	ex.SetFailNext(true)

	_, err := ex.Execute(context.Background(), order.Request{
		Side:       "BUY",
		Instrument: "TOKEN-A",
		Size:       100,
	})
	if err == nil {
		t.Error("should error when failNext is set")
	}

	// Next call should succeed
	result, err := ex.Execute(context.Background(), order.Request{
		Side:       "BUY",
		Instrument: "TOKEN-A",
		Size:       100,
	})
	if err != nil {
		t.Fatalf("second call should succeed: %v", err)
	}
	if result.Status != "filled" {
		t.Errorf("status = %q, want filled", result.Status)
	}
}

func TestExchange_CancelAll(t *testing.T) {
	ex := mockexchange.New(testLogger())
	err := ex.CancelAll(context.Background())
	if err != nil {
		t.Errorf("CancelAll error: %v", err)
	}
}

func TestExchange_MarketOrderBuy(t *testing.T) {
	ex := mockexchange.New(testLogger())
	ex.AddMarket("TOKEN-A", 0.60, 0.58, 0.62)

	// Price=0 means market order
	result, err := ex.Execute(context.Background(), order.Request{
		Side:       "BUY",
		Instrument: "TOKEN-A",
		Price:      0,
		Size:       100,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.FillPrice != 0.62 {
		t.Errorf("fill price = %v, want 0.62 (ask)", result.FillPrice)
	}
}
