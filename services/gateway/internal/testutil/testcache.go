package testutil

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	redisTC "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/zhejian/url-shortener/gateway/internal/infra"
)

// TestCache holds test cache resources
type TestCache struct {
	Client    *redis.Client
	container *redisTC.RedisContainer
}

// SetupTestCache creates a new test Redis container
func SetupTestCache(ctx context.Context) (*TestCache, error) {
	container, err := redisTC.Run(ctx,
		"redis:8-alpine",
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		return nil, err
	}

	connString, err := container.ConnectionString(ctx)
	if err != nil {
		if terr := container.Terminate(ctx); terr != nil {
			err = terr
		}
		return nil, err
	}

	client, err := infra.NewCacheClient(ctx, connString)
	if err != nil {
		if terr := container.Terminate(ctx); terr != nil {
			err = terr
		}
		return nil, err
	}

	return &TestCache{Client: client, container: container}, nil
}

// Cleanup flushes all keys from the database
func (t *TestCache) Cleanup(ctx context.Context) {
	if t == nil || t.Client == nil {
		return
	}
	t.Client.FlushDB(ctx)
}

// Container returns the underlying redis container for direct access.
func (t *TestCache) Container() *redisTC.RedisContainer {
	return t.container
}

// Teardown closes connections and terminates container
func (t *TestCache) Teardown(ctx context.Context) {
	if t.Client != nil {
		t.Client.Close()
	}
	if t.container != nil {
		if err := t.container.Terminate(ctx); err != nil {
			return
		}
	}
}
