package infra

import (
	"context"
	"fmt"
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
// readTimeout and writeTimeout override the go-redis defaults (3 s each); pass 0 to keep the defaults.
// poolSize sets the connection pool size per node; pass 0 to use the go-redis default (10 * GOMAXPROCS).
func NewCacheClient(ctx context.Context, connString string, readTimeout, writeTimeout time.Duration, poolSize int) (*redis.Client, error) {
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
	if poolSize > 0 {
		opt.PoolSize = poolSize
	}

	rdb := redis.NewClient(opt)
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, err
	}

	return rdb, nil
}

// NewCacheRings creates a Redis client for each node in cacheNodes (host:port format).
// On partial failure, already-opened clients are closed before returning the error.
func NewCacheRings(ctx context.Context, cacheNodes []string, readTimeout, writeTimeout time.Duration, poolSize int) (map[string]*redis.Client, error) {
	clients := make(map[string]*redis.Client)
	for _, node := range cacheNodes {
		connString := nodeConnectionString(node)
		client, err := NewCacheClient(ctx, connString, readTimeout, writeTimeout, poolSize)
		if err != nil {
			for _, c := range clients {
				c.Close()
			}
			return nil, fmt.Errorf("connecting to cache node %s: %w", node, err)
		}
		clients[node] = client
	}

	return clients, nil
}

func nodeConnectionString(node string) string {
	return fmt.Sprintf("redis://%s/0", node)
}
