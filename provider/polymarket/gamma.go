package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// GammaClient provides access to the Polymarket Gamma API for event/market discovery.
type GammaClient struct {
	baseURL    string
	httpClient *http.Client
	log        *slog.Logger
}

// NewGammaClient creates a new Gamma API client.
func NewGammaClient(logger *slog.Logger) *GammaClient {
	return &GammaClient{
		baseURL: gammaAPIURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		log: logger,
	}
}

// GetEvents fetches events from the Gamma API with the given query filters.
func (gc *GammaClient) GetEvents(ctx context.Context, q EventQuery) ([]GammaEvent, error) {
	params := url.Values{}
	if q.TagID > 0 {
		params.Set("tag_id", strconv.Itoa(q.TagID))
	}
	if q.Slug != "" {
		params.Set("slug", q.Slug)
	}
	if q.Closed != nil {
		params.Set("closed", strconv.FormatBool(*q.Closed))
	}
	if q.Active != nil {
		params.Set("active", strconv.FormatBool(*q.Active))
	}
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		params.Set("offset", strconv.Itoa(q.Offset))
	}
	if q.Order != "" {
		params.Set("order", q.Order)
		if q.Ascending {
			params.Set("ascending", "true")
		} else {
			params.Set("ascending", "false")
		}
	}

	reqURL := fmt.Sprintf("%s/events?%s", gc.baseURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := gc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gamma request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gamma HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var events []GammaEvent
	if err := json.Unmarshal(body, &events); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}

	return events, nil
}

// GetEventBySlug fetches a single event by its slug.
func (gc *GammaClient) GetEventBySlug(ctx context.Context, slug string) (*GammaEvent, error) {
	events, err := gc.GetEvents(ctx, EventQuery{Slug: slug, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no event found for slug %q", slug)
	}
	return &events[0], nil
}

// GetEventsByTag fetches events by tag ID with pagination.
func (gc *GammaClient) GetEventsByTag(ctx context.Context, tagID int, closed bool, limit, offset int) ([]GammaEvent, error) {
	c := closed
	return gc.GetEvents(ctx, EventQuery{
		TagID:  tagID,
		Closed: &c,
		Limit:  limit,
		Offset: offset,
	})
}

// GetAllEventsByTag fetches all events for a tag, handling pagination automatically.
func (gc *GammaClient) GetAllEventsByTag(ctx context.Context, tagID int) ([]GammaEvent, error) {
	var all []GammaEvent
	limit := 50
	offset := 0

	for {
		events, err := gc.GetEvents(ctx, EventQuery{
			TagID:  tagID,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("fetch page offset=%d: %w", offset, err)
		}
		all = append(all, events...)
		if len(events) < limit {
			break
		}
		offset += limit
	}

	gc.log.Info("fetched all events by tag", "tag_id", tagID, "count", len(all))
	return all, nil
}

// ParseTokenIDs extracts the CLOB token IDs from a GammaMarket's JSON string field.
func ParseTokenIDs(m *GammaMarket) ([]string, error) {
	var ids []string
	if err := json.Unmarshal([]byte(m.ClobTokenIDs), &ids); err != nil {
		return nil, fmt.Errorf("parse clobTokenIds: %w", err)
	}
	return ids, nil
}

// ParseOutcomes extracts the outcome labels from a GammaMarket's JSON string field.
func ParseOutcomes(m *GammaMarket) ([]string, error) {
	var outcomes []string
	if err := json.Unmarshal([]byte(m.Outcomes), &outcomes); err != nil {
		return nil, fmt.Errorf("parse outcomes: %w", err)
	}
	return outcomes, nil
}

// ParseOutcomePrices extracts the outcome prices from a GammaMarket's JSON string field.
func ParseOutcomePrices(m *GammaMarket) ([]float64, error) {
	var raw []string
	if err := json.Unmarshal([]byte(m.OutcomePrices), &raw); err != nil {
		return nil, fmt.Errorf("parse outcomePrices: %w", err)
	}
	prices := make([]float64, len(raw))
	for i, s := range raw {
		p, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("parse price %q: %w", s, err)
		}
		prices[i] = p
	}
	return prices, nil
}
