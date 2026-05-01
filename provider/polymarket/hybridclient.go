package polymarket

import (
	"context"
	"log/slog"
)

// HybridCLOBClient satisfies the engine.CLOBClient interface by reading from
// the WebSocket feed cache first, falling back to REST on cache miss.
type HybridCLOBClient struct {
	wsFeed     *PolymarketFeed
	restClient *Client
	log        *slog.Logger
}

// NewHybridCLOBClient creates a hybrid client that prefers WebSocket data.
func NewHybridCLOBClient(wsFeed *PolymarketFeed, restClient *Client, logger *slog.Logger) *HybridCLOBClient {
	return &HybridCLOBClient{
		wsFeed:     wsFeed,
		restClient: restClient,
		log:        logger,
	}
}

// GetOrderBook returns the order book from WS cache, falling back to REST.
func (h *HybridCLOBClient) GetOrderBook(ctx context.Context, tokenID string) (*OrderBook, error) {
	if book, ok := h.wsFeed.GetOrderBook(tokenID); ok {
		return book, nil
	}
	h.log.Debug("ws cache miss for order book, REST fallback", "token_id", tokenID)
	return h.restClient.GetOrderBook(ctx, tokenID)
}

// GetMidPrice returns the mid price from WS cache, falling back to REST.
func (h *HybridCLOBClient) GetMidPrice(ctx context.Context, tokenID string) (float64, error) {
	if bid, ask, ok := h.wsFeed.GetBestBidAsk(tokenID); ok {
		return (bid + ask) / 2, nil
	}
	h.log.Debug("ws cache miss for mid price, REST fallback", "token_id", tokenID)
	return h.restClient.GetMidPrice(ctx, tokenID)
}
