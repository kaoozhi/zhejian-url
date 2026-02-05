package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/singleflight"

	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"
	"github.com/zhejian/url-shortener/gateway/internal/model"
)

var tracer = otel.Tracer("gateway/repository")

// CachedURLRepository wraps URLRepository with Redis caching.
// It uses cache-aside for reads and write-through for writes.
type CachedURLRepository struct {
	db           URLRepositoryInterface
	cache        *redis.Client
	ttl          time.Duration
	requestGroup *singleflight.Group
	cacheCB      *gobreaker.CircuitBreaker
	logger       *slog.Logger
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
func NewCachedURLRepository(db URLRepositoryInterface, cache *redis.Client, ttl time.Duration, logger *slog.Logger, opts ...CachedURLRepositoryOptions) *CachedURLRepository {
	cb := DefaultCBSettings()
	if len(opts) > 0 && opts[0].CacheCB != nil {
		cb = *opts[0].CacheCB
	}

	repo := &CachedURLRepository{
		db:           db,
		cache:        cache,
		ttl:          ttl,
		requestGroup: &singleflight.Group{},
		logger:       logger,
	}

	repo.cacheCB = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "redis",
		MaxRequests: cb.MaxRequests,
		Interval:    cb.Interval,
		Timeout:     cb.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			shouldTrip := counts.ConsecutiveFailures >= cb.ConsecutiveFailures
			if shouldTrip {
				repo.logger.Error("circuit breaker about to trip",
					slog.String("name", "redis"),
					slog.Uint64("requests", uint64(counts.Requests)),
					slog.Uint64("total_successes", uint64(counts.TotalSuccesses)),
					slog.Uint64("total_failures", uint64(counts.TotalFailures)),
					slog.Uint64("consecutive_successes", uint64(counts.ConsecutiveSuccesses)),
					slog.Uint64("consecutive_failures", uint64(counts.ConsecutiveFailures)))
			}
			return shouldTrip
		},
		IsSuccessful: func(err error) bool {
			// redis.Nil is a cache miss, not an infrastructure failure.
			return err == nil || err == redis.Nil
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logLevel := slog.LevelWarn
			message := "circuit breaker state change"

			// Use different log levels based on transition
			switch to {
			case gobreaker.StateOpen:
				logLevel = slog.LevelError
				message = "circuit breaker OPENED - failing fast"
			case gobreaker.StateClosed:
				if from == gobreaker.StateHalfOpen {
					logLevel = slog.LevelInfo
					message = "circuit breaker RECOVERED"
				}
			case gobreaker.StateHalfOpen:
				logLevel = slog.LevelWarn
				message = "circuit breaker testing recovery"
			}

			repo.logger.Log(context.Background(), logLevel, message,
				slog.String("name", name),
				slog.String("from", from.String()),
				slog.String("to", to.String()))
		},
	})

	return repo
}

// GetByCode retrieves a URL by short code using cache-aside pattern.
// It checks cache first, falls back to DB on miss, and caches the result.
// Non-existent URLs are negatively cached to prevent DB stampede.
func (r *CachedURLRepository) GetByCode(ctx context.Context, code string) (*model.URL, error) {
	cacheKey := fmt.Sprintf("url:%s", code)

	// Try cache first
	if r.cache != nil {
		// Start span for cache lookup
		ctx, span := tracer.Start(ctx, "cache.get",
			trace.WithAttributes(
				attribute.String("db.system", "redis"),
				attribute.String("db.operation", "GET"),
				attribute.String("cache.key", cacheKey),
			),
		)
		cached, err := r.cacheGet(ctx, cacheKey)
		if err == nil {
			if cached == string(notFoundSentinel) {
				span.SetAttributes(attribute.Bool("cache.hit", true))
				span.SetAttributes(attribute.Bool("cache.negative", true))
				span.End()
				return nil, ErrNotFound
			}
			var cachedURL model.URL
			if err := json.Unmarshal([]byte(cached), &cachedURL); err == nil {
				span.SetAttributes(attribute.Bool("cache.hit", true))
				span.End()
				return &cachedURL, nil
			}
			span.RecordError(err)
			r.logger.Error("cache deserialization error",
				slog.Any("error", err),
				slog.String("key", cacheKey))
		} else if err != redis.Nil && !errors.Is(err, gobreaker.ErrOpenState) {
			span.RecordError(err)
			r.logger.Error("cache read error",
				slog.Any("error", err),
				slog.String("key", cacheKey))
		}
		span.SetAttributes(attribute.Bool("cache.hit", false))
		span.End()
	}

	// Cache miss - query database with singleflight to prevent stampede
	return queryFromDBWithSingleflight(ctx, r, code)
}

// Create stores a new URL using write-through pattern.
// It writes to DB first, then caches the result.
func (r *CachedURLRepository) Create(ctx context.Context, url *model.URL) error {
	ctx, span := tracer.Start(ctx, "db.insert",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", "INSERT"),
			attribute.String("short_code", url.ShortCode),
		),
	)

	if err := r.db.Create(ctx, url); err != nil {
		span.RecordError(err)
		span.End()
		return err
	}
	span.End()

	if r.cache != nil {
		cacheKey := fmt.Sprintf("url:%s", url.ShortCode)
		ctx, span := tracer.Start(ctx, "cache.set",
			trace.WithAttributes(
				attribute.String("db.system", "redis"),
				attribute.String("db.operation", "SET"),
				attribute.String("cache.key", cacheKey),
			),
		)
		if data, err := json.Marshal(url); err == nil {
			r.cacheSet(ctx, cacheKey, data, r.ttl)
		} else {
			span.RecordError(err)
			r.logger.Error("cache serialization error on create",
				slog.String("error", err.Error()),
				slog.String("short_code", url.ShortCode))
		}
		span.End()
	}
	return nil
}

// Delete removes a URL from DB and invalidates the cache entry.
func (r *CachedURLRepository) Delete(ctx context.Context, code string) error {
	ctx, span := tracer.Start(ctx, "db.delete",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", "DELETE"),
			attribute.String("short_code", code),
		),
	)
	if err := r.db.Delete(ctx, code); err != nil {
		span.RecordError(err)
		span.End()
		return err
	}
	span.End()

	if r.cache != nil {
		cacheKey := fmt.Sprintf("url:%s", code)
		ctx, span := tracer.Start(ctx, "cache.delete",
			trace.WithAttributes(
				attribute.String("db.system", "redis"),
				attribute.String("db.operation", "DELETE"),
				attribute.String("cache.Key", cacheKey),
			),
		)
		r.cacheDel(ctx, cacheKey)
		span.End()
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
			r.logger.Error("cache serialization error on rewrite",
				slog.String("error", err.Error()),
				slog.String("key", cacheKey))
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
		r.logger.Error("cache write error",
			slog.String("error", err.Error()),
			slog.String("key", key))
	}
}

func (r *CachedURLRepository) cacheDel(ctx context.Context, key string) {
	_, err := r.cacheCB.Execute(func() (interface{}, error) {
		return nil, r.cache.Del(ctx, key).Err()
	})
	if err != nil && !errors.Is(err, gobreaker.ErrOpenState) {
		r.logger.Error("cache delete error",
			slog.String("error", err.Error()),
			slog.String("key", key))
	}
}

// Compile-time check: CachedURLRepository must implement URLRepositoryInterface.
var _ URLRepositoryInterface = (*CachedURLRepository)(nil)
