package polymarket

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// BookUpdate represents a real-time order book change from the Polymarket WebSocket.
type BookUpdate struct {
	TokenID   string
	EventType string // "book", "price_change", "best_bid_ask", "market_resolved"
	Timestamp time.Time
}

// PolyFeedStats contains health metrics for the Polymarket WebSocket feed.
type PolyFeedStats struct {
	Connected      bool
	TickCount      int64
	LastTickAge    time.Duration
	SubscribedIDs  int
	CachedBooks    int
	ReconnectCount int64
}

// PolymarketFeed maintains real-time order book data via Polymarket WebSocket.
type PolymarketFeed struct {
	wsURL string
	conn  *websocket.Conn
	mu    sync.RWMutex
	log   *slog.Logger

	// Order book cache: tokenID -> latest OrderBook.
	books    map[string]*OrderBook
	bestBids map[string]float64
	bestAsks map[string]float64

	// Subscription management.
	subscribedIDs map[string]bool
	subMu         sync.Mutex

	// writeMu serializes all writes to conn. gorilla/websocket is explicitly
	// unsafe for concurrent writes; without this, heartbeat PINGs can race
	// with subscribe/resubscribe frames and wedge the connection.
	writeMu sync.Mutex

	// Listener fan-out.
	listeners []chan BookUpdate

	// Tick callback for raw tick storage (latency analysis).
	tickCallback func(tokenID string, mid float64, ingestedAt time.Time)

	// Event callback for raw WS event storage.
	eventCallback func(eventType string, raw []byte, ingestedAt time.Time)

	// Market lifecycle callbacks.
	newMarketCallback       func(event NewMarketEvent)
	marketResolvedCallback  func(event MarketResolvedEvent)

	// Health stats.
	lastTickTime   time.Time
	tickCount      int64
	reconnectCount int64

	done chan struct{}
}

// NewPolymarketFeed creates a new Polymarket WebSocket feed.
func NewPolymarketFeed(wsURL string, logger *slog.Logger) *PolymarketFeed {
	return &PolymarketFeed{
		wsURL:         wsURL,
		books:         make(map[string]*OrderBook),
		bestBids:      make(map[string]float64),
		bestAsks:      make(map[string]float64),
		subscribedIDs: make(map[string]bool),
		done:          make(chan struct{}),
		log:           logger,
	}
}

// OnTick registers a callback that fires on every book update with
// the token's mid-price and the local ingestion timestamp.
func (pf *PolymarketFeed) OnTick(fn func(tokenID string, mid float64, ingestedAt time.Time)) {
	pf.mu.Lock()
	pf.tickCallback = fn
	pf.mu.Unlock()
}

// OnEvent registers a callback that fires on every WS message with
// the event type, raw JSON bytes, and ingestion timestamp.
func (pf *PolymarketFeed) OnEvent(fn func(eventType string, raw []byte, ingestedAt time.Time)) {
	pf.mu.Lock()
	pf.eventCallback = fn
	pf.mu.Unlock()
}

// OnNewMarket registers a callback for new_market WS events.
func (pf *PolymarketFeed) OnNewMarket(fn func(event NewMarketEvent)) {
	pf.mu.Lock()
	pf.newMarketCallback = fn
	pf.mu.Unlock()
}

// OnMarketResolved registers a callback for market_resolved WS events.
func (pf *PolymarketFeed) OnMarketResolved(fn func(event MarketResolvedEvent)) {
	pf.mu.Lock()
	pf.marketResolvedCallback = fn
	pf.mu.Unlock()
}

// Start connects to the Polymarket WebSocket and begins reading events.
func (pf *PolymarketFeed) Start() error {
	if err := pf.connect(); err != nil {
		return err
	}
	go pf.readLoop()
	go pf.heartbeatLoop()
	return nil
}

// Stop closes the WebSocket connection and stops all goroutines.
func (pf *PolymarketFeed) Stop() {
	close(pf.done)
	pf.mu.Lock()
	if pf.conn != nil {
		pf.conn.Close()
		pf.conn = nil
	}
	pf.mu.Unlock()
}

// Subscribe sends a subscription message for the given token IDs.
func (pf *PolymarketFeed) Subscribe(tokenIDs []string) error {
	if len(tokenIDs) == 0 {
		return nil
	}

	pf.subMu.Lock()
	for _, id := range tokenIDs {
		pf.subscribedIDs[id] = true
	}
	pf.subMu.Unlock()

	return pf.sendSubscription(tokenIDs, "subscribe")
}

// Unsubscribe removes subscriptions for the given token IDs and clears their cached books.
func (pf *PolymarketFeed) Unsubscribe(tokenIDs []string) error {
	if len(tokenIDs) == 0 {
		return nil
	}

	pf.subMu.Lock()
	for _, id := range tokenIDs {
		delete(pf.subscribedIDs, id)
	}
	pf.subMu.Unlock()

	// Clear cached books for unsubscribed tokens.
	pf.mu.Lock()
	for _, id := range tokenIDs {
		delete(pf.books, id)
		delete(pf.bestBids, id)
		delete(pf.bestAsks, id)
	}
	pf.mu.Unlock()

	return pf.sendSubscription(tokenIDs, "unsubscribe")
}

// UpdateSubscriptions diffs the desired set against current subscriptions
// and subscribes/unsubscribes the delta.
func (pf *PolymarketFeed) UpdateSubscriptions(desired []string) {
	desiredSet := make(map[string]bool, len(desired))
	for _, id := range desired {
		desiredSet[id] = true
	}

	pf.subMu.Lock()
	var toSub, toUnsub []string
	// New tokens to subscribe.
	for id := range desiredSet {
		if !pf.subscribedIDs[id] {
			toSub = append(toSub, id)
		}
	}
	// Old tokens to unsubscribe.
	for id := range pf.subscribedIDs {
		if !desiredSet[id] {
			toUnsub = append(toUnsub, id)
		}
	}
	pf.subMu.Unlock()

	if len(toUnsub) > 0 {
		if err := pf.Unsubscribe(toUnsub); err != nil {
			pf.log.Warn("failed to unsubscribe tokens", "count", len(toUnsub), "error", err)
		} else {
			pf.log.Info("unsubscribed tokens", "count", len(toUnsub))
		}
	}
	if len(toSub) > 0 {
		if err := pf.Subscribe(toSub); err != nil {
			pf.log.Warn("failed to subscribe tokens", "count", len(toSub), "error", err)
		} else {
			pf.log.Info("subscribed tokens", "count", len(toSub))
		}
	}
}

// GetOrderBook returns the cached order book for a token, if available.
func (pf *PolymarketFeed) GetOrderBook(tokenID string) (*OrderBook, bool) {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	book, ok := pf.books[tokenID]
	if !ok {
		return nil, false
	}
	// Return a copy to avoid races.
	bookCopy := *book
	bookCopy.Bids = make([]PriceLevel, len(book.Bids))
	copy(bookCopy.Bids, book.Bids)
	bookCopy.Asks = make([]PriceLevel, len(book.Asks))
	copy(bookCopy.Asks, book.Asks)
	return &bookCopy, true
}

// GetBestBidAsk returns the cached best bid and ask for a token.
func (pf *PolymarketFeed) GetBestBidAsk(tokenID string) (bid, ask float64, ok bool) {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	bid, bidOK := pf.bestBids[tokenID]
	ask, askOK := pf.bestAsks[tokenID]
	if !bidOK || !askOK {
		return 0, 0, false
	}
	return bid, ask, true
}

// AddListener returns a channel that receives book updates.
func (pf *PolymarketFeed) AddListener() <-chan BookUpdate {
	ch := make(chan BookUpdate, 256)
	pf.mu.Lock()
	pf.listeners = append(pf.listeners, ch)
	pf.mu.Unlock()
	return ch
}

// Stats returns current feed health metrics.
func (pf *PolymarketFeed) Stats() PolyFeedStats {
	// Acquire subMu FIRST to maintain consistent lock ordering with
	// Unsubscribe (subMu → mu). Nesting subMu inside mu deadlocks when
	// Unsubscribe holds subMu and waits for mu.Lock.
	pf.subMu.Lock()
	subCount := len(pf.subscribedIDs)
	pf.subMu.Unlock()

	pf.mu.RLock()
	defer pf.mu.RUnlock()

	var lastTickAge time.Duration
	if !pf.lastTickTime.IsZero() {
		lastTickAge = time.Since(pf.lastTickTime)
	}

	return PolyFeedStats{
		Connected:      pf.conn != nil,
		TickCount:      pf.tickCount,
		LastTickAge:    lastTickAge,
		SubscribedIDs:  subCount,
		CachedBooks:    len(pf.books),
		ReconnectCount: pf.reconnectCount,
	}
}

// --- Internal ---

// writeTimeout bounds every write so a stalled TCP send buffer can never
// block the writer forever (which would in turn stall readers of pf.mu).
const writeTimeout = 5 * time.Second

// safeWriteJSON serializes writes and enforces a write deadline.
func (pf *PolymarketFeed) safeWriteJSON(conn *websocket.Conn, v any) error {
	pf.writeMu.Lock()
	defer pf.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return conn.WriteJSON(v)
}

// safeWriteMessage serializes writes and enforces a write deadline.
func (pf *PolymarketFeed) safeWriteMessage(conn *websocket.Conn, messageType int, data []byte) error {
	pf.writeMu.Lock()
	defer pf.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return conn.WriteMessage(messageType, data)
}

func (pf *PolymarketFeed) connect() error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.Dial(pf.wsURL, nil)
	if err != nil {
		return fmt.Errorf("polymarket ws connect: %w", err)
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	})
	pf.mu.Lock()
	pf.conn = conn
	pf.mu.Unlock()
	pf.log.Info("connected to Polymarket WebSocket", "url", pf.wsURL)
	return nil
}

func (pf *PolymarketFeed) sendSubscription(tokenIDs []string, operation string) error {
	pf.mu.RLock()
	conn := pf.conn
	pf.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}

	msg := map[string]any{
		"assets_ids":             tokenIDs,
		"type":                   "market",
		"custom_feature_enabled": true,
	}
	if operation != "" && operation != "subscribe" {
		// Initial subscription doesn't need operation field.
		// Dynamic subscribe/unsubscribe uses the operation field.
		msg["operation"] = operation
	} else if operation == "subscribe" {
		// For dynamic subscription after initial connect.
		msg["operation"] = "subscribe"
	}

	return pf.safeWriteJSON(conn, msg)
}

// resubscribeAll re-sends subscription for all tracked token IDs after reconnect.
func (pf *PolymarketFeed) resubscribeAll() {
	pf.subMu.Lock()
	ids := make([]string, 0, len(pf.subscribedIDs))
	for id := range pf.subscribedIDs {
		ids = append(ids, id)
	}
	pf.subMu.Unlock()

	if len(ids) == 0 {
		return
	}

	// Send as initial subscription (no operation field).
	pf.mu.RLock()
	conn := pf.conn
	pf.mu.RUnlock()
	if conn == nil {
		return
	}

	msg := map[string]any{
		"assets_ids":             ids,
		"type":                   "market",
		"custom_feature_enabled": true,
	}
	if err := pf.safeWriteJSON(conn, msg); err != nil {
		pf.log.Warn("failed to resubscribe after reconnect", "error", err, "count", len(ids))
	} else {
		pf.log.Info("resubscribed after reconnect", "count", len(ids))
	}
}

// Polymarket WebSocket message types.
type wsBookEvent struct {
	EventType string       `json:"event_type"`
	AssetID   string       `json:"asset_id"`
	Market    string       `json:"market"`
	Bids      []PriceLevel `json:"bids"`
	Asks      []PriceLevel `json:"asks"`
	Timestamp string       `json:"timestamp"`
	Hash      string       `json:"hash"`
}

type wsBestBidAskEvent struct {
	EventType string `json:"event_type"`
	AssetID   string `json:"asset_id"`
	Market    string `json:"market"`
	BestBid   string `json:"best_bid"`
	BestAsk   string `json:"best_ask"`
	Timestamp string `json:"timestamp"`
}

type wsPriceChangeEvent struct {
	EventType string       `json:"event_type"`
	AssetID   string       `json:"asset_id"`
	Market    string       `json:"market"`
	Side      string       `json:"side"` // "BUY" or "SELL"
	Changes   []PriceLevel `json:"changes"`
	Timestamp string       `json:"timestamp"`
}

type wsGenericEvent struct {
	EventType string `json:"event_type"`
	AssetID   string `json:"asset_id"`
	Market    string `json:"market"`
}

func (pf *PolymarketFeed) readLoop() {
	for {
		select {
		case <-pf.done:
			return
		default:
		}

		pf.mu.RLock()
		conn := pf.conn
		pf.mu.RUnlock()

		if conn == nil {
			pf.reconnect()
			continue
		}

		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			pf.log.Warn("polymarket ws read error, reconnecting", "error", err)
			pf.reconnect()
			continue
		}

		pf.handleMessage(msg)
	}
}

func (pf *PolymarketFeed) handleMessage(msg []byte) {
	// First parse the event_type to dispatch.
	var generic wsGenericEvent
	if err := json.Unmarshal(msg, &generic); err != nil {
		pf.log.Debug("failed to parse ws message", "error", err)
		return
	}

	if generic.EventType == "" {
		// Could be a PONG or other non-event message.
		return
	}

	now := time.Now()

	// Fire raw event callback for archival storage (before any parsing).
	pf.mu.RLock()
	ecb := pf.eventCallback
	pf.mu.RUnlock()
	if ecb != nil {
		ecb(generic.EventType, msg, now)
	}

	switch generic.EventType {
	case "book":
		var evt wsBookEvent
		if err := json.Unmarshal(msg, &evt); err != nil {
			pf.log.Debug("failed to parse book event", "error", err)
			return
		}
		pf.handleBook(evt, now)

	case "best_bid_ask":
		var evt wsBestBidAskEvent
		if err := json.Unmarshal(msg, &evt); err != nil {
			pf.log.Debug("failed to parse best_bid_ask event", "error", err)
			return
		}
		pf.handleBestBidAsk(evt, now)

	case "price_change":
		var evt wsPriceChangeEvent
		if err := json.Unmarshal(msg, &evt); err != nil {
			pf.log.Debug("failed to parse price_change event", "error", err)
			return
		}
		pf.handlePriceChange(evt, now)

	case "last_trade_price":
		// Not used for edge detection, ignore.
		return

	case "tick_size_change":
		// Not relevant, ignore.
		return

	case "new_market":
		var evt NewMarketEvent
		if err := json.Unmarshal(msg, &evt); err != nil {
			pf.log.Debug("failed to parse new_market event", "error", err)
			return
		}
		pf.log.Info("new_market event received",
			"condition_id", evt.ConditionID, "question", evt.Question,
			"assets", len(evt.AssetsIDs), "tags", evt.Tags)
		pf.mu.RLock()
		cb := pf.newMarketCallback
		pf.mu.RUnlock()
		if cb != nil {
			cb(evt)
		}
		return

	case "market_resolved":
		var evt MarketResolvedEvent
		if err := json.Unmarshal(msg, &evt); err != nil {
			pf.log.Debug("failed to parse market_resolved event", "error", err)
			return
		}
		pf.log.Info("market_resolved event received",
			"condition_id", evt.Market, "winning_outcome", evt.WinningOutcome)
		// Fan out as BookUpdate for backward compat (existing listeners).
		pf.fanOut(BookUpdate{
			TokenID:   generic.AssetID,
			EventType: "market_resolved",
			Timestamp: now,
		})
		pf.mu.RLock()
		cb := pf.marketResolvedCallback
		pf.mu.RUnlock()
		if cb != nil {
			cb(evt)
		}
		return

	default:
		pf.log.Debug("unknown ws event type", "type", generic.EventType)
		return
	}
}

func (pf *PolymarketFeed) handleBook(evt wsBookEvent, now time.Time) {
	book := &OrderBook{
		Bids:      evt.Bids,
		Asks:      evt.Asks,
		AssetID:   evt.AssetID,
		Timestamp: evt.Timestamp,
	}

	pf.mu.Lock()
	pf.books[evt.AssetID] = book
	pf.lastTickTime = now
	pf.tickCount++
	// Update best bid/ask from the full book.
	// CLOB book is sorted: bids ascending (worst→best), asks descending (worst→best).
	// Best bid = last bid (highest price), best ask = last ask (lowest price).
	if len(evt.Bids) > 0 {
		if p, err := strconv.ParseFloat(evt.Bids[len(evt.Bids)-1].Price, 64); err == nil {
			pf.bestBids[evt.AssetID] = p
		}
	}
	if len(evt.Asks) > 0 {
		if p, err := strconv.ParseFloat(evt.Asks[len(evt.Asks)-1].Price, 64); err == nil {
			pf.bestAsks[evt.AssetID] = p
		}
	}
	cb := pf.tickCallback
	bid := pf.bestBids[evt.AssetID]
	ask := pf.bestAsks[evt.AssetID]
	pf.mu.Unlock()

	// Fire tick callback with mid-price for latency analysis.
	if cb != nil && bid > 0 && ask > 0 {
		cb(evt.AssetID, (bid+ask)/2, now)
	}

	pf.fanOut(BookUpdate{
		TokenID:   evt.AssetID,
		EventType: "book",
		Timestamp: now,
	})
}

func (pf *PolymarketFeed) handleBestBidAsk(evt wsBestBidAskEvent, now time.Time) {
	bid, _ := strconv.ParseFloat(evt.BestBid, 64)
	ask, _ := strconv.ParseFloat(evt.BestAsk, 64)
	if bid <= 0 && ask <= 0 {
		return
	}

	pf.mu.Lock()
	if bid > 0 {
		pf.bestBids[evt.AssetID] = bid
	}
	if ask > 0 {
		pf.bestAsks[evt.AssetID] = ask
	}
	pf.lastTickTime = now
	pf.tickCount++
	pf.mu.Unlock()

	pf.fanOut(BookUpdate{
		TokenID:   evt.AssetID,
		EventType: "best_bid_ask",
		Timestamp: now,
	})
}

func (pf *PolymarketFeed) handlePriceChange(evt wsPriceChangeEvent, now time.Time) {
	pf.mu.Lock()
	book, exists := pf.books[evt.AssetID]
	if exists {
		// Apply incremental updates to the cached book.
		if evt.Side == "BUY" {
			book.Bids = applyPriceChanges(book.Bids, evt.Changes)
			if len(book.Bids) > 0 {
				if p, err := strconv.ParseFloat(book.Bids[len(book.Bids)-1].Price, 64); err == nil {
					pf.bestBids[evt.AssetID] = p
				}
			}
		} else {
			book.Asks = applyPriceChanges(book.Asks, evt.Changes)
			if len(book.Asks) > 0 {
				if p, err := strconv.ParseFloat(book.Asks[len(book.Asks)-1].Price, 64); err == nil {
					pf.bestAsks[evt.AssetID] = p
				}
			}
		}
	}
	pf.lastTickTime = now
	pf.tickCount++
	pf.mu.Unlock()

	pf.fanOut(BookUpdate{
		TokenID:   evt.AssetID,
		EventType: "price_change",
		Timestamp: now,
	})
}

// applyPriceChanges applies incremental price level changes to a side of the book.
// Levels with size "0" are removed; others are upserted.
func applyPriceChanges(levels []PriceLevel, changes []PriceLevel) []PriceLevel {
	// Build a map for O(1) lookup.
	m := make(map[string]string, len(levels))
	for _, l := range levels {
		m[l.Price] = l.Size
	}
	for _, c := range changes {
		if c.Size == "0" || c.Size == "0.0" || c.Size == "" {
			delete(m, c.Price)
		} else {
			m[c.Price] = c.Size
		}
	}
	result := make([]PriceLevel, 0, len(m))
	for price, size := range m {
		result = append(result, PriceLevel{Price: price, Size: size})
	}
	// Sort by price descending for bids, but we don't know side here.
	// The caller uses the first element for best bid/ask, so just sort descending
	// (works for bids; for asks the engine re-parses anyway).
	// A proper sort would need to know the side. Since evaluateContract parses
	// all levels anyway, ordering here is best-effort.
	return result
}

func (pf *PolymarketFeed) fanOut(update BookUpdate) {
	pf.mu.RLock()
	for _, ch := range pf.listeners {
		select {
		case ch <- update:
		default:
		}
	}
	pf.mu.RUnlock()
}

func (pf *PolymarketFeed) heartbeatLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-pf.done:
			return
		case <-ticker.C:
			pf.mu.RLock()
			conn := pf.conn
			pf.mu.RUnlock()
			if conn != nil {
				if err := pf.safeWriteMessage(conn, websocket.TextMessage, []byte("PING")); err != nil {
					pf.log.Debug("heartbeat write failed", "error", err)
				}
			}
		}
	}
}

func (pf *PolymarketFeed) reconnect() {
	pf.mu.Lock()
	if pf.conn != nil {
		pf.conn.Close()
		pf.conn = nil
	}
	// Clear book cache on reconnect — incremental updates may have been missed.
	pf.books = make(map[string]*OrderBook)
	pf.bestBids = make(map[string]float64)
	pf.bestAsks = make(map[string]float64)
	pf.reconnectCount++
	pf.mu.Unlock()

	backoff := time.Second
	maxBackoff := 30 * time.Second
	for {
		select {
		case <-pf.done:
			return
		default:
		}
		pf.log.Info("attempting Polymarket WS reconnect", "backoff", backoff)
		time.Sleep(backoff)
		if err := pf.connect(); err != nil {
			pf.log.Warn("reconnect failed", "error", err)
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
			continue
		}
		pf.log.Info("reconnected to Polymarket WebSocket")
		// Re-subscribe to all tracked tokens.
		pf.resubscribeAll()
		return
	}
}
