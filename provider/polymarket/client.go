package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// Client is a REST client for the Polymarket CLOB API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	auth       *HMACAuth
	limiter    *rate.Limiter
	log        *slog.Logger

	maxRetries int
	baseDelay  time.Duration
}

func NewClient(baseURL, apiKey, secret, passphrase string, rateLimit int, maxRetries int, baseDelay time.Duration, logger *slog.Logger) *Client {
	return NewClientWithAddress(baseURL, "", apiKey, secret, passphrase, rateLimit, maxRetries, baseDelay, logger)
}

func NewClientWithAddress(baseURL, address, apiKey, secret, passphrase string, rateLimit int, maxRetries int, baseDelay time.Duration, logger *slog.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 5 * time.Second,
				DisableCompression:  true,
			},
		},
		auth:       NewHMACAuthWithAddress(address, apiKey, secret, passphrase),
		limiter:    rate.NewLimiter(rate.Limit(rateLimit), rateLimit),
		log:        logger,
		maxRetries: maxRetries,
		baseDelay:  baseDelay,
	}
}

// GetOrderBook fetches the order book for a given token ID.
func (c *Client) GetOrderBook(ctx context.Context, tokenID string) (*OrderBook, error) {
	path := fmt.Sprintf("/book?token_id=%s", tokenID)
	var book OrderBook
	if err := c.doGet(ctx, path, &book); err != nil {
		return nil, fmt.Errorf("get order book: %w", err)
	}
	book.AssetID = tokenID
	return &book, nil
}

// GetMarket fetches market info for a condition ID.
func (c *Client) GetMarket(ctx context.Context, conditionID string) (*Market, error) {
	path := fmt.Sprintf("/markets/%s", conditionID)
	var market Market
	if err := c.doGet(ctx, path, &market); err != nil {
		return nil, fmt.Errorf("get market: %w", err)
	}
	return &market, nil
}

// GetOpenOrders returns all open orders.
func (c *Client) GetOpenOrders(ctx context.Context) ([]OpenOrder, error) {
	path := "/orders?open=true"
	var orders []OpenOrder
	if err := c.doGetAuth(ctx, "GET", path, &orders); err != nil {
		return nil, fmt.Errorf("get open orders: %w", err)
	}
	return orders, nil
}

// GetOrder returns the current state of a specific order.
func (c *Client) GetOrder(ctx context.Context, orderID string) (*OpenOrder, error) {
	path := fmt.Sprintf("/order/%s", orderID)
	var order OpenOrder
	if err := c.doGetAuth(ctx, "GET", path, &order); err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	return &order, nil
}

// PlaceOrder places a new order on the CLOB.
func (c *Client) PlaceOrder(ctx context.Context, order *OrderRequest) (*OrderResponse, error) {
	path := "/order"
	body, err := json.Marshal(order)
	if err != nil {
		return nil, fmt.Errorf("marshal order: %w", err)
	}
	var resp OrderResponse
	if err := c.doPostAuth(ctx, path, body, &resp); err != nil {
		return nil, fmt.Errorf("place order: %w", err)
	}
	return &resp, nil
}

// CancelOrder cancels an existing order.
func (c *Client) CancelOrder(ctx context.Context, orderID string) error {
	path := fmt.Sprintf("/order/%s", orderID)
	return c.doDeleteAuth(ctx, path)
}

// CancelAllOrders cancels all open orders.
func (c *Client) CancelAllOrders(ctx context.Context) error {
	path := "/order/cancel-all"
	return c.doPostAuthNoBody(ctx, path)
}

// GetBalance returns the user's USDC (collateral) balance on Polymarket.
// signatureType: 0=EOA, 1=POLY_PROXY, 2=GNOSIS_SAFE
func (c *Client) GetBalance(ctx context.Context) (float64, error) {
	// Try signature types in order: Gnosis Safe (most common), then Proxy, then EOA
	for _, sigType := range []int{2, 1, 0} {
		path := fmt.Sprintf("/balance-allowance?asset_type=COLLATERAL&signature_type=%d", sigType)
		var resp struct {
			Balance string `json:"balance"`
		}
		if err := c.doGetAuth(ctx, "GET", path, &resp); err != nil {
			continue
		}
		raw, err := strconv.ParseFloat(resp.Balance, 64)
		if err != nil {
			continue
		}
		if raw > 0 {
			// USDC has 6 decimals
			return raw / 1_000_000, nil
		}
	}
	return 0, nil
}

// GetMidPrice returns the mid-market price for a token.
func (c *Client) GetMidPrice(ctx context.Context, tokenID string) (float64, error) {
	path := fmt.Sprintf("/midpoint?token_id=%s", tokenID)
	var resp struct {
		Mid string `json:"mid"`
	}
	if err := c.doGet(ctx, path, &resp); err != nil {
		return 0, fmt.Errorf("get midpoint: %w", err)
	}
	return strconv.ParseFloat(resp.Mid, 64)
}

// GetBestPrice returns the best bid/ask for a token.
func (c *Client) GetBestPrice(ctx context.Context, tokenID string) (bid, ask float64, err error) {
	path := fmt.Sprintf("/price?token_id=%s&side=BUY", tokenID)
	var bidResp struct {
		Price string `json:"price"`
	}
	if err := c.doGet(ctx, path, &bidResp); err != nil {
		return 0, 0, fmt.Errorf("get bid price: %w", err)
	}

	path = fmt.Sprintf("/price?token_id=%s&side=SELL", tokenID)
	var askResp struct {
		Price string `json:"price"`
	}
	if err := c.doGet(ctx, path, &askResp); err != nil {
		return 0, 0, fmt.Errorf("get ask price: %w", err)
	}

	bid, _ = strconv.ParseFloat(bidResp.Price, 64)
	ask, _ = strconv.ParseFloat(askResp.Price, 64)
	return bid, ask, nil
}

// --- HTTP helpers with retry and rate limiting ---

func (c *Client) doGet(ctx context.Context, path string, out interface{}) error {
	return c.withRetry(ctx, func() error {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limit: %w", err)
		}
		url := c.baseURL + path
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}
		return c.executeAndDecode(req, out)
	})
}

func (c *Client) doGetAuth(ctx context.Context, method, path string, out interface{}) error {
	return c.withRetry(ctx, func() error {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limit: %w", err)
		}
		url := c.baseURL + path
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return err
		}
		headers := c.auth.Headers(method, path, "")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return c.executeAndDecode(req, out)
	})
}

func (c *Client) doPostAuth(ctx context.Context, path string, body []byte, out interface{}) error {
	return c.withRetry(ctx, func() error {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limit: %w", err)
		}
		url := c.baseURL + path
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		headers := c.auth.Headers("POST", path, string(body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return c.executeAndDecode(req, out)
	})
}

func (c *Client) doDeleteAuth(ctx context.Context, path string) error {
	return c.withRetry(ctx, func() error {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limit: %w", err)
		}
		url := c.baseURL + path
		req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
		if err != nil {
			return err
		}
		headers := c.auth.Headers("DELETE", path, "")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
		return nil
	})
}

func (c *Client) doPostAuthNoBody(ctx context.Context, path string) error {
	return c.withRetry(ctx, func() error {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limit: %w", err)
		}
		url := c.baseURL + path
		req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
		if err != nil {
			return err
		}
		headers := c.auth.Headers("POST", path, "")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
		return nil
	})
}

func (c *Client) executeAndDecode(req *http.Request, out interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode == 429 {
		return &RateLimitError{RetryAfter: 2 * time.Second}
	}
	if resp.StatusCode == 404 {
		// 404s are not retryable (e.g., no orderbook exists).
		return &PermanentError{Code: 404, Message: string(body)}
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode response: %w (body: %s)", err, string(body))
		}
	}
	return nil
}

func (c *Client) withRetry(ctx context.Context, fn func() error) error {
	var lastErr error
	delay := c.baseDelay

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			c.log.Debug("retrying request", "attempt", attempt, "delay", delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2 // exponential backoff
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Don't retry permanent errors (404s, etc.).
		if _, ok := lastErr.(*PermanentError); ok {
			return lastErr
		}

		// If rate limited, wait the specified time.
		if rlErr, ok := lastErr.(*RateLimitError); ok {
			delay = rlErr.RetryAfter
			continue
		}

		c.log.Warn("request failed", "attempt", attempt, "error", lastErr)
	}
	return fmt.Errorf("all %d retries exhausted: %w", c.maxRetries, lastErr)
}

// RateLimitError indicates a 429 response.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited, retry after %s", e.RetryAfter)
}

// PermanentError indicates a non-retryable error (e.g., 404).
type PermanentError struct {
	Code    int
	Message string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Message)
}
