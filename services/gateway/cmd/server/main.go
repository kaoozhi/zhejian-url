package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zhejian/url-shortener/gateway/internal/api"
	"github.com/zhejian/url-shortener/gateway/internal/repository"
	"github.com/zhejian/url-shortener/gateway/internal/service"
)

func main() {
	// TODO: Load configuration from environment variables or config file
	// - Database connection string
	// - Server port
	// - Base URL for short links

	// TODO: Initialize database connection pool
	// db, err := repository.NewPostgresPool(ctx, connectionString)

	// TODO: Initialize repository
	// urlRepo := repository.NewURLRepository(db)

	// TODO: Initialize service
	// urlService := service.NewURLService(urlRepo, baseURL)

	// TODO: Initialize handler and router
	// handler := api.NewHandler(urlService)
	// router := handler.SetupRouter()

	// TODO: Create HTTP server
	// srv := &http.Server{
	//     Addr:    ":8080",
	//     Handler: router,
	// }

	// TODO: Start server in goroutine
	// go func() {
	//     if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
	//         log.Fatalf("listen: %s\n", err)
	//     }
	// }()

	// TODO: Graceful shutdown
	// - Wait for interrupt signal
	// - Create shutdown context with timeout
	// - Call srv.Shutdown(ctx)
	// - Close database connection

	_ = context.Background()
	_ = log.Println
	_ = http.StatusOK
	_ = os.Getenv
	_ = signal.Notify
	_ = syscall.SIGINT
	_ = time.Second
	_ = api.NewHandler
	_ = repository.NewURLRepository
	_ = service.NewURLService
}
