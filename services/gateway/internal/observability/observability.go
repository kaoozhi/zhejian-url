package observability

import (
	"context"
	"log/slog"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Config struct {
	ServiceName  string
	Environment  string // "development", "staging", "production"
	OTLPEndpoint string // e.g., "localhost:4317" — empty means no export
}

// Observability holds all telemetry providers
type Observability struct {
	Logger         *slog.Logger
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
}

// Setup initializes all observability components
func Setup(ctx context.Context, cfg Config) (*Observability, error) {
	// Initialize logger
	logger := NewLogger(cfg.Environment)

	// Initialize tracer
	tp, err := NewTracerProvider(ctx, cfg.ServiceName, cfg.OTLPEndpoint)
	if err != nil {
		return nil, err
	}

	// Initialize meter
	mp, err := NewMeterProvider()
	if err != nil {
		return nil, err
	}

	logger.Info("observability initialized",
		slog.String("service", cfg.ServiceName),
		slog.String("environment", cfg.Environment),
	)

	return &Observability{
		Logger:         logger,
		TracerProvider: tp,
		MeterProvider:  mp,
	}, nil
}

// Shutdown gracefully shuts down all telemetry providers
func (o *Observability) Shutdown(ctx context.Context) {
	o.Logger.Info("shutting down observability")

	if o.TracerProvider != nil {
		if err := o.TracerProvider.Shutdown(ctx); err != nil {
			o.Logger.Error("failed to shutdown tracer provider", slog.String("error", err.Error()))
		}
	}

	if o.MeterProvider != nil {
		if err := o.MeterProvider.Shutdown(ctx); err != nil {
			o.Logger.Error("failed to shutdown metrics provider", slog.String("error", err.Error()))
		}
	}
}
