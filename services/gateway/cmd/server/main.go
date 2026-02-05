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
		ServiceName: "gateway",
		Environment: "development",
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

	// Connect to cache
	cacheConnString := cfg.Cache.ConnectionString()
	cache, err := infra.NewCacheClient(ctx, cacheConnString)
	if err != nil {
		log.Fatalf("Failed to connect to cache: %v", err)
	}
	defer cache.Close()

	// Verify database connectivity
	if err := db.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}
	obs.Logger.Info("Database connected successfully")

	// Verify cache connectivity
	if err := cache.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to ping cache: %v", err)
	}
	obs.Logger.Info("Cache connected successfully")

	srv := server.NewServer(cfg, db, cache, obs)

	// Start server in a goroutine
	go func() {
		obs.Logger.Info("Server starting",
			slog.String("port", cfg.Server.Port),
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
