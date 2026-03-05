package infra

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// NewPostgresPool creates a configured connection pool for PostgreSQL.
func NewPostgresPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}

	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}

// NewCacheClient creates a Redis client from a connection string.
// readTimeout and writeTimeout override the go-redis defaults (3 s each);
// pass 0 to keep the defaults.
func NewCacheClient(ctx context.Context, connString string, readTimeout, writeTimeout time.Duration) (*redis.Client, error) {
	opt, err := redis.ParseURL(connString)
	if err != nil {
		return nil, err
	}

	if readTimeout > 0 {
		opt.ReadTimeout = readTimeout
	}
	if writeTimeout > 0 {
		opt.WriteTimeout = writeTimeout
	}

	rdb := redis.NewClient(opt)
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, err
	}

	return rdb, nil
}
