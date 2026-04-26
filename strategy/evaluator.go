package strategy

import (
	"context"

	"github.com/yun-jay/botagent/signal"
)

// Decision is the result of evaluating a signal.
type Decision struct {
	Approved      bool
	Signal        signal.Signal
	PositionSize  float64 // in base currency, set by the evaluator (using a Sizer)
	ReasonSkipped string  // populated when Approved=false
}

// Evaluator decides whether a signal should be traded.
// Each bot implements this with its own filters and sizing logic.
type Evaluator interface {
	Evaluate(ctx context.Context, sig signal.Signal) (Decision, error)
}
