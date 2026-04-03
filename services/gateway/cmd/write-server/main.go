package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zhejian/url-shortener/gateway/internal/cache"
	"github.com/zhejian/url-shortener/gateway/internal/config"
	"github.com/zhejian/url-shortener/gateway/internal/infra"
	"github.com/zhejian/url-shortener/gateway/internal/observability"
	"github.com/zhejian/url-shortener/gateway/internal/server"
)

func main() {
	// Load configuration from environment variables
	cfg := config.Load()

	// Create background context for database connection
	ctx := context.Background()

	// Setup observability
	obs, err := observability.Setup(ctx, observability.Config{
		ServiceName:  "write-service",
		Environment:  "development",
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	})

	if err != nil {
		log.Fatalf("Failed to enable observability: %v", err)
	}
	defer obs.Shutdown(ctx)

	// Connect to database
	DBconnectionString := cfg.Database.ConnectionString()
	db, err := infra.NewPostgresPool(ctx, DBconnectionString)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Connect to cache — always build a HashRing; single node is a ring of one.
	// cfg.Cache.Nodes is always populated (defaults to CACHE_HOST:CACHE_PORT when CACHE_NODES is unset).
	clients, err := infra.NewCacheRings(ctx, cfg.Cache.Nodes, cfg.Cache.ReadTimeout, cfg.Cache.WriteTimeout, cfg.Cache.PoolSize)
	if err != nil {
		log.Fatalf("Failed to connect to cache: %v", err)
	}
	cacheProvider := cache.NewHashRing(clients, 150)
	defer cacheProvider.Close()

	// Verify database connectivity
	if err := db.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}
	obs.Logger.Info("Database connected successfully")

	// Verify cache connectivity
	if err := cacheProvider.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping cache: %v", err)
	}
	obs.Logger.Info("Cache connected successfully")

	// Setup rate limiter
	// var rateLimiter *ratelimit.Client
	// if cfg.RateLimiter.Enabled {
	// 	rateLimiter, err = ratelimit.NewClient(cfg.RateLimiter.Addr, cfg.RateLimiter.Timeout, obs.Logger)
	// 	if err != nil {
	// 		log.Fatalf("Failed to setup rate limiter: %v", err)
	// 	}
	// 	defer rateLimiter.Close()
	// 	obs.Logger.Info("Rate limiter enabled")
	// }

	// Setup analytics publisher (optional — disabled when AMQP_URL is empty)
	// var pub *analytics.Publisher
	// if cfg.Analytics.Enabled {
	// 	pub, err = analytics.NewPublisher(cfg.Analytics.AMQPURL, obs.Logger)
	// 	if err != nil {
	// 		log.Fatalf("Failed to connect to RabbitMQ: %v", err)
	// 	}
	// 	defer pub.Close()
	// 	obs.Logger.Info("Analytics publisher enabled")
	// }

	srv := server.NewWriteServer(cfg, db, cacheProvider, nil, obs, nil)

	// Start write server in a goroutine
	go func() {
		obs.Logger.Info("Write Server starting",
			slog.String("port", cfg.WriteServer.Port),
			slog.String("base_url", cfg.App.BaseURL))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Graceful shutdown
	// Wait for interrupt signal (Ctrl+C or SIGTERM)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	obs.Logger.Info("Shutting down server...")

	// Create shutdown context with 10 second timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	obs.Logger.Info("Server exited gracefully")
}
