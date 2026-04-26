package log

import (
	"io"
	"log/slog"
	"os"
)

// New creates the standard bot logger.
// JSON handler writing to stdout — directly ingestible by Loki, Datadog, CloudWatch.
// Every line includes: "bot", "time" (RFC3339Nano), "level", "msg".
// Verbose=true sets level to Debug, otherwise Info.
func New(botName string, verbose bool) *slog.Logger {
	return NewWithWriter(botName, verbose, os.Stdout)
}

// NewWithWriter creates a logger writing to a custom io.Writer.
// Use for tests (bytes.Buffer) or custom transports (e.g., direct Loki push).
func NewWithWriter(botName string, verbose bool, w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(handler).With("bot", botName)
}

// WithComponent returns a child logger tagged with a component name.
func WithComponent(logger *slog.Logger, component string) *slog.Logger {
	return logger.With("component", component)
}
