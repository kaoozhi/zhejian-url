package observability

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// NewMeterProvider creates an OTel MeterProvider with a Prometheus exporter.
// The Prometheus exporter is pull-based: it registers with the default
// prometheus.Registry and serves metrics when /metrics is scraped.
func NewMeterProvider() (*sdkmetric.MeterProvider, error) {
	// Create the Prometheus exporter (registers with default prometheus.Registry)
	exporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}

	// Create MeterProvider with the exporter as a reader
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
	)

	// Register globally so otel.Meter("name") works everywhere
	otel.SetMeterProvider(mp)

	return mp, nil
}
