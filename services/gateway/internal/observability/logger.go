package observability

import (
	"log/slog"
	"os"
)

// NewLogger creates a logger based on environment
func NewLogger(environment string) *slog.Logger {
	var handler slog.Handler

	if environment == "production" {
		// Production: JSON with structured fields
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:     slog.LevelInfo,
			AddSource: true,
		})
	} else {
		// Development: Human-readable text
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})
	}

	return slog.New(handler)
}
