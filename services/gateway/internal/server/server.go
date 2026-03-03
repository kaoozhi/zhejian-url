package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/zhejian/url-shortener/gateway/internal/analytics"
	"github.com/zhejian/url-shortener/gateway/internal/api"
	"github.com/zhejian/url-shortener/gateway/internal/config"
	"github.com/zhejian/url-shortener/gateway/internal/middleware"
	"github.com/zhejian/url-shortener/gateway/internal/observability"
	"github.com/zhejian/url-shortener/gateway/internal/ratelimit"
	"github.com/zhejian/url-shortener/gateway/internal/repository"
	"github.com/zhejian/url-shortener/gateway/internal/service"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// redisPinger adapts *redis.Client to api.CacheInterface.
type redisPinger struct{ client *redis.Client }

func (r *redisPinger) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// NewRouter initializes all dependencies and returns a configured Gin router.
// Middleware is registered before routes so it applies to all requests.
func NewRouter(cfg *config.Config, db *pgxpool.Pool, cache *redis.Client, rateLimiter *ratelimit.Client, obs *observability.Observability, pub *analytics.Publisher) *gin.Engine {
	r := gin.Default()

	// Metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Middleware: tracing first (creates span), then logging (reads span context) and metrics
	r.Use(otelgin.Middleware("gateway"))
	r.Use(middleware.Logging(obs.Logger))
	r.Use(middleware.Metrics())
	if rateLimiter != nil {
		r.Use(middleware.RateLimit(rateLimiter, obs.Logger))
	}

	// Wire dependencies and register routes
	baseRepo := repository.NewURLRepository(db)
	urlRepo := repository.NewCachedURLRepository(baseRepo, cache, cfg.Cache.TTL, obs.Logger)
	urlService := service.NewURLService(urlRepo, obs.Logger, cfg.App.BaseURL, cfg.App.ShortCodeLen, cfg.App.ShortCodeRetries)
	handler := api.NewHandler(urlService, db, &redisPinger{client: cache}, obs.Logger, pub).WithCBProviders(urlRepo, rateLimiter)
	handler.RegisterRoutes(r)

	return r
}

// NewServer initializes all dependencies and returns a configured HTTP server.
// This includes the router plus HTTP server settings (timeouts, address, etc.).
func NewServer(cfg *config.Config, db *pgxpool.Pool, cache *redis.Client, rateLimiter *ratelimit.Client, obs *observability.Observability, pub *analytics.Publisher) *http.Server {
	router := NewRouter(cfg, db, cache, rateLimiter, obs, pub)

	return &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}
