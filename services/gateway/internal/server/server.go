package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/zhejian/url-shortener/gateway/internal/analytics"
	"github.com/zhejian/url-shortener/gateway/internal/api"
	"github.com/zhejian/url-shortener/gateway/internal/cache"
	"github.com/zhejian/url-shortener/gateway/internal/config"
	"github.com/zhejian/url-shortener/gateway/internal/middleware"
	"github.com/zhejian/url-shortener/gateway/internal/observability"
	"github.com/zhejian/url-shortener/gateway/internal/ratelimit"
	"github.com/zhejian/url-shortener/gateway/internal/repository"
	"github.com/zhejian/url-shortener/gateway/internal/service"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// NewRouter initializes all dependencies and returns a configured Gin router.
// Middleware is registered before routes so it applies to all requests.
func NewRouter(serviceName string, cfg *config.Config, db *pgxpool.Pool, cache cache.ClientProvider, rateLimiter *ratelimit.Client, obs *observability.Observability, pub *analytics.Publisher) (*gin.Engine, *api.Handler) {
	r := gin.Default()

	// Metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Middleware: tracing first (creates span), then logging (reads span context) and metrics
	r.Use(otelgin.Middleware(serviceName))
	r.Use(middleware.Logging(obs.Logger))
	r.Use(middleware.Metrics())
	if rateLimiter != nil {
		r.Use(middleware.RateLimit(rateLimiter, obs.Logger))
	}

	// Wire dependencies and register routes
	baseRepo := repository.NewURLRepository(db)
	cacheCB := repository.DefaultCBSettings()
	cacheCB.OperationTimeout = cfg.Cache.OperationTimeout
	cacheCB.MinRequestsToTrip = cfg.Cache.CBMinRequests
	cacheCB.FailureRateThreshold = cfg.Cache.CBFailureRate
	cacheCB.ConsecutiveFailures = cfg.Cache.CBConsecutiveFailures
	cacheCB.Timeout = cfg.Cache.CBTimeout
	urlRepo := repository.NewCachedURLRepository(baseRepo, cache, cfg.Cache.TTL, obs.Logger,
		repository.CachedURLRepositoryOptions{CacheCB: &cacheCB})
	urlService := service.NewURLService(urlRepo, obs.Logger, cfg.App.BaseURL, cfg.App.ShortCodeLen, cfg.App.ShortCodeRetries)
	var rlCB api.CBStateProvider
	if rateLimiter != nil {
		rlCB = rateLimiter
	}
	handler := api.NewHandler(urlService, db, cache, obs.Logger, pub).WithCBProviders(urlRepo, rlCB)
	return r, handler
}

// NewServer initializes all dependencies and returns a configured HTTP server.
// This includes the router plus HTTP server settings (timeouts, address, etc.).
func NewServer(cfg *config.Config, db *pgxpool.Pool, cache cache.ClientProvider, rateLimiter *ratelimit.Client, obs *observability.Observability, pub *analytics.Publisher) *http.Server {
	router, handler := NewRouter("gateway", cfg, db, cache, rateLimiter, obs, pub)
	handler.RegisterRoutes(router)

	return &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// NewWriteServer returns an HTTP server for the write-service binary.
// Serves write routes (POST /shorten, DELETE /urls/:code) plus health.
// No rate limit middleware — writes are low-frequency and will be protected by auth later.
// pub should be nil: analytics are not published on the write path.
func NewWriteServer(cfg *config.Config, db *pgxpool.Pool, cache cache.ClientProvider, rateLimiter *ratelimit.Client, obs *observability.Observability, pub *analytics.Publisher) *http.Server {
	router, handler := NewRouter("write-service", cfg, db, cache, rateLimiter, obs, pub)
	handler.RegisterWriteRoutes(router)
	return &http.Server{Addr: ":" + cfg.WriteServer.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second}
}

// NewReadServer returns an HTTP server for the read-service binary.
// Serves only read routes (GET /urls/:code, GET /:code redirect) plus health.
// Rate limit middleware is active when rateLimiter is non-nil.
// Analytics publisher fires click events on redirect.
func NewReadServer(cfg *config.Config, db *pgxpool.Pool, cache cache.ClientProvider, rateLimiter *ratelimit.Client, obs *observability.Observability, pub *analytics.Publisher) *http.Server {
	router, handler := NewRouter("read-service", cfg, db, cache, rateLimiter, obs, pub)
	handler.RegisterReadRoutes(router)

	return &http.Server{
		Addr:         ":" + cfg.ReadServer.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}
