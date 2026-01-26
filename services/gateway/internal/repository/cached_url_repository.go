package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zhejian/url-shortener/gateway/internal/model"
)

type CachedURLRepository struct {
	db    *URLRepository
	cache *redis.Client
	ttl   time.Duration
}

type URLRepositoryInterface interface {
	GetByCode(ctx context.Context, code string) (*model.URL, error)
	Create(ctx context.Context, url *model.URL) error
	Delete(ctx context.Context, code string) error
}

func NewCacheClient(ctx context.Context, connString string) (*redis.Client, error) {
	opt, err := redis.ParseURL(connString)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(opt)
	// Test the connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, err
	}
	// - Return the pool
	return rdb, nil
}

// GetByCode with cache-aside pattern
func (r *CachedURLRepository) GetByCode(ctx context.Context, code string) (*model.URL, error) {
	cacheKey := fmt.Sprintf("url:%s", code)

	// 1. Try cache first (gracefully handle Redis errors)
	if r.cache != nil {
		cached, err := r.cache.Get(ctx, cacheKey).Result()

	}

	// 2. Query database
	url, err := r.db.GetByCode(ctx, code)
	if err != nil {
		// Cache negative result
		return nil, err
	}

	// 3. Store in cache
	if r.cache != nil {
		data, _ := json.Marshal(url)
		r.cache.Set(ctx, cacheKey, data, r.ttl)
	}

	return url, nil
}

// Create: write through

// Delete: delete from cache, if hit
