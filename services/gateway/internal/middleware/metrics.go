// internal/middleware/metrics.go
package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metrics returns a Gin middleware that records HTTP request metrics.
func Metrics() gin.HandlerFunc {
	meter := otel.Meter("gateway")

	// Create instruments once (not per-request)
	requestDuration, _ := meter.Float64Histogram("http_request_duration_seconds",
		metric.WithDescription("HTTP request duration in seconds"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0),
	)

	requestCounter, _ := meter.Int64Counter("http_requests_total",
		metric.WithDescription("Total HTTP requests"),
	)

	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		duration := time.Since(start).Seconds()
		attrs := metric.WithAttributes(
			attribute.String("method", c.Request.Method),
			attribute.String("route", c.FullPath()), // "/api/v1/urls/:code"
			attribute.String("status", strconv.Itoa(c.Writer.Status())),
		)

		requestDuration.Record(c.Request.Context(), duration, attrs)
		requestCounter.Add(c.Request.Context(), 1, attrs)
	}
}
