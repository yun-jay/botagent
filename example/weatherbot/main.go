package weatherbot

import "github.com/yunus/botagent/bot"

// Main is the entry point for running the weather bot standalone.
// Call this from a cmd/main.go or use bot.Run directly.
func Main() {
	bot.Run(&WeatherBot{}, bot.Config{
		Name:           "weather-bot",
		Verbose:        true,
		DryRunEnabled:  false,
		MaxDrawdownPct: 0.20,
		PortfolioValue: 1000,
	})
}
