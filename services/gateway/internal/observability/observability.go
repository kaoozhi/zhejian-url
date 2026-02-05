package observability

import (
	"context"
	"log/slog"
	// sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Config struct {
	ServiceName string
	Environment string // "development", "staging", "production"
	// OTLPEndpoint   string // e.g., "otel-collector:4317"
}

// Observability holds all telemetry providers
type Observability struct {
	Logger *slog.Logger
	// TracerProvider *sdktrace.TracerProvider
	// MetricsProvider will be added later
}

// Setup initializes all observability components
func Setup(ctx context.Context, cfg Config) (*Observability, error) {
	// Initialize logger
	logger := NewLogger(cfg.Environment)

	// Initialize tracer
	// tp, err := NewTracerProvider(ctx, cfg.ServiceName, cfg.OTLPEndpoint)
	// if err != nil {
	//     return nil, err
	// }

	logger.Info("observability initialized",
		slog.String("service", cfg.ServiceName),
		slog.String("environment", cfg.Environment),
	)

	return &Observability{
		Logger: logger,
		// TracerProvider: tp,
	}, nil
}

// Shutdown gracefully shuts down all telemetry
func (o *Observability) Shutdown(ctx context.Context) {
	o.Logger.Info("shutting down observability")

	// Flush traces (will be added in Phase 2)
	// if o.TracerProvider != nil {
	// 	if err := o.TracerProvider.Shutdown(ctx); err != nil {
	// 		return err
	// 	}
	// }

	// return nil
}
