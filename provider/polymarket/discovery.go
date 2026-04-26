package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const gammaAPIURL = "https://gamma-api.polymarket.com"

// ContractDiscovery auto-discovers active up/down contracts for configured assets.
type ContractDiscovery struct {
	client    *http.Client
	log       *slog.Logger
	MinVolume float64  // minimum market volume in USDC to include
	baseURL   string   // Gamma API base URL (defaults to gammaAPIURL)
	assets    []string // tradeable assets (lowercase, e.g., "btc", "eth", "sol")
}

// DiscoveredContract holds the parsed data for a live short-term market.
type DiscoveredContract struct {
	Question    string
	ConditionID string
	TokenIDUp   string // "Yes" / "Up" outcome
	TokenIDDown string // "No" / "Down" outcome
	Asset       string // "BTC" or "ETH"
	Timeframe   string // "5m" or "15m"
	EndDate     string
	Active      bool
	Closed      bool
	Volume      float64 // market volume in USDC
	RefPrice    float64 // Binance price at contract window start (set externally)
}

type gammaEvent struct {
	Slug    string        `json:"slug"`
	Title   string        `json:"title"`
	Active  bool          `json:"active"`
	Closed  bool          `json:"closed"`
	Markets []gammaMarket `json:"markets"`
}

type gammaMarket struct {
	Question        string  `json:"question"`
	ConditionID     string  `json:"conditionId"`
	ClobTokenIDs    string  `json:"clobTokenIds"` // JSON array as string
	Outcomes        string  `json:"outcomes"`      // JSON array as string
	Active          bool    `json:"active"`
	Closed          bool    `json:"closed"`
	EndDate         string  `json:"endDate"`
	AcceptingOrders bool    `json:"acceptingOrders"`
	Volume          float64 `json:"volumeNum"`
}

func NewContractDiscovery(logger *slog.Logger, minVolume float64, assets []string) *ContractDiscovery {
	if len(assets) == 0 {
		assets = []string{"btc", "eth"}
	}
	return &ContractDiscovery{
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 5 * time.Second,
				DisableCompression:  true,
			},
		},
		log:       logger,
		MinVolume: minVolume,
		baseURL:   gammaAPIURL,
		assets:    assets,
	}
}

// DiscoverActive finds the currently active BTC/ETH 5m and 15m up/down contracts.
// It generates slugs based on the current time window and fetches them in parallel.
func (cd *ContractDiscovery) DiscoverActive(ctx context.Context) ([]DiscoveredContract, error) {
	now := time.Now().UTC()

	// Generate slug timestamps for current and next windows.
	// 5m contracts: rounded to nearest 5-minute boundary.
	// 15m contracts: rounded to nearest 15-minute boundary.
	timestamps5m := cd.generateTimestamps(now, 5*time.Minute, 3)
	timestamps15m := cd.generateTimestamps(now, 15*time.Minute, 2)

	type fetchReq struct {
		slug      string
		asset     string
		timeframe string
	}

	var reqs []fetchReq
	for _, asset := range cd.assets {
		for _, ts := range timestamps5m {
			reqs = append(reqs, fetchReq{
				slug:      fmt.Sprintf("%s-updown-5m-%d", asset, ts),
				asset:     assetUpper(asset),
				timeframe: "5m",
			})
		}
		for _, ts := range timestamps15m {
			reqs = append(reqs, fetchReq{
				slug:      fmt.Sprintf("%s-updown-15m-%d", asset, ts),
				asset:     assetUpper(asset),
				timeframe: "15m",
			})
		}
	}

	type fetchResult struct {
		contract *DiscoveredContract
		err      error
		slug     string
	}

	results := make(chan fetchResult, len(reqs))
	for _, r := range reqs {
		go func(r fetchReq) {
			found, err := cd.fetchEvent(ctx, r.slug, r.asset, r.timeframe)
			results <- fetchResult{contract: found, err: err, slug: r.slug}
		}(r)
	}

	var contracts []DiscoveredContract
	for range reqs {
		res := <-results
		if res.err != nil {
			cd.log.Debug("discovery miss", "slug", res.slug, "error", res.err)
			continue
		}
		if res.contract != nil && res.contract.Active && !res.contract.Closed {
			contracts = append(contracts, *res.contract)
		}
	}

	cd.log.Info("contract discovery complete", "found", len(contracts))
	for _, c := range contracts {
		cd.log.Info("discovered contract",
			"asset", c.Asset,
			"timeframe", c.Timeframe,
			"condition_id", c.ConditionID,
			"question", c.Question,
		)
	}

	return contracts, nil
}

func (cd *ContractDiscovery) fetchEvent(ctx context.Context, slug, asset, timeframe string) (*DiscoveredContract, error) {
	url := fmt.Sprintf("%s/events?slug=%s", cd.baseURL, slug)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := cd.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d for slug %s", resp.StatusCode, slug)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var events []gammaEvent
	if err := json.Unmarshal(body, &events); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}

	if len(events) == 0 || len(events[0].Markets) == 0 {
		return nil, fmt.Errorf("no markets found for slug %s", slug)
	}

	m := events[0].Markets[0]

	// Skip markets that are closed or not accepting orders.
	if m.Closed || !m.AcceptingOrders {
		return nil, fmt.Errorf("market %s not accepting orders (closed=%v, accepting=%v)", slug, m.Closed, m.AcceptingOrders)
	}

	// Skip markets whose end date has already passed.
	if m.EndDate != "" {
		endTime, err := time.Parse(time.RFC3339, m.EndDate)
		if err == nil && time.Now().After(endTime) {
			return nil, fmt.Errorf("market %s already ended at %s", slug, m.EndDate)
		}
	}

	// Skip markets below the minimum volume threshold.
	if cd.MinVolume > 0 && m.Volume < cd.MinVolume {
		return nil, fmt.Errorf("market %s volume %.0f below minimum %.0f", slug, m.Volume, cd.MinVolume)
	}

	// Parse clobTokenIds.
	var tokenIDs []string
	if err := json.Unmarshal([]byte(m.ClobTokenIDs), &tokenIDs); err != nil {
		return nil, fmt.Errorf("parse token IDs: %w", err)
	}
	if len(tokenIDs) < 2 {
		return nil, fmt.Errorf("expected 2 token IDs, got %d", len(tokenIDs))
	}

	return &DiscoveredContract{
		Question:    m.Question,
		ConditionID: m.ConditionID,
		TokenIDUp:   tokenIDs[0], // First token is "Up"
		TokenIDDown: tokenIDs[1], // Second token is "Down"
		Asset:       asset,
		Timeframe:   timeframe,
		EndDate:     m.EndDate,
		Active:      m.Active,
		Closed:      m.Closed,
		Volume:      m.Volume,
	}, nil
}

// generateTimestamps generates Unix timestamps for time window boundaries.
func (cd *ContractDiscovery) generateTimestamps(now time.Time, interval time.Duration, count int) []int64 {
	// Round down to the nearest interval.
	intervalSec := int64(interval.Seconds())
	nowUnix := now.Unix()
	base := nowUnix - (nowUnix % intervalSec)

	var timestamps []int64
	// Include previous, current, and next windows.
	for i := -1; i < count; i++ {
		timestamps = append(timestamps, base+int64(i)*intervalSec)
	}
	return timestamps
}

func assetUpper(asset string) string {
	switch asset {
	case "btc":
		return "BTC"
	case "eth":
		return "ETH"
	default:
		return asset
	}
}
