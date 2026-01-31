package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"
	"github.com/zhejian/url-shortener/gateway/internal/model"
)

// CachedURLRepository wraps URLRepository with Redis caching.
// It uses cache-aside for reads and write-through for writes.
type CachedURLRepository struct {
	db           URLRepositoryInterface
	cache        *redis.Client
	ttl          time.Duration
	requestGroup *singleflight.Group
	cacheCB      *gobreaker.CircuitBreaker
}

// URLRepositoryInterface defines the contract for URL storage operations.
type URLRepositoryInterface interface {
	GetByCode(ctx context.Context, code string) (*model.URL, error)
	Create(ctx context.Context, url *model.URL) error
	Delete(ctx context.Context, code string) error
}

// notFoundSentinel is cached to prevent repeated DB queries for non-existent URLs.
var notFoundSentinel = []byte("__NOT_FOUND__")

// CBSettings holds circuit breaker configuration for any external dependency.
type CBSettings struct {
	MaxRequests         uint32
	Interval            time.Duration
	Timeout             time.Duration
	ConsecutiveFailures uint32
}

// DefaultCBSettings returns production circuit breaker defaults.
func DefaultCBSettings() CBSettings {
	return CBSettings{
		MaxRequests:         3,
		Interval:            10 * time.Second,
		Timeout:             30 * time.Second,
		ConsecutiveFailures: 5,
	}
}

// CachedURLRepositoryOptions holds optional configuration.
type CachedURLRepositoryOptions struct {
	CacheCB *CBSettings
}

// NewCachedURLRepository creates a new cached URL repository.
func NewCachedURLRepository(db URLRepositoryInterface, cache *redis.Client, ttl time.Duration, opts ...CachedURLRepositoryOptions) *CachedURLRepository {
	cb := DefaultCBSettings()
	if len(opts) > 0 && opts[0].CacheCB != nil {
		cb = *opts[0].CacheCB
	}

	return &CachedURLRepository{
		db:           db,
		cache:        cache,
		ttl:          ttl,
		requestGroup: &singleflight.Group{},
		cacheCB: gobreaker.NewCircuitBreaker(gobreaker.Settings{
			Name:        "redis",
			MaxRequests: cb.MaxRequests,
			Interval:    cb.Interval,
			Timeout:     cb.Timeout,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				return counts.ConsecutiveFailures >= cb.ConsecutiveFailures
			},
			IsSuccessful: func(err error) bool {
				// redis.Nil is a cache miss, not an infrastructure failure.
				return err == nil || err == redis.Nil
			},
			OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
				log.Printf("circuit breaker [%s]: %s → %s", name, from, to)
			},
		}),
	}
}

// GetByCode retrieves a URL by short code using cache-aside pattern.
// It checks cache first, falls back to DB on miss, and caches the result.
// Non-existent URLs are negatively cached to prevent DB stampede.
func (r *CachedURLRepository) GetByCode(ctx context.Context, code string) (*model.URL, error) {
	cacheKey := fmt.Sprintf("url:%s", code)

	// Try cache first
	if r.cache != nil {
		cached, err := r.cacheGet(ctx, cacheKey)
		if err == nil {
			if cached == string(notFoundSentinel) {
				return nil, ErrNotFound
			}
			var cachedURL model.URL
			if err := json.Unmarshal([]byte(cached), &cachedURL); err == nil {
				return &cachedURL, nil
			}
			log.Println("JSON unmarshal error:", err)
		} else if err != redis.Nil && !errors.Is(err, gobreaker.ErrOpenState) {
			log.Println("Redis error:", err)
		}
	}

	// Cache miss - query database with singleflight to prevent stampede
	return queryFromDBWithSingleflight(ctx, r, code)
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
			r.cacheSet(ctx, cacheKey, data, r.ttl)
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
		r.cacheDel(ctx, cacheKey)
	}
	return nil
}

// isNotFoundError checks if the error is a not-found error.
func isNotFoundError(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// queryFromDBWithSingleflight deduplicates concurrent DB queries for the same key.
// Only the first caller executes the query; subsequent callers share its result.
// A cache re-check inside the callback handles late arrivals after a previous
// singleflight call has already completed and populated the cache.
func queryFromDBWithSingleflight(ctx context.Context, r *CachedURLRepository, code string) (*model.URL, error) {
	cacheKey := fmt.Sprintf("url:%s", code)
	res, gerr, _ := r.requestGroup.Do(cacheKey, func() (interface{}, error) {
		// Re-check cache: a previous singleflight call may have populated it
		// before this callback was invoked (double-checked locking pattern).
		if r.cache != nil {
			cached, err := r.cacheGet(ctx, cacheKey)
			if err == nil {
				if cached == string(notFoundSentinel) {
					return nil, ErrNotFound
				}
				var url model.URL
				if err := json.Unmarshal([]byte(cached), &url); err == nil {
					return &url, nil
				}
			}
		}

		// Use a context detached from the caller to prevent cancellation
		// of one request from failing all waiting callers.
		dbCtx := context.WithoutCancel(ctx)
		url, err := r.db.GetByCode(dbCtx, code)
		return rewriteCache(dbCtx, r, cacheKey, url, err)
	})

	if gerr != nil {
		return nil, gerr
	}
	url, ok := res.(*model.URL)
	if !ok {
		return nil, errors.New("unexpected type from singleflight")
	}
	return url, nil
}

// rewriteCache populates the cache after a DB query.
// On not-found errors, it caches a sentinel value to avoid repeated DB lookups.
// On success, it caches the URL with the configured TTL.
func rewriteCache(ctx context.Context, r *CachedURLRepository, cacheKey string, url *model.URL, err error) (*model.URL, error) {
	if err != nil {
		if r.cache != nil && isNotFoundError(err) {
			// Negative cache: store sentinel to prevent repeated DB queries
			r.cacheSet(ctx, cacheKey, notFoundSentinel, time.Minute)
		}
		return nil, err
	}

	// Store the URL in cache for future requests
	if r.cache != nil {
		if data, err := json.Marshal(url); err == nil {
			r.cacheSet(ctx, cacheKey, data, r.ttl)
		} else {
			log.Println("JSON marshal error:", err)
		}
	}
	return url, nil
}

func (r *CachedURLRepository) cacheGet(ctx context.Context, key string) (string, error) {
	res, err := r.cacheCB.Execute(func() (interface{}, error) {
		return r.cache.Get(ctx, key).Result()
	})
	if err != nil {
		return "", err
	}
	return res.(string), nil
}

func (r *CachedURLRepository) cacheSet(ctx context.Context, key string, data interface{}, ttl time.Duration) {
	_, err := r.cacheCB.Execute(func() (interface{}, error) {
		return nil, r.cache.Set(ctx, key, data, ttl).Err()
	})
	if err != nil && !errors.Is(err, gobreaker.ErrOpenState) {
		log.Println("Redis SET error:", err)
	}
}

func (r *CachedURLRepository) cacheDel(ctx context.Context, key string) {
	_, err := r.cacheCB.Execute(func() (interface{}, error) {
		return nil, r.cache.Del(ctx, key).Err()
	})
	if err != nil && !errors.Is(err, gobreaker.ErrOpenState) {
		log.Println("Redis DEL error:", err)
	}
}

// Compile-time check: CachedURLRepository must implement URLRepositoryInterface.
var _ URLRepositoryInterface = (*CachedURLRepository)(nil)
