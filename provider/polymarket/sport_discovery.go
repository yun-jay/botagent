package polymarket

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// SportDiscovery periodically polls the Gamma API for active sport events
// by tag ID and tracks their token IDs for WS subscription.
type SportDiscovery struct {
	gamma    *GammaClient
	log      *slog.Logger
	tagIDs   []int
	interval time.Duration

	mu       sync.RWMutex
	known    map[string]SportMarket // conditionID -> market
	tokenIDs map[string]bool        // all known token IDs

	onNew func(markets []SportMarket)
	done  chan struct{}
}

// SportMarket holds a discovered sport market's key fields.
type SportMarket struct {
	ConditionID string
	Question    string
	Slug        string
	TokenIDs    []string
	Outcomes    []string
	Tags        []string
	EndDate     string
	Active      bool
}

// NewSportDiscovery creates a sport market discoverer.
// tagIDs are Gamma API tag IDs (e.g., 100350 for Soccer, 1494 for Bundesliga).
// interval controls how often the Gamma API is polled.
func NewSportDiscovery(gamma *GammaClient, tagIDs []int, interval time.Duration, logger *slog.Logger) *SportDiscovery {
	return &SportDiscovery{
		gamma:    gamma,
		log:      logger,
		tagIDs:   tagIDs,
		interval: interval,
		known:    make(map[string]SportMarket),
		tokenIDs: make(map[string]bool),
		done:     make(chan struct{}),
	}
}

// OnNew registers a callback invoked with newly discovered markets (not previously seen).
func (sd *SportDiscovery) OnNew(fn func(markets []SportMarket)) {
	sd.onNew = fn
}

// KnownTokenIDs returns all token IDs currently tracked.
func (sd *SportDiscovery) KnownTokenIDs() []string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	ids := make([]string, 0, len(sd.tokenIDs))
	for id := range sd.tokenIDs {
		ids = append(ids, id)
	}
	return ids
}

// Start performs an initial fetch and starts the background poller.
func (sd *SportDiscovery) Start(ctx context.Context) error {
	if err := sd.poll(ctx); err != nil {
		sd.log.Warn("initial sport discovery failed, will retry", "error", err)
	}
	go sd.worker(ctx)
	return nil
}

// Stop halts the background poller.
func (sd *SportDiscovery) Stop() {
	close(sd.done)
}

func (sd *SportDiscovery) worker(ctx context.Context) {
	timer := time.NewTimer(sd.interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sd.done:
			return
		case <-timer.C:
			if err := sd.poll(ctx); err != nil {
				sd.log.Warn("sport discovery poll failed", "error", err)
			}
			timer.Reset(sd.interval)
		}
	}
}

func (sd *SportDiscovery) poll(ctx context.Context) error {
	var allNew []SportMarket

	for _, tagID := range sd.tagIDs {
		events, err := sd.gamma.GetEvents(ctx, EventQuery{
			TagID:  tagID,
			Closed: boolPtr(false),
			Limit:  100,
			Order:  "startDate",
		})
		if err != nil {
			sd.log.Warn("sport discovery fetch failed", "tag_id", tagID, "error", err)
			continue
		}

		for _, evt := range events {
			for _, mkt := range evt.Markets {
				if mkt.Closed || !mkt.AcceptingOrders {
					continue
				}

				sd.mu.RLock()
				_, exists := sd.known[mkt.ConditionID]
				sd.mu.RUnlock()
				if exists {
					continue
				}

				tokenIDs, err := ParseTokenIDs(&mkt)
				if err != nil {
					sd.log.Debug("sport discovery: skip market, bad token IDs",
						"condition_id", mkt.ConditionID, "error", err)
					continue
				}

				outcomes, _ := ParseOutcomes(&mkt)

				// Extract tag labels from the event.
				var tagLabels []string
				// Tags are on the event level, not market level in GammaEvent.
				// We don't have them directly here, but the scanner's OnNewMarket
				// handler will pick them up from the WS event. Store the slug prefix as context.

				sm := SportMarket{
					ConditionID: mkt.ConditionID,
					Question:    mkt.Question,
					Slug:        mkt.Slug,
					TokenIDs:    tokenIDs,
					Outcomes:    outcomes,
					Tags:        tagLabels,
					EndDate:     mkt.EndDate,
					Active:      mkt.Active,
				}

				sd.mu.Lock()
				sd.known[mkt.ConditionID] = sm
				for _, id := range tokenIDs {
					sd.tokenIDs[id] = true
				}
				sd.mu.Unlock()

				allNew = append(allNew, sm)

				sd.log.Info("sport market discovered",
					"condition_id", mkt.ConditionID,
					"question", mkt.Question,
					"slug", mkt.Slug,
					"token_ids", len(tokenIDs),
				)
			}
		}
	}

	sd.log.Info("sport discovery poll complete",
		"new_markets", len(allNew),
		"total_tracked", len(sd.known),
	)

	if len(allNew) > 0 && sd.onNew != nil {
		sd.onNew(allNew)
	}

	return nil
}

func boolPtr(v bool) *bool { return &v }
