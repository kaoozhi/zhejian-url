package repository

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zhejian/url-shortener/gateway/internal/model"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug, // Show all logs including circuit breaker transitions
	}))
}

// deadRedisClient returns a Redis client connected to a non-existent server.
// Every operation will fail, which is used to trip the circuit breaker.
func deadRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:        "localhost:59999",
		DialTimeout: 10 * time.Millisecond,
	})
}

// fastCBSettings returns circuit breaker settings suitable for testing:
// trips after 2 failures, recovers after 100ms.
func fastCBSettings() *CBSettings {
	return &CBSettings{
		MaxRequests:         1,
		Interval:            time.Second,
		Timeout:             100 * time.Millisecond,
		ConsecutiveFailures: 2,
	}
}

// testDB and testCache are declared and initialized in url_repository_test.go's TestMain

func TestCachedURLRepository_GetByCode(t *testing.T) {
	ctx := context.Background()
	cacheTTL := 5 * time.Minute

	t.Run("cache miss - fetches from db and caches", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL, newTestLogger())

		// Insert test data directly in DB
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "cachemiss", "https://example.com/cachemiss", time.Now())

		// Get should fetch from DB
		url, err := repo.GetByCode(ctx, "cachemiss")
		require.NoError(t, err)
		assert.Equal(t, "cachemiss", url.ShortCode)

		// Verify it's now cached
		cacheKey := "url:cachemiss"
		exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
		assert.Equal(t, int64(1), exists, "expected URL to be cached after fetch")
	})

	t.Run("cache hit - returns from cache without db query", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL, newTestLogger())

		// Insert and fetch to cache it
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "cachehit", "https://example.com/cachehit", time.Now())

		_, err := repo.GetByCode(ctx, "cachehit")
		require.NoError(t, err, "first fetch failed")

		// Delete from DB directly
		testDB.Pool.Exec(ctx, "DELETE FROM urls WHERE short_code = $1", "cachehit")

		// Should still return from cache
		url, err := repo.GetByCode(ctx, "cachehit")
		require.NoError(t, err, "expected cache hit")
		assert.Equal(t, "https://example.com/cachehit", url.OriginalURL)
	})

	t.Run("negative caching - caches not found", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL, newTestLogger())

		// Fetch non-existent URL
		_, err := repo.GetByCode(ctx, "notfound")
		require.ErrorIs(t, err, ErrNotFound)

		// Verify sentinel is cached
		cacheKey := "url:notfound"
		cached, err := testCache.Client.Get(ctx, cacheKey).Result()
		require.NoError(t, err, "expected cache entry")
		assert.Equal(t, "__NOT_FOUND__", cached)
	})

	t.Run("negative cache hit - returns not found without db query", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL, newTestLogger())

		// Fetch non-existent to cache negative result
		_, _ = repo.GetByCode(ctx, "negcache")

		// Insert into DB after negative cache
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "negcache", "https://example.com/negcache", time.Now())

		// Should still return not found from cache
		_, err := repo.GetByCode(ctx, "negcache")
		assert.ErrorIs(t, err, ErrNotFound, "expected ErrNotFound from negative cache")
	})

	t.Run("graceful degradation - works when cache is nil", func(t *testing.T) {
		testDB.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, nil, cacheTTL, newTestLogger()) // nil cache

		// Insert test data
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "nocache", "https://example.com/nocache", time.Now())

		// Should still work, just without caching
		url, err := repo.GetByCode(ctx, "nocache")
		require.NoError(t, err)
		assert.Equal(t, "nocache", url.ShortCode)
	})
}

func TestCachedURLRepository_Create(t *testing.T) {
	ctx := context.Background()
	cacheTTL := 5 * time.Minute

	t.Run("write-through - caches on create", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL, newTestLogger())

		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "created",
			OriginalURL: "https://example.com/created",
			CreatedAt:   time.Now(),
		}

		err := repo.Create(ctx, url)
		require.NoError(t, err)

		// Verify it's cached
		cacheKey := "url:created"
		exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
		assert.Equal(t, int64(1), exists, "expected URL to be cached after create")

		// Verify cache contains correct data
		cachedURL, err := repo.GetByCode(ctx, "created")
		require.NoError(t, err, "expected to get cached URL")
		assert.Equal(t, "https://example.com/created", cachedURL.OriginalURL)
	})

	t.Run("overwrites negative cache on create", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL, newTestLogger())

		// Fetch non-existent to create negative cache
		_, _ = repo.GetByCode(ctx, "overwrite")

		// Verify negative cache exists
		cacheKey := "url:overwrite"
		cached, _ := testCache.Client.Get(ctx, cacheKey).Result()
		require.Equal(t, "__NOT_FOUND__", cached, "expected negative cache entry")

		// Create the URL
		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "overwrite",
			OriginalURL: "https://example.com/overwrite",
			CreatedAt:   time.Now(),
		}
		err := repo.Create(ctx, url)
		require.NoError(t, err)

		// Verify negative cache is overwritten
		cached, _ = testCache.Client.Get(ctx, cacheKey).Result()
		assert.NotEqual(t, "__NOT_FOUND__", cached, "expected negative cache to be overwritten")

		// Should return the URL now
		result, err := repo.GetByCode(ctx, "overwrite")
		require.NoError(t, err, "expected URL")
		assert.Equal(t, "https://example.com/overwrite", result.OriginalURL)
	})

	t.Run("graceful degradation - works when cache is nil", func(t *testing.T) {
		testDB.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, nil, cacheTTL, newTestLogger())

		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "nocache2",
			OriginalURL: "https://example.com/nocache2",
			CreatedAt:   time.Now(),
		}

		err := repo.Create(ctx, url)
		require.NoError(t, err)

		// Verify in DB
		var count int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "nocache2").Scan(&count)
		assert.Equal(t, 1, count)
	})
}

func TestCachedURLRepository_Delete(t *testing.T) {
	ctx := context.Background()
	cacheTTL := 5 * time.Minute

	t.Run("invalidates cache on delete", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL, newTestLogger())

		// Create and cache a URL
		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "todelete",
			OriginalURL: "https://example.com/todelete",
			CreatedAt:   time.Now(),
		}
		repo.Create(ctx, url)

		// Verify it's cached
		cacheKey := "url:todelete"
		exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
		require.Equal(t, int64(1), exists, "expected URL to be cached before delete")

		// Delete
		err := repo.Delete(ctx, "todelete")
		require.NoError(t, err)

		// Verify cache invalidated
		exists, _ = testCache.Client.Exists(ctx, cacheKey).Result()
		assert.Equal(t, int64(0), exists, "expected cache to be invalidated after delete")
	})

	t.Run("delete non-existent does not create cache entry", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL, newTestLogger())

		// Try to delete non-existent
		err := repo.Delete(ctx, "nonexistent")
		require.ErrorIs(t, err, ErrNotFound)

		// Verify no cache entry created
		cacheKey := "url:nonexistent"
		exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
		assert.Equal(t, int64(0), exists, "expected no cache entry for failed delete")
	})

	t.Run("graceful degradation - works when cache is nil", func(t *testing.T) {
		testDB.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, nil, cacheTTL, newTestLogger())

		// Insert directly
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "nocache3", "https://example.com/nocache3", time.Now())

		// Delete should work
		err := repo.Delete(ctx, "nocache3")
		require.NoError(t, err)

		// Verify deleted from DB
		var count int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "nocache3").Scan(&count)
		assert.Equal(t, 0, count)
	})
}

func TestCachedURLRepository_CacheTTL(t *testing.T) {
	ctx := context.Background()

	t.Run("cache entry has correct TTL", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		cacheTTL := 10 * time.Minute
		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL, newTestLogger())

		// Create a URL
		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "ttltest",
			OriginalURL: "https://example.com/ttltest",
			CreatedAt:   time.Now(),
		}
		repo.Create(ctx, url)

		// Check TTL
		cacheKey := "url:ttltest"
		ttl, err := testCache.Client.TTL(ctx, cacheKey).Result()
		require.NoError(t, err, "failed to get TTL")

		// TTL should be close to cacheTTL (within 1 second tolerance)
		assert.True(t, ttl >= cacheTTL-time.Second && ttl <= cacheTTL, "expected TTL close to %v, got %v", cacheTTL, ttl)
	})
}

type countingRepository struct {
	URLRepositoryInterface
	getByCodeCount atomic.Int32
}

func (c *countingRepository) GetByCode(ctx context.Context, code string) (*model.URL, error) {
	c.getByCodeCount.Add(1)
	return c.URLRepositoryInterface.GetByCode(ctx, code)
}

func TestCachedURLRepository_SingleFlight(t *testing.T) {
	ctx := context.Background()

	t.Run("singleflight deduplicates concurrent DB queries", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		cacheTTL := 10 * time.Minute
		dbRepo := NewURLRepository(testDB.Pool)
		counter := &countingRepository{URLRepositoryInterface: dbRepo}
		repo := NewCachedURLRepository(counter, testCache.Client, cacheTTL, newTestLogger())

		// Insert test data (cache is cold)
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "sftest", "https://example.com/sftest", time.Now())

		// Launch N concurrent requests for the same code against a cold cache
		const n = 10
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, n)

		for i := range n {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start // wait for all goroutines to be ready
				_, errs[idx] = repo.GetByCode(ctx, "sftest")
			}(i)
		}

		close(start) // release all goroutines simultaneously
		wg.Wait()

		for i, err := range errs {
			assert.NoError(t, err, "goroutine %d got error", i)
		}

		assert.Equal(t, int32(1), counter.getByCodeCount.Load(), "expected 1 DB query (singleflight)")
	})
}

// tripCircuitBreaker makes enough failing calls to open the circuit breaker.
// It calls GetByCode on a non-existent code so that every call attempts
// a Redis GET (which fails on the dead client), tripping the CB.
func tripCircuitBreaker(ctx context.Context, repo *CachedURLRepository) {
	for i := 0; i < 3; i++ {
		repo.GetByCode(ctx, "tripCB")
	}
}

func TestCachedURLRepository_CircuitBreaker(t *testing.T) {
	ctx := context.Background()
	cacheTTL := 5 * time.Minute
	cbOpts := CachedURLRepositoryOptions{CacheCB: fastCBSettings()}

	t.Run("GetByCode falls back to DB when circuit opens", func(t *testing.T) {
		testDB.Cleanup(ctx)

		badRedis := deadRedisClient()
		defer badRedis.Close()

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, badRedis, cacheTTL, newTestLogger(), cbOpts)

		// Insert test data
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "cbget", "https://example.com/cbget", time.Now())

		// Trip the circuit breaker
		tripCircuitBreaker(ctx, repo)

		// Should still return data from DB despite open circuit
		url, err := repo.GetByCode(ctx, "cbget")
		require.NoError(t, err, "expected DB fallback, got error: %v", err)
		assert.Equal(t, "cbget", url.ShortCode, "expected short_code 'cbget', got '%s'", url.ShortCode)
	})

	t.Run("Create succeeds when circuit is open", func(t *testing.T) {
		testDB.Cleanup(ctx)

		badRedis := deadRedisClient()
		defer badRedis.Close()

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, badRedis, cacheTTL, newTestLogger(), cbOpts)

		// Trip the circuit breaker
		tripCircuitBreaker(ctx, repo)

		// Create should succeed (DB write works, cache write silently fails)
		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "cbcreate",
			OriginalURL: "https://example.com/cbcreate",
			CreatedAt:   time.Now(),
		}
		err := repo.Create(ctx, url)
		require.NoError(t, err, "expected no error, got %v", err)

		// Verify data exists in DB
		var count int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "cbcreate").Scan(&count)
		assert.Equal(t, 1, count, "expected 1 row in DB, got %d", count)
	})

	t.Run("Delete succeeds when circuit is open", func(t *testing.T) {
		testDB.Cleanup(ctx)

		badRedis := deadRedisClient()
		defer badRedis.Close()

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, badRedis, cacheTTL, newTestLogger(), cbOpts)

		// Insert test data
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "cbdelete", "https://example.com/cbdelete", time.Now())

		// Trip the circuit breaker
		tripCircuitBreaker(ctx, repo)

		// Delete should succeed (DB delete works, cache invalidation silently fails)
		err := repo.Delete(ctx, "cbdelete")
		require.NoError(t, err, "expected no error, got %v", err)

		// Verify deleted from DB
		var count int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "cbdelete").Scan(&count)
		assert.Equal(t, 0, count, "expected 0 rows in DB, got %d", count)
	})

	t.Run("circuit breaker recovers after timeout", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		badRedis := deadRedisClient()
		defer badRedis.Close()

		dbRepo := NewURLRepository(testDB.Pool)
		// Use dead Redis to trip the breaker, with MaxRequests=3 so
		// half-open allows enough calls for a full GetByCode cycle
		// (cacheGet in GetByCode + cacheGet re-check + cacheSet in rewriteCache).
		recoverOpts := CachedURLRepositoryOptions{CacheCB: &CBSettings{
			MaxRequests:         3,
			Interval:            time.Second,
			Timeout:             100 * time.Millisecond,
			ConsecutiveFailures: 2,
		}}
		repo := NewCachedURLRepository(dbRepo, badRedis, cacheTTL, newTestLogger(), recoverOpts)

		// Insert test data
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "cbrecover", "https://example.com/cbrecover", time.Now())

		// Trip the circuit breaker
		tripCircuitBreaker(ctx, repo)

		// Swap to a working Redis by replacing the cache field.
		// This simulates Redis coming back online.
		repo.cache = testCache.Client

		// Wait for CB timeout to enter half-open state
		time.Sleep(150 * time.Millisecond)

		// This call should succeed: CB is half-open, Redis is now alive,
		// so cacheGet succeeds (miss) → DB query → cacheSet succeeds → CB closes.
		url, err := repo.GetByCode(ctx, "cbrecover")
		require.NoError(t, err, "expected recovery, got error: %v", err)
		assert.Equal(t, "cbrecover", url.ShortCode, "expected short_code 'cbrecover', got '%s'", url.ShortCode)

		// Verify the URL is now cached (CB recovered, cacheSet succeeded)
		cacheKey := "url:cbrecover"
		exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
		assert.Equal(t, int64(1), exists, "expected URL to be cached after CB recovery")
	})
}
