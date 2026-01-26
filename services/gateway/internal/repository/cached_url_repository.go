package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zhejian/url-shortener/gateway/internal/model"
)

// CachedURLRepository wraps URLRepository with Redis caching.
// It uses cache-aside for reads and write-through for writes.
type CachedURLRepository struct {
	db    *URLRepository
	cache *redis.Client
	ttl   time.Duration
}

// URLRepositoryInterface defines the contract for URL storage operations.
type URLRepositoryInterface interface {
	GetByCode(ctx context.Context, code string) (*model.URL, error)
	Create(ctx context.Context, url *model.URL) error
	Delete(ctx context.Context, code string) error
}

// notFoundSentinel is cached to prevent repeated DB queries for non-existent URLs.
var notFoundSentinel = []byte("__NOT_FOUND__")

// NewCachedURLRepository creates a new cached URL repository.
func NewCachedURLRepository(db *URLRepository, cache *redis.Client, ttl time.Duration) *CachedURLRepository {
	return &CachedURLRepository{db: db, cache: cache, ttl: ttl}
}

// NewCacheClient creates a new Redis client and verifies connectivity.
func NewCacheClient(ctx context.Context, connString string) (*redis.Client, error) {
	opt, err := redis.ParseURL(connString)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(opt)
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, err
	}
	return rdb, nil
}

// GetByCode retrieves a URL by short code using cache-aside pattern.
// It checks cache first, falls back to DB on miss, and caches the result.
// Non-existent URLs are negatively cached to prevent DB stampede.
func (r *CachedURLRepository) GetByCode(ctx context.Context, code string) (*model.URL, error) {
	cacheKey := fmt.Sprintf("url:%s", code)
	var cachedURL model.URL

	// Try cache first
	if r.cache != nil {
		cached, err := r.cache.Get(ctx, cacheKey).Result()
		if err == nil {
			if cached == string(notFoundSentinel) {
				return nil, ErrNotFound
			}
			if err := json.Unmarshal([]byte(cached), &cachedURL); err == nil {
				return &cachedURL, nil
			}
			log.Println("JSON unmarshal error:", err)
		} else if err != redis.Nil {
			log.Println("Redis error:", err)
		}
	}

	// Cache miss - query database
	url, err := r.db.GetByCode(ctx, code)
	if err != nil {
		if r.cache != nil && isNotFoundError(err) {
			r.cache.Set(ctx, cacheKey, notFoundSentinel, time.Minute)
		}
		return nil, err
	}

	// Cache the result
	if r.cache != nil {
		if data, err := json.Marshal(url); err == nil {
			r.cache.Set(ctx, cacheKey, data, r.ttl)
		} else {
			log.Println("JSON marshal error:", err)
		}
	}

	return url, nil
}

// Create stores a new URL using write-through pattern.
// It writes to DB first, then caches the result.
func (r *CachedURLRepository) Create(ctx context.Context, url *model.URL) error {
	if err := r.db.Create(ctx, url); err != nil {
		return err
	}

	if r.cache != nil {
		cacheKey := fmt.Sprintf("url:%s", url.ShortCode)
		if data, err := json.Marshal(url); err == nil {
			r.cache.Set(ctx, cacheKey, data, r.ttl)
		} else {
			log.Println("JSON marshal error:", err)
		}
	}
	return nil
}

// Delete removes a URL from DB and invalidates the cache entry.
func (r *CachedURLRepository) Delete(ctx context.Context, code string) error {
	if err := r.db.Delete(ctx, code); err != nil {
		return err
	}

	if r.cache != nil {
		cacheKey := fmt.Sprintf("url:%s", code)
		if err := r.cache.Del(ctx, cacheKey).Err(); err != nil && err != redis.Nil {
			log.Println("Redis error:", err)
		}
	}
	return nil
}

func isNotFoundError(err error) bool {
	return errors.Is(err, ErrNotFound)
}

var _ URLRepositoryInterface = (*CachedURLRepository)(nil)
