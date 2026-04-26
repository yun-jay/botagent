package alert

import "context"

// Alerter delivers notifications. Market-agnostic interface.
type Alerter interface {
	// Send delivers a markdown-formatted message to the configured channel.
	Send(ctx context.Context, msg string) error
	// IsConfigured returns true if alerting is set up.
	IsConfigured() bool
}

// NopAlerter is a no-op implementation for bots that don't need alerts or in tests.
type NopAlerter struct{}

func (n *NopAlerter) Send(_ context.Context, _ string) error { return nil }
func (n *NopAlerter) IsConfigured() bool                     { return false }
