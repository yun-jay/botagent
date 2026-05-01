package polymarket

import "time"

// OrderBook represents the current state of an order book for a token.
type OrderBook struct {
	Bids      []PriceLevel `json:"bids"`
	Asks      []PriceLevel `json:"asks"`
	AssetID   string       `json:"asset_id"`
	Timestamp string       `json:"timestamp"` // Unix ms as string from API
}

type PriceLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// Market represents a Polymarket market/condition.
type Market struct {
	ConditionID   string        `json:"condition_id"`
	QuestionID    string        `json:"question_id"`
	Question      string        `json:"question"`
	Active        bool          `json:"active"`
	Closed        bool          `json:"closed"`
	Volume        string        `json:"volume"`
	EndDateISO    string        `json:"end_date_iso"`
	GameStartTime string        `json:"game_start_time"`
	Tokens        []MarketToken `json:"tokens"`
}

// MarketToken represents a token in a resolved market.
// Price is 0 (lost) or 1 (won) after resolution.
type MarketToken struct {
	TokenID string  `json:"token_id"`
	Outcome string  `json:"outcome"` // "Up" or "Down"
	Price   float64 `json:"price"`   // 0 or 1 after resolution
}

// FeeSchedule represents the fee structure of a market.
type FeeSchedule struct {
	Exponent   string `json:"exponent"`
	Rate       string `json:"rate"`
	TakerOnly  bool   `json:"taker_only"`
	RebateRate string `json:"rebate_rate"`
}

// NewMarketEvent is emitted via WS when a new market is created.
// Requires custom_feature_enabled: true.
type NewMarketEvent struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Market      string   `json:"market"` // condition_id
	Slug        string   `json:"slug"`
	Description string   `json:"description"`
	AssetsIDs   []string `json:"assets_ids"`
	Outcomes    []string `json:"outcomes"`
	EventType   string   `json:"event_type"` // "new_market"
	Timestamp   string   `json:"timestamp"`
	Tags        []string `json:"tags"`
	ConditionID string   `json:"condition_id"`
	Active      bool     `json:"active"`
	ClobTokenIDs []string `json:"clob_token_ids"`
	TickSize    string   `json:"order_price_min_tick_size"`
	FeeSchedule *FeeSchedule `json:"fee_schedule,omitempty"`
	GroupItemTitle string `json:"group_item_title"`
}

// MarketResolvedEvent is emitted via WS when a market resolves.
// Requires custom_feature_enabled: true.
type MarketResolvedEvent struct {
	ID              string   `json:"id"`
	Question        string   `json:"question"`
	Market          string   `json:"market"` // condition_id
	Slug            string   `json:"slug"`
	AssetsIDs       []string `json:"assets_ids"`
	Outcomes        []string `json:"outcomes"`
	WinningAssetID  string   `json:"winning_asset_id"`
	WinningOutcome  string   `json:"winning_outcome"`
	EventType       string   `json:"event_type"` // "market_resolved"
	Timestamp       string   `json:"timestamp"`
}

// OrderSide represents buy or sell.
type OrderSide string

const (
	Buy  OrderSide = "BUY"
	Sell OrderSide = "SELL"
)

// OrderType represents the order type.
type OrderType string

const (
	LimitOrder  OrderType = "GTC"  // Good Til Cancelled
	FOKOrder    OrderType = "FOK"  // Fill or Kill
	GTDOrder    OrderType = "GTD"  // Good Til Date
)

// OrderRequest is what we send to the CLOB.
type OrderRequest struct {
	TokenID    string    `json:"tokenID"`
	Price      float64   `json:"price"`
	Size       float64   `json:"size"`
	Side       OrderSide `json:"side"`
	OrderType  OrderType `json:"type"`
	FeeRateBps int       `json:"feeRateBps"`
	Expiration int64     `json:"expiration,omitempty"` // Unix timestamp, required for GTD orders
}

// OrderResponse is what the CLOB returns.
type OrderResponse struct {
	OrderID   string `json:"orderID"`
	Status    string `json:"status"`
	ErrorMsg  string `json:"errorMsg"`
	TransactHash string `json:"transactHash"`
}

// OpenOrder represents an active order.
type OpenOrder struct {
	OrderID       string    `json:"id"`
	TokenID       string    `json:"asset_id"`
	Side          string    `json:"side"`
	Price         string    `json:"price"`
	OriginalSize  string    `json:"original_size"`
	SizeMatched   string    `json:"size_matched"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

// BalanceResponse holds the user's USDC balance info.
type BalanceResponse struct {
	Balance string `json:"balance"`
}
