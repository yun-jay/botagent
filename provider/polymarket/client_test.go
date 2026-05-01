package polymarket

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func newTestClient(handler http.Handler) *Client {
	server := httptest.NewServer(handler)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewClient(server.URL, "key", "secret", "pass", 100, 1, 100*time.Millisecond, logger)
}

func TestClient_GetMidPrice(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"mid": "0.55"})
	})
	client := newTestClient(handler)

	mid, err := client.GetMidPrice(context.Background(), "token123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 0.55 {
		t.Fatalf("expected 0.55, got %f", mid)
	}
}

func TestClient_GetOrderBook(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		book := OrderBook{
			Bids:      []PriceLevel{{Price: "0.50", Size: "100"}},
			Asks:      []PriceLevel{{Price: "0.55", Size: "200"}},
			Timestamp: "1774741588000",
		}
		json.NewEncoder(w).Encode(book)
	})
	client := newTestClient(handler)

	book, err := client.GetOrderBook(context.Background(), "token123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(book.Bids) != 1 {
		t.Fatalf("expected 1 bid, got %d", len(book.Bids))
	}
	if len(book.Asks) != 1 {
		t.Fatalf("expected 1 ask, got %d", len(book.Asks))
	}
	if book.AssetID != "token123" {
		t.Fatalf("expected asset_id token123, got %s", book.AssetID)
	}
}

func TestClient_404ReturnsImmediately(t *testing.T) {
	attempts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"not found"}`))
	})
	client := newTestClient(handler)

	_, err := client.GetMidPrice(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt (no retry for 404), got %d", attempts)
	}
}

func TestClient_429TriggersRetry(t *testing.T) {
	attempts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(429)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"mid": "0.60"})
	})
	client := newTestClient(handler)

	mid, err := client.GetMidPrice(context.Background(), "token123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mid != 0.60 {
		t.Fatalf("expected 0.60, got %f", mid)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts (1 retry after 429), got %d", attempts)
	}
}

func TestClient_500RetriesThenFails(t *testing.T) {
	attempts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(500)
		w.Write([]byte("server error"))
	})
	client := newTestClient(handler)

	_, err := client.GetMidPrice(context.Background(), "token123")
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if attempts != 2 { // initial + 1 retry (maxRetries=1)
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestClient_GetBestPrice(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		side := r.URL.Query().Get("side")
		if side == "BUY" {
			json.NewEncoder(w).Encode(map[string]string{"price": "0.50"})
		} else {
			json.NewEncoder(w).Encode(map[string]string{"price": "0.55"})
		}
	})
	client := newTestClient(handler)

	bid, ask, err := client.GetBestPrice(context.Background(), "token123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bid != 0.50 {
		t.Fatalf("expected bid 0.50, got %f", bid)
	}
	if ask != 0.55 {
		t.Fatalf("expected ask 0.55, got %f", ask)
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // slow server
	})
	client := newTestClient(handler)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.GetMidPrice(ctx, "token123")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestClient_GetMidPrice_MalformedJSON(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{invalid`))
	})
	client := newTestClient(handler)

	_, err := client.GetMidPrice(context.Background(), "token123")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestClient_GetOrderBook_EmptyBook(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		book := OrderBook{
			Bids:      []PriceLevel{},
			Asks:      []PriceLevel{},
			Timestamp: "1774741588000",
		}
		json.NewEncoder(w).Encode(book)
	})
	client := newTestClient(handler)

	book, err := client.GetOrderBook(context.Background(), "token123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(book.Bids) != 0 {
		t.Fatalf("expected 0 bids, got %d", len(book.Bids))
	}
	if len(book.Asks) != 0 {
		t.Fatalf("expected 0 asks, got %d", len(book.Asks))
	}
}

func TestClient_PlaceOrder(t *testing.T) {
	var receivedBody map[string]interface{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/order" {
			t.Errorf("expected path /order, got %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		json.NewEncoder(w).Encode(OrderResponse{
			OrderID: "abc",
			Status:  "matched",
		})
	})
	client := newTestClient(handler)

	order := &OrderRequest{
		TokenID:   "tok123",
		Price:     0.55,
		Size:      100,
		Side:      Buy,
		OrderType: LimitOrder,
	}
	resp, err := client.PlaceOrder(context.Background(), order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.OrderID != "abc" {
		t.Fatalf("expected orderID abc, got %s", resp.OrderID)
	}
	if resp.Status != "matched" {
		t.Fatalf("expected status matched, got %s", resp.Status)
	}

	// Verify request body contained the correct fields.
	if receivedBody["tokenID"] != "tok123" {
		t.Errorf("expected tokenID tok123, got %v", receivedBody["tokenID"])
	}
	if receivedBody["price"] != 0.55 {
		t.Errorf("expected price 0.55, got %v", receivedBody["price"])
	}
	if receivedBody["size"] != float64(100) {
		t.Errorf("expected size 100, got %v", receivedBody["size"])
	}
	if receivedBody["side"] != "BUY" {
		t.Errorf("expected side BUY, got %v", receivedBody["side"])
	}
	if receivedBody["type"] != "GTC" {
		t.Errorf("expected type GTC, got %v", receivedBody["type"])
	}
}

func TestClient_CancelOrder(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/order/order-abc-123" {
			t.Errorf("expected path /order/order-abc-123, got %s", r.URL.Path)
		}
		w.WriteHeader(200)
	})
	client := newTestClient(handler)

	err := client.CancelOrder(context.Background(), "order-abc-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_CancelAllOrders(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/order/cancel-all" {
			t.Errorf("expected path /order/cancel-all, got %s", r.URL.Path)
		}
		called = true
		w.WriteHeader(200)
	})
	client := newTestClient(handler)

	err := client.CancelAllOrders(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("cancel-all endpoint was not called")
	}
}

func TestClient_GetMarket(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/markets/cond-xyz" {
			t.Errorf("expected path /markets/cond-xyz, got %s", r.URL.Path)
		}
		market := Market{
			ConditionID: "cond-xyz",
			Question:    "Will BTC go up in 5m?",
			Active:      true,
			Closed:      false,
			Tokens: []MarketToken{
				{TokenID: "tok-up", Outcome: "Up", Price: 0.55},
				{TokenID: "tok-down", Outcome: "Down", Price: 0.45},
			},
		}
		json.NewEncoder(w).Encode(market)
	})
	client := newTestClient(handler)

	market, err := client.GetMarket(context.Background(), "cond-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if market.ConditionID != "cond-xyz" {
		t.Errorf("expected ConditionID cond-xyz, got %s", market.ConditionID)
	}
	if market.Question != "Will BTC go up in 5m?" {
		t.Errorf("expected Question 'Will BTC go up in 5m?', got %s", market.Question)
	}
	if !market.Active {
		t.Error("expected Active=true")
	}
	if market.Closed {
		t.Error("expected Closed=false")
	}
	if len(market.Tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(market.Tokens))
	}
	if market.Tokens[0].TokenID != "tok-up" {
		t.Errorf("expected first token ID tok-up, got %s", market.Tokens[0].TokenID)
	}
	if market.Tokens[1].Outcome != "Down" {
		t.Errorf("expected second token outcome Down, got %s", market.Tokens[1].Outcome)
	}
}

func TestClient_GetBestPrice_EmptyBook(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return empty price (no bids or asks available).
		json.NewEncoder(w).Encode(map[string]string{"price": ""})
	})
	client := newTestClient(handler)

	bid, ask, err := client.GetBestPrice(context.Background(), "token123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty string parsed by ParseFloat returns 0.
	if bid != 0 {
		t.Errorf("expected bid 0, got %f", bid)
	}
	if ask != 0 {
		t.Errorf("expected ask 0, got %f", ask)
	}
}

func TestClient_ContextCancellation_Immediate(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"mid": "0.55"})
	})
	client := newTestClient(handler)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before calling

	start := time.Now()
	_, err := client.GetMidPrice(ctx, "token123")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected quick return, took %v", elapsed)
	}
}
