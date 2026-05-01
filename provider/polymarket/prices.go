package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

const clobBaseURL = "https://clob.polymarket.com"

// GetPriceHistory fetches historical price data for a token from the CLOB API.
// interval: "all", "1d", "1w", "1m" etc.
// fidelity: seconds between data points (60=1min, 3600=1hr, 86400=1day).
// Note: for resolved markets, fine-grained data may not be available.
// Use fidelity=3600 for hourly snapshots on resolved markets.
func (c *Client) GetPriceHistory(ctx context.Context, tokenID string, interval string, fidelity int) ([]PricePoint, error) {
	path := fmt.Sprintf("/prices-history?market=%s&interval=%s&fidelity=%d", tokenID, interval, fidelity)
	url := clobBaseURL + path

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("price history request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("price history HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		History []PricePoint `json:"history"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode price history: %w", err)
	}

	return result.History, nil
}

// GetLastPriceBeforeTimestamp returns the most recent price point before the given timestamp.
// Useful for getting pre-match odds. Returns nil if no data point exists before the timestamp.
func (c *Client) GetLastPriceBeforeTimestamp(ctx context.Context, tokenID string, beforeUnix int64) (*PricePoint, error) {
	history, err := c.GetPriceHistory(ctx, tokenID, "all", 3600)
	if err != nil {
		return nil, err
	}

	var best *PricePoint
	for i := range history {
		if history[i].T < beforeUnix {
			best = &history[i]
		}
	}

	return best, nil
}

// GetMidPriceForToken returns the current mid-market price using the CLOB midpoint endpoint.
// This is a convenience wrapper that returns just the float64 price.
func GetMidPriceForToken(ctx context.Context, httpClient *http.Client, tokenID string) (float64, error) {
	url := fmt.Sprintf("%s/midpoint?token_id=%s", clobBaseURL, tokenID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("midpoint request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("midpoint HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Mid string `json:"mid"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("decode midpoint: %w", err)
	}

	return strconv.ParseFloat(result.Mid, 64)
}
