package main

import (
	"context"
	"errors"
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

	// Connect to PostgreSQL once — the pool handles reconnects automatically.
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

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		<-quit
		logger.Info("analytics-worker: shutdown signal received")
		cancel()
	}()

	// Reconnect loop: re-dials RabbitMQ whenever the broker connection drops.
	//
	// Run() returns nil in two cases:
	//   - ctx cancelled (SIGTERM): disambiguated by runCtx.Err() != nil → exit cleanly.
	//   - deliveries channel closed (broker drop): runCtx.Err() == nil → reconnect.
	//
	// Run() returns a non-nil error in two cases:
	//   - consumer.ErrFatal (DB failure): unrecoverable, exit non-zero for Docker restart.
	//   - plain AMQP error from conn.Channel() in the brief window after dialWithRetry:
	//     retriable, loop back to dialWithRetry.
	for runCtx.Err() == nil {
		conn, err := dialWithRetry(runCtx, amqpURL, logger)
		if err != nil {
			// Context was cancelled (SIGTERM) while waiting to reconnect.
			break
		}

		c := consumer.New(conn, repo, logger, batchSize, flushInterval)
		if err := c.Setup(); err != nil {
			conn.Close()
			logger.Warn("analytics-worker: topology setup failed, will retry",
				slog.String("error", err.Error()))
			continue
		}

		logger.Info("analytics-worker: started",
			slog.Int("batch_size", batchSize),
			slog.Duration("flush_interval", flushInterval))

		if err := c.Run(runCtx); err != nil {
			conn.Close()
			if errors.Is(err, consumer.ErrFatal) {
				// DB error: unacked messages requeue when the connection closes.
				// Exit non-zero so Docker restarts the worker once the DB recovers.
				log.Fatalf("analytics-worker: fatal error: %v", err)
			}
			// Plain AMQP error (e.g. conn.Channel() failed in the timing gap after dial).
			logger.Warn("analytics-worker: AMQP error, reconnecting",
				slog.String("error", err.Error()))
			continue
		}
		conn.Close()

		if runCtx.Err() != nil {
			logger.Info("analytics-worker: stopping cleanly after graceful flush")
			break
		}
		// deliveries channel closed mid-run — broker dropped.
		logger.Warn("analytics-worker: broker connection lost, reconnecting...")
	}

	logger.Info("analytics-worker: stopped")
}

// dialWithRetry dials amqpURL with exponential backoff (1s → 2s → … → 30s cap).
// Blocks until a connection is established or ctx is cancelled.
func dialWithRetry(ctx context.Context, url string, logger *slog.Logger) (*amqp.Connection, error) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		conn, err := amqp.Dial(url)
		if err == nil {
			return conn, nil
		}
		logger.Warn("analytics-worker: RabbitMQ dial failed, retrying",
			slog.String("error", err.Error()),
			slog.Duration("backoff", backoff))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
			if backoff < maxBackoff {
				backoff *= 2
			}
		}
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
