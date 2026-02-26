package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/zhejian/url-shortener/analytics-worker/internal/consumer"
	"github.com/zhejian/url-shortener/analytics-worker/internal/repository"
)

func main() {
	// Load .env when running locally outside Docker (no-op if file is missing).
	_ = godotenv.Load("../../../../.env")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	amqpURL := mustEnv("AMQP_URL")
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		mustEnv("DB_USER"),
		mustEnv("DB_PASSWORD"),
		getEnv("DB_HOST", "localhost"),
		getEnv("DB_PORT", "5432"),
		mustEnv("DB_NAME"),
		getEnv("DB_SSLMODE", "disable"),
	)
	batchSize := getEnvInt("BATCH_SIZE", 100)
	flushInterval := getEnvDuration("FLUSH_INTERVAL", 5*time.Second)

	// Connect to RabbitMQ.
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer conn.Close()

	// Connect to PostgreSQL.
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping PostgreSQL: %v", err)
	}

	repo := repository.New(db)
	c := consumer.New(conn, repo, logger, batchSize, flushInterval)

	// Declare exchange, DLQ, and main queue — idempotent.
	if err := c.Setup(); err != nil {
		log.Fatalf("Failed to setup RabbitMQ topology: %v", err)
	}

	logger.Info("analytics-worker started",
		slog.Int("batch_size", batchSize),
		slog.Duration("flush_interval", flushInterval))

	// Cancel the run context on SIGINT / SIGTERM — consumer flushes before exit.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		<-quit
		logger.Info("analytics-worker shutting down")
		cancel()
	}()

	if err := c.Run(runCtx); err != nil {
		log.Fatalf("Consumer error: %v", err)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("Required env var %s is not set", key)
	}
	return v
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var i int
		if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
			return i
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
