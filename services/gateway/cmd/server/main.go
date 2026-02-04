package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zhejian/url-shortener/gateway/internal/config"
	"github.com/zhejian/url-shortener/gateway/internal/infra"
	"github.com/zhejian/url-shortener/gateway/internal/server"
)

func main() {
	// Load configuration from environment variables
	cfg := config.Load()

	// Create background context for database connection
	ctx := context.Background()

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
	log.Println("Database connected successfully")

	// Verify cache connectivity
	if err := cache.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to ping cache: %v", err)
	}
	log.Println("Cache connected successfully")

	srv := server.NewServer(cfg, db, cache)

	// Start server in a goroutine
	go func() {
		log.Printf("Server starting on port %s", cfg.Server.Port)
		log.Printf("Base URL: %s", cfg.App.BaseURL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Graceful shutdown
	// Wait for interrupt signal (Ctrl+C or SIGTERM)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Create shutdown context with 10 second timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited gracefully")
}
