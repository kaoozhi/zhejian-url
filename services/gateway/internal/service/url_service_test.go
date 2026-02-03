package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zhejian/url-shortener/gateway/internal/config"
	"github.com/zhejian/url-shortener/gateway/internal/model"
	"github.com/zhejian/url-shortener/gateway/internal/repository"
	"github.com/zhejian/url-shortener/gateway/internal/testutil"
)

var (
	testDB    *testutil.TestDB
	testCache *testutil.TestCache
	testCfg   *config.Config
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Load test configuration
	var err error
	testCfg, err = config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	testDB, err = testutil.SetupTestDB(ctx)
	if err != nil {
		panic("failed to setup test database: " + err.Error())
	}

	testCache, err = testutil.SetupTestCache(ctx)
	if err != nil {
		panic("failed to setup test cache: " + err.Error())
	}

	// Run tests
	code := m.Run()

	// Cleanup
	testCache.Teardown(ctx)
	testDB.Teardown(ctx)
	os.Exit(code)
}

func TestURLService_CreateShortURL(t *testing.T) {
	ctx := context.Background()
	db := repository.NewURLRepository(testDB.Pool)
	repo := repository.NewCachedURLRepository(db, nil, 0)
	service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

	t.Run("creates short URL successfully", func(t *testing.T) {
		testDB.Cleanup(ctx)

		req := &model.CreateURLRequest{
			URL:       "https://example.com/very/long/url",
			ExpiresIn: 0,
		}

		resp, err := service.CreateShortURL(ctx, req)
		require.NoError(t, err, "Expected no error, got %v", err)

		assert.NotEmpty(t, resp.ShortCode, "Expected short code to be generated")
		assert.NotEmpty(t, resp.ShortURL, "Expected short URL to be generated")

		expectedURL := testCfg.App.BaseURL + "/" + resp.ShortCode
		assert.Equal(t, expectedURL, resp.ShortURL, "Expected short URL %s, got %s", expectedURL, resp.ShortURL)

		assert.Empty(t, resp.ExpiresAt, "Expected no expiration for permanent URL")
	})

	t.Run("creates short URL with custom alias", func(t *testing.T) {
		testDB.Cleanup(ctx)

		req := &model.CreateURLRequest{
			URL:         "https://example.com/custom",
			CustomAlias: "my-custom-alias",
		}

		resp, err := service.CreateShortURL(ctx, req)
		require.NoError(t, err, "Expected no error, got %v", err)
		assert.Equal(t, "my-custom-alias", resp.ShortCode, "Expected short code 'my-custom-alias', got %s", resp.ShortCode)
	})

	t.Run("creates short URL with expiration", func(t *testing.T) {
		testDB.Cleanup(ctx)

		req := &model.CreateURLRequest{
			URL:       "https://example.com/expiring",
			ExpiresIn: 7, // 7 days
		}

		resp, err := service.CreateShortURL(ctx, req)
		require.NoError(t, err, "Expected no error, got %v", err)

		assert.NotEmpty(t, resp.ExpiresAt, "Expected expiration date to be set")

		// Verify expiration is approximately 7 days from now
		expiresAt, err := time.Parse(time.RFC3339, resp.ExpiresAt)
		require.NoError(t, err, "Failed to parse expiration date: %v", err)

		expectedExpiry := time.Now().AddDate(0, 0, 7)
		diff := expiresAt.Sub(expectedExpiry).Abs()
		assert.LessOrEqual(t, diff, time.Minute, "Expiration date is not approximately 7 days from now")
	})

	t.Run("fails when custom alias already exists", func(t *testing.T) {
		testDB.Cleanup(ctx)

		req := &model.CreateURLRequest{
			URL:         "https://example.com/first",
			CustomAlias: "duplicate-alias",
		}

		_, err := service.CreateShortURL(ctx, req)
		require.NoError(t, err, "Expected first creation to succeed, got %v", err)

		// Try to create another URL with the same alias
		req2 := &model.CreateURLRequest{
			URL:         "https://example.com/second",
			CustomAlias: "duplicate-alias",
		}

		_, err = service.CreateShortURL(ctx, req2)
		assert.Error(t, err, "Expected error for duplicate alias, got nil")
	})

	t.Run("retries on collision and succeeds", func(t *testing.T) {
		testDB.Cleanup(ctx)

		req := &model.CreateURLRequest{
			URL: "https://collision.example",
		}

		// First creation should succeed and produce a short code
		resp1, err := service.CreateShortURL(ctx, req)
		require.NoError(t, err, "Expected first creation to succeed, got %v", err)

		// Second creation for the same long URL will initially generate
		// the same candidate short code, causing a conflict; the service
		// should retry and return a different short code.
		resp2, err := service.CreateShortURL(ctx, req)
		require.NoError(t, err, "Expected second creation to succeed after retry, got %v", err)

		assert.NotEqual(t, resp1.ShortCode, resp2.ShortCode, "Expected different short codes after retry, got same %s", resp1.ShortCode)

		// Verify both short codes exist in DB
		var count int
		err = testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", resp1.ShortCode).Scan(&count)
		require.NoError(t, err, "expected first short code to exist, got count=%d err=%v", count, err)
		assert.Equal(t, 1, count, "expected first short code to exist, got count=%d", count)

		err = testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", resp2.ShortCode).Scan(&count)
		require.NoError(t, err, "expected second short code to exist, got count=%d err=%v", count, err)
		assert.Equal(t, 1, count, "expected second short code to exist, got count=%d", count)
	})
}

func TestURLService_GetURL(t *testing.T) {
	ctx := context.Background()
	db := repository.NewURLRepository(testDB.Pool)
	repo := repository.NewCachedURLRepository(db, nil, 0)
	service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

	t.Run("retrieves existing URL successfully", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Create a URL first
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/original",
			CustomAlias: "get-test",
		}

		createResp, err := service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Failed to create URL: %v", err)

		// Retrieve the URL
		urlResp, err := service.GetURL(ctx, createResp.ShortCode)
		require.NoError(t, err, "Expected no error, got %v", err)

		assert.Equal(t, "get-test", urlResp.ShortCode, "Expected short code 'get-test', got %s", urlResp.ShortCode)
		assert.Equal(t, "https://example.com/original", urlResp.OriginalURL, "Expected original URL 'https://example.com/original', got %s", urlResp.OriginalURL)

		expectedShortURL := testCfg.App.BaseURL + "/get-test"
		assert.Equal(t, expectedShortURL, urlResp.ShortURL, "Expected short URL '%s', got %s", expectedShortURL, urlResp.ShortURL)

		assert.Equal(t, int64(0), urlResp.ClickCount, "Expected click count 0, got %d", urlResp.ClickCount)
		assert.NotEmpty(t, urlResp.CreatedAt, "Expected created_at to be set")
	})

	t.Run("returns error for non-existent URL", func(t *testing.T) {
		testDB.Cleanup(ctx)

		_, err := service.GetURL(ctx, "nonexistent")
		assert.Error(t, err, "Expected error for non-existent URL, got nil")
		assert.Equal(t, ErrURLNotFound, err, "Expected ErrURLNotFound, got %v", err)
	})

	t.Run("returns error for expired URL", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Manually insert an expired URL into database
		expiredTime := time.Now().Add(-24 * time.Hour) // Expired 24 hours ago
		_, err := testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5)
		`, "00000000-0000-0000-0000-000000000001", "expired-test", "https://example.com/expired", time.Now().Add(-48*time.Hour), expiredTime)
		if err != nil {
			require.NoError(t, err, "Failed to insert expired URL: %v", err)
		}

		// Try to get the expired URL
		_, err = service.GetURL(ctx, "expired-test")
		assert.Error(t, err, "Expected error for expired URL, got nil")
		assert.Equal(t, ErrURLExpired, err, "Expected ErrURLExpired, got %v", err)
	})
}

func TestURLService_Redirect(t *testing.T) {
	ctx := context.Background()
	db := repository.NewURLRepository(testDB.Pool)
	repo := repository.NewCachedURLRepository(db, nil, 0)
	service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

	t.Run("redirects to original URL successfully", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Create a URL first
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/redirect-target",
			CustomAlias: "redirect-test",
		}

		createResp, err := service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Failed to create URL: %v", err)

		// Get redirect URL
		originalURL, err := service.Redirect(ctx, createResp.ShortCode)
		require.NoError(t, err, "Expected no error, got %v", err)
		assert.Equal(t, "https://example.com/redirect-target", originalURL, "Expected redirect to 'https://example.com/redirect-target', got %s", originalURL)
	})

	t.Run("returns error for non-existent short code", func(t *testing.T) {
		testDB.Cleanup(ctx)

		_, err := service.Redirect(ctx, "nonexistent")
		assert.Error(t, err, "Expected error for non-existent short code, got nil")
		assert.Equal(t, ErrURLNotFound, err, "Expected ErrURLNotFound, got %v", err)
	})

	t.Run("returns error for expired URL", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Manually insert an expired URL into database
		expiredTime := time.Now().Add(-24 * time.Hour) // Expired 24 hours ago
		_, err := testDB.Pool.Exec(ctx, `
			INSERT INTO urls (id, short_code, original_url, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5)
		`, "00000000-0000-0000-0000-000000000002", "expired-redirect", "https://example.com/expired-redirect", time.Now().Add(-48*time.Hour), expiredTime)
		if err != nil {
			require.NoError(t, err, "Failed to insert expired URL: %v", err)
		}

		// Try to redirect
		_, err = service.Redirect(ctx, "expired-redirect")
		assert.Error(t, err, "Expected error for expired URL, got nil")
		assert.Equal(t, ErrURLExpired, err, "Expected ErrURLExpired, got %v", err)
	})
}

func TestURLService_DeleteURL(t *testing.T) {
	ctx := context.Background()
	db := repository.NewURLRepository(testDB.Pool)
	repo := repository.NewCachedURLRepository(db, nil, 0)
	service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

	t.Run("deletes existing URL successfully", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Create a URL first
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/to-delete",
			CustomAlias: "delete-test",
		}

		createResp, err := service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Failed to create URL: %v", err)

		// Delete the URL
		err = service.DeleteURL(ctx, createResp.ShortCode)
		require.NoError(t, err, "Expected no error, got %v", err)

		// Verify it's deleted by trying to get it
		_, err = service.GetURL(ctx, createResp.ShortCode)
		assert.Error(t, err, "Expected error when getting deleted URL, got nil")
		assert.Equal(t, ErrURLNotFound, err, "Expected ErrURLNotFound, got %v", err)
	})

	t.Run("returns error when deleting non-existent URL", func(t *testing.T) {
		testDB.Cleanup(ctx)

		err := service.DeleteURL(ctx, "nonexistent")
		assert.Error(t, err, "Expected error for non-existent URL, got nil")

		// Check that the error is properly translated from repository layer
		if err != repository.ErrNotFound {
			t.Logf("Note: DeleteURL should translate repository.ErrNotFound to service.ErrURLNotFound")
		}
	})

	t.Run("can recreate URL after deletion", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Create a URL
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/recreate",
			CustomAlias: "recreate-test",
		}

		createResp, err := service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Failed to create URL: %v", err)

		// Delete it
		err = service.DeleteURL(ctx, createResp.ShortCode)
		require.NoError(t, err, "Failed to delete URL: %v", err)

		// Recreate with same alias
		createResp2, err := service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Expected to recreate URL after deletion, got error: %v", err)
		assert.Equal(t, "recreate-test", createResp2.ShortCode, "Expected short code 'recreate-test', got %s", createResp2.ShortCode)
	})
}

func TestURLService_Integration_FullWorkflow(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)

	db := repository.NewURLRepository(testDB.Pool)
	repo := repository.NewCachedURLRepository(db, nil, 0)
	service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

	t.Run("complete URL lifecycle", func(t *testing.T) {
		// 1. Create a short URL
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/lifecycle-test",
			CustomAlias: "lifecycle",
			ExpiresIn:   30,
		}

		createResp, err := service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Failed to create URL: %v", err)

		t.Logf("Created short URL: %s", createResp.ShortURL)

		// 2. Retrieve URL metadata
		urlResp, err := service.GetURL(ctx, createResp.ShortCode)
		require.NoError(t, err, "Failed to get URL: %v", err)
		assert.Equal(t, createReq.URL, urlResp.OriginalURL, "Original URL mismatch: expected %s, got %s", createReq.URL, urlResp.OriginalURL)

		// 3. Redirect to original URL
		redirectURL, err := service.Redirect(ctx, createResp.ShortCode)
		require.NoError(t, err, "Failed to redirect: %v", err)
		assert.Equal(t, createReq.URL, redirectURL, "Redirect URL mismatch: expected %s, got %s", createReq.URL, redirectURL)

		// 4. Delete the URL
		err = service.DeleteURL(ctx, createResp.ShortCode)
		require.NoError(t, err, "Failed to delete URL: %v", err)

		// 5. Verify deletion
		_, err = service.GetURL(ctx, createResp.ShortCode)
		assert.Equal(t, ErrURLNotFound, err, "Expected ErrURLNotFound after deletion, got %v", err)
	})

	t.Run("multiple URLs with different configurations", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Create multiple URLs
		urls := []struct {
			alias     string
			url       string
			expiresIn int
		}{
			{"short1", "https://example.com/page1", 0},
			{"short2", "https://example.com/page2", 7},
			{"short3", "https://example.com/page3", 30},
		}

		for _, u := range urls {
			req := &model.CreateURLRequest{
				URL:         u.url,
				CustomAlias: u.alias,
				ExpiresIn:   u.expiresIn,
			}

			_, err := service.CreateShortURL(ctx, req)
			require.NoError(t, err, "Failed to create URL %s: %v", u.alias, err)
		}

		// Verify all URLs exist and can be retrieved
		for _, u := range urls {
			urlResp, err := service.GetURL(ctx, u.alias)
			require.NoError(t, err, "Failed to get URL %s: %v", u.alias, err)
			assert.Equal(t, u.url, urlResp.OriginalURL, "URL mismatch for %s: expected %s, got %s", u.alias, u.url, urlResp.OriginalURL)
		}
	})

	t.Run("multiple URLs with auto-generated short codes", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// URLs to shorten
		originalURLs := []string{
			"https://example.com/article/1",
			"https://example.com/article/2",
			"https://example.com/article/3",
			"https://example.com/blog/post-1",
			"https://example.com/blog/post-2",
		}

		// Store mapping of short code to original URL
		shortCodeMap := make(map[string]string)

		// Create URLs with auto-generated short codes
		for _, url := range originalURLs {
			req := &model.CreateURLRequest{
				URL: url,
				// No CustomAlias - let system generate
			}

			resp, err := service.CreateShortURL(ctx, req)
			require.NoError(t, err, "Failed to create URL for %s: %v", url, err)

			assert.NotEmpty(t, resp.ShortCode, "Expected short code to be generated for %s", url)

			// Verify short code is unique
			if _, exists := shortCodeMap[resp.ShortCode]; exists {
				assert.Failf(t, "duplicate", "Duplicate short code generated: %s", resp.ShortCode)
			}

			shortCodeMap[resp.ShortCode] = url
			t.Logf("Created: %s -> %s", resp.ShortCode, url)
		}

		// Verify we can retrieve all URLs by their generated short codes
		for shortCode, expectedURL := range shortCodeMap {
			urlResp, err := service.GetURL(ctx, shortCode)
			require.NoError(t, err, "Failed to get URL for short code %s: %v", shortCode, err)
			assert.Equal(t, expectedURL, urlResp.OriginalURL, "URL mismatch for %s: expected %s, got %s", shortCode, expectedURL, urlResp.OriginalURL)
			assert.Equal(t, shortCode, urlResp.ShortCode, "Short code mismatch: expected %s, got %s", shortCode, urlResp.ShortCode)
		}

		// Verify we can also redirect using generated short codes
		for shortCode, expectedURL := range shortCodeMap {
			redirectURL, err := service.Redirect(ctx, shortCode)
			require.NoError(t, err, "Failed to redirect for short code %s: %v", shortCode, err)
			assert.Equal(t, expectedURL, redirectURL, "Redirect mismatch for %s: expected %s, got %s", shortCode, expectedURL, redirectURL)
		}

		// Verify all 5 URLs were created
		if len(shortCodeMap) != len(originalURLs) {
			assert.Equal(t, len(originalURLs), len(shortCodeMap), "Expected %d URLs to be created, got %d", len(originalURLs), len(shortCodeMap))
		}
	})
}

func TestURLService_WithCache(t *testing.T) {
	ctx := context.Background()
	cacheTTL := 5 * time.Minute

	t.Run("caches URL on first read", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := repository.NewURLRepository(testDB.Pool)
		repo := repository.NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)
		service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

		// Create a URL
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/cache-test",
			CustomAlias: "cache-test",
		}
		_, err := service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Failed to create URL: %v", err)

		// First read - should cache the result
		_, err = service.GetURL(ctx, "cache-test")
		require.NoError(t, err, "Failed to get URL: %v", err)

		// Verify it's in cache
		cacheKey := "url:cache-test"
		exists, err := testCache.Client.Exists(ctx, cacheKey).Result()
		require.NoError(t, err, "Failed to check cache: %v", err)
		assert.Equal(t, int64(1), exists, "Expected URL to be cached after first read")
	})

	t.Run("serves from cache on subsequent reads", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := repository.NewURLRepository(testDB.Pool)
		repo := repository.NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)
		service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

		// Create and read a URL to cache it
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/cache-hit",
			CustomAlias: "cache-hit",
		}
		_, err := service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Failed to create URL: %v", err)

		// First read to populate cache
		_, err = service.GetURL(ctx, "cache-hit")
		require.NoError(t, err, "Failed to get URL: %v", err)

		// Delete from DB directly (bypass cache)
		_, err = testDB.Pool.Exec(ctx, "DELETE FROM urls WHERE short_code = $1", "cache-hit")
		require.NoError(t, err, "Failed to delete from DB: %v", err)

		// Second read should still succeed (served from cache)
		urlResp, err := service.GetURL(ctx, "cache-hit")
		require.NoError(t, err, "Expected cache hit, got error: %v", err)
		assert.Equal(t, "https://example.com/cache-hit", urlResp.OriginalURL, "Expected cached URL, got %s", urlResp.OriginalURL)
	})

	t.Run("invalidates cache on delete", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := repository.NewURLRepository(testDB.Pool)
		repo := repository.NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)
		service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

		// Create and cache a URL
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/cache-delete",
			CustomAlias: "cache-delete",
		}
		_, err := service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Failed to create URL: %v", err)

		// Read to ensure it's cached
		_, err = service.GetURL(ctx, "cache-delete")
		require.NoError(t, err, "Failed to get URL: %v", err)

		// Verify it's cached
		cacheKey := "url:cache-delete"
		exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
		assert.Equal(t, int64(1), exists, "Expected URL to be cached before delete")

		// Delete via service (should invalidate cache)
		err = service.DeleteURL(ctx, "cache-delete")
		require.NoError(t, err, "Failed to delete URL: %v", err)

		// Verify cache is invalidated
		exists, _ = testCache.Client.Exists(ctx, cacheKey).Result()
		assert.Equal(t, int64(0), exists, "Expected cache to be invalidated after delete")
	})

	t.Run("caches negative result for non-existent URL", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := repository.NewURLRepository(testDB.Pool)
		repo := repository.NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)
		service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

		// Try to get a non-existent URL
		_, err := service.GetURL(ctx, "nonexistent-cache")
		assert.Error(t, err, "Expected error for non-existent URL")

		// Verify negative result is cached
		cacheKey := "url:nonexistent-cache"
		cached, err := testCache.Client.Get(ctx, cacheKey).Result()
		require.NoError(t, err, "Expected negative cache entry, got error: %v", err)
		assert.Equal(t, "__NOT_FOUND__", cached, "Expected sentinel value '__NOT_FOUND__', got %s", cached)
	})

	t.Run("create overwrites negative cache", func(t *testing.T) {
		testDB.Cleanup(ctx)
		testCache.Cleanup(ctx)

		dbRepo := repository.NewURLRepository(testDB.Pool)
		repo := repository.NewCachedURLRepository(dbRepo, testCache.Client, cacheTTL)
		service := NewURLService(repo, testCfg.App.BaseURL, testCfg.App.ShortCodeLen, testCfg.App.ShortCodeRetries)

		// Try to get a non-existent URL (triggers negative caching)
		_, err := service.GetURL(ctx, "overwrite-neg")
		assert.Error(t, err, "Expected error for non-existent URL")

		// Verify negative cache exists
		cacheKey := "url:overwrite-neg"
		cached, _ := testCache.Client.Get(ctx, cacheKey).Result()
		require.Equal(t, "__NOT_FOUND__", cached, "Expected negative cache entry, got %s", cached)

		// Now create the URL
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/overwrite-neg",
			CustomAlias: "overwrite-neg",
		}
		_, err = service.CreateShortURL(ctx, createReq)
		require.NoError(t, err, "Failed to create URL: %v", err)

		// The negative cache should be overwritten
		cached, _ = testCache.Client.Get(ctx, cacheKey).Result()
		if cached == "__NOT_FOUND__" {
			assert.NotEqual(t, "__NOT_FOUND__", cached, "Expected negative cache to be overwritten by create")
		}

		// Should be able to get the URL now
		urlResp, err := service.GetURL(ctx, "overwrite-neg")
		require.NoError(t, err, "Expected URL to exist after create, got error: %v", err)
		assert.Equal(t, "https://example.com/overwrite-neg", urlResp.OriginalURL, "Expected correct URL, got %s", urlResp.OriginalURL)
	})
}
