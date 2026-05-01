package polymarket

// GammaEvent represents an event from the Polymarket Gamma API.
type GammaEvent struct {
	ID        string        `json:"id"`
	Slug      string        `json:"slug"`
	Title     string        `json:"title"`
	StartDate string        `json:"startDate"`
	EndDate   string        `json:"endDate"`
	Active    bool          `json:"active"`
	Closed    bool          `json:"closed"`
	Volume    float64       `json:"volume"`
	Liquidity float64       `json:"liquidity"`
	Markets   []GammaMarket `json:"markets"`
}

// GammaMarket represents a market within a Gamma event.
type GammaMarket struct {
	ID              string  `json:"id"`
	Question        string  `json:"question"`
	ConditionID     string  `json:"conditionId"`
	Slug            string  `json:"slug"`
	ClobTokenIDs    string  `json:"clobTokenIds"`    // JSON array as string
	Outcomes        string  `json:"outcomes"`         // JSON array as string
	OutcomePrices   string  `json:"outcomePrices"`    // JSON array as string
	Active          bool    `json:"active"`
	Closed          bool    `json:"closed"`
	EndDate         string  `json:"endDate"`
	AcceptingOrders bool    `json:"acceptingOrders"`
	Volume          float64 `json:"volumeNum"`
	GroupItemTitle  string  `json:"groupItemTitle"`
}

// PricePoint represents a single historical price observation.
type PricePoint struct {
	T int64   `json:"t"` // Unix timestamp
	P float64 `json:"p"` // Price (0-1 probability)
}

// EventQuery holds filters for the GetEvents endpoint.
type EventQuery struct {
	TagID     int
	Slug      string
	Closed    *bool
	Active    *bool
	Limit     int
	Offset    int
	Order     string // field to order by, e.g. "startDate"
	Ascending bool
}
