package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zhejian/url-shortener/gateway/internal/model"
)

// testDB and testCache are declared and initialized in url_repository_test.go's TestMain

func TestCachedURLRepository_GetByCode(t *testing.T) {
	ctx := context.Background()
	cacheTTL := 5 * time.Minute

	t.Run("cache miss - fetches from db and caches", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)

		// Insert test data directly in DB
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "cachemiss", "https://example.com/cachemiss", time.Now())

		// Get should fetch from DB
		url, err := repo.GetByCode(ctx, "cachemiss")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if url.ShortCode != "cachemiss" {
			t.Errorf("expected short_code 'cachemiss', got '%s'", url.ShortCode)
		}

		// Verify it's now cached
		cacheKey := "url:cachemiss"
		exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
		if exists != 1 {
			t.Error("expected URL to be cached after fetch")
		}
	})

	t.Run("cache hit - returns from cache without db query", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)

		// Insert and fetch to cache it
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "cachehit", "https://example.com/cachehit", time.Now())

		_, err := repo.GetByCode(ctx, "cachehit")
		if err != nil {
			t.Fatalf("first fetch failed: %v", err)
		}

		// Delete from DB directly
		testDB.Pool.Exec(ctx, "DELETE FROM urls WHERE short_code = $1", "cachehit")

		// Should still return from cache
		url, err := repo.GetByCode(ctx, "cachehit")
		if err != nil {
			t.Fatalf("expected cache hit, got error: %v", err)
		}
		if url.OriginalURL != "https://example.com/cachehit" {
			t.Errorf("expected cached URL, got %s", url.OriginalURL)
		}
	})

	t.Run("negative caching - caches not found", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)

		// Fetch non-existent URL
		_, err := repo.GetByCode(ctx, "notfound")
		if err != ErrNotFound {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}

		// Verify sentinel is cached
		cacheKey := "url:notfound"
		cached, err := testCache.Client.Get(ctx, cacheKey).Result()
		if err != nil {
			t.Fatalf("expected cache entry, got error: %v", err)
		}
		if cached != "__NOT_FOUND__" {
			t.Errorf("expected sentinel '__NOT_FOUND__', got '%s'", cached)
		}
	})

	t.Run("negative cache hit - returns not found without db query", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)

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
		if err != ErrNotFound {
			t.Errorf("expected ErrNotFound from negative cache, got %v", err)
		}
	})

	t.Run("graceful degradation - works when cache is nil", func(t *testing.T) {
		testDB.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, nil, cacheTTL) // nil cache

		// Insert test data
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "nocache", "https://example.com/nocache", time.Now())

		// Should still work, just without caching
		url, err := repo.GetByCode(ctx, "nocache")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if url.ShortCode != "nocache" {
			t.Errorf("expected short_code 'nocache', got '%s'", url.ShortCode)
		}
	})
}

func TestCachedURLRepository_Create(t *testing.T) {
	ctx := context.Background()
	cacheTTL := 5 * time.Minute

	t.Run("write-through - caches on create", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)

		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "created",
			OriginalURL: "https://example.com/created",
			CreatedAt:   time.Now(),
		}

		err := repo.Create(ctx, url)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify it's cached
		cacheKey := "url:created"
		exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
		if exists != 1 {
			t.Error("expected URL to be cached after create")
		}

		// Verify cache contains correct data
		cachedURL, err := repo.GetByCode(ctx, "created")
		if err != nil {
			t.Fatalf("expected to get cached URL, got error: %v", err)
		}
		if cachedURL.OriginalURL != "https://example.com/created" {
			t.Errorf("expected cached URL to match, got %s", cachedURL.OriginalURL)
		}
	})

	t.Run("overwrites negative cache on create", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)

		// Fetch non-existent to create negative cache
		_, _ = repo.GetByCode(ctx, "overwrite")

		// Verify negative cache exists
		cacheKey := "url:overwrite"
		cached, _ := testCache.Client.Get(ctx, cacheKey).Result()
		if cached != "__NOT_FOUND__" {
			t.Fatal("expected negative cache entry")
		}

		// Create the URL
		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "overwrite",
			OriginalURL: "https://example.com/overwrite",
			CreatedAt:   time.Now(),
		}
		err := repo.Create(ctx, url)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify negative cache is overwritten
		cached, _ = testCache.Client.Get(ctx, cacheKey).Result()
		if cached == "__NOT_FOUND__" {
			t.Error("expected negative cache to be overwritten")
		}

		// Should return the URL now
		result, err := repo.GetByCode(ctx, "overwrite")
		if err != nil {
			t.Fatalf("expected URL, got error: %v", err)
		}
		if result.OriginalURL != "https://example.com/overwrite" {
			t.Errorf("expected correct URL, got %s", result.OriginalURL)
		}
	})

	t.Run("graceful degradation - works when cache is nil", func(t *testing.T) {
		testDB.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, nil, cacheTTL)

		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "nocache2",
			OriginalURL: "https://example.com/nocache2",
			CreatedAt:   time.Now(),
		}

		err := repo.Create(ctx, url)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify in DB
		var count int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "nocache2").Scan(&count)
		if count != 1 {
			t.Errorf("expected 1 row, got %d", count)
		}
	})
}

func TestCachedURLRepository_Delete(t *testing.T) {
	ctx := context.Background()
	cacheTTL := 5 * time.Minute

	t.Run("invalidates cache on delete", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)

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
		if exists != 1 {
			t.Fatal("expected URL to be cached before delete")
		}

		// Delete
		err := repo.Delete(ctx, "todelete")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify cache invalidated
		exists, _ = testCache.Client.Exists(ctx, cacheKey).Result()
		if exists != 0 {
			t.Error("expected cache to be invalidated after delete")
		}
	})

	t.Run("delete non-existent does not create cache entry", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)

		// Try to delete non-existent
		err := repo.Delete(ctx, "nonexistent")
		if err != ErrNotFound {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}

		// Verify no cache entry created
		cacheKey := "url:nonexistent"
		exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
		if exists != 0 {
			t.Error("expected no cache entry for failed delete")
		}
	})

	t.Run("graceful degradation - works when cache is nil", func(t *testing.T) {
		testDB.Cleanup(ctx)

		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, nil, cacheTTL)

		// Insert directly
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, "nocache3", "https://example.com/nocache3", time.Now())

		// Delete should work
		err := repo.Delete(ctx, "nocache3")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify deleted from DB
		var count int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "nocache3").Scan(&count)
		if count != 0 {
			t.Errorf("expected 0 rows, got %d", count)
		}
	})
}

func TestCachedURLRepository_CacheTTL(t *testing.T) {
	ctx := context.Background()

	t.Run("cache entry has correct TTL", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		cacheTTL := 10 * time.Minute
		dbRepo := NewURLRepository(testDB.Pool)
		repo := NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)

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
		if err != nil {
			t.Fatalf("failed to get TTL: %v", err)
		}

		// TTL should be close to cacheTTL (within 1 second tolerance)
		if ttl < cacheTTL-time.Second || ttl > cacheTTL {
			t.Errorf("expected TTL close to %v, got %v", cacheTTL, ttl)
		}
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
		repo := NewCachedURLRepository(counter, testCache.Client, cacheTTL)

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

		for i := 0; i < n; i++ {
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
			if err != nil {
				t.Errorf("goroutine %d got error: %v", i, err)
			}
		}

		if val := counter.getByCodeCount.Load(); val != 1 {
			t.Errorf("expected 1 DB query (singleflight), got %d", val)
		}
	})
}
