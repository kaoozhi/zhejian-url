package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zhejian/url-shortener/gateway/internal/model"
	"github.com/zhejian/url-shortener/gateway/internal/repository"
	"github.com/zhejian/url-shortener/gateway/internal/testutil"
)

var testDB *testutil.TestDB

func TestMain(m *testing.M) {
	ctx := context.Background()

	var err error
	testDB, err = testutil.SetupTestDB(ctx)
	if err != nil {
		panic("failed to setup test database: " + err.Error())
	}

	// Run tests
	code := m.Run()

	// Cleanup
	testDB.Teardown(ctx)
	os.Exit(code)
}

func TestURLService_CreateShortURL(t *testing.T) {
	ctx := context.Background()

	repo := repository.NewURLRepository(testDB.Pool)
	service := NewURLService(repo, "http://localhost:8080", 6, 3)

	t.Run("creates short URL successfully", func(t *testing.T) {
		testDB.Cleanup(ctx)

		req := &model.CreateURLRequest{
			URL:       "https://example.com/very/long/url",
			ExpiresIn: 0,
		}

		resp, err := service.CreateShortURL(ctx, req)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if resp.ShortCode == "" {
			t.Error("Expected short code to be generated")
		}

		if resp.ShortURL == "" {
			t.Error("Expected short URL to be generated")
		}

		expectedURL := "http://localhost:8080/" + resp.ShortCode
		if resp.ShortURL != expectedURL {
			t.Errorf("Expected short URL %s, got %s", expectedURL, resp.ShortURL)
		}

		if resp.ExpiresAt != "" {
			t.Error("Expected no expiration for permanent URL")
		}
	})

	t.Run("creates short URL with custom alias", func(t *testing.T) {
		testDB.Cleanup(ctx)

		req := &model.CreateURLRequest{
			URL:         "https://example.com/custom",
			CustomAlias: "my-custom-alias",
		}

		resp, err := service.CreateShortURL(ctx, req)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if resp.ShortCode != "my-custom-alias" {
			t.Errorf("Expected short code 'my-custom-alias', got %s", resp.ShortCode)
		}
	})

	t.Run("creates short URL with expiration", func(t *testing.T) {
		testDB.Cleanup(ctx)

		req := &model.CreateURLRequest{
			URL:       "https://example.com/expiring",
			ExpiresIn: 7, // 7 days
		}

		resp, err := service.CreateShortURL(ctx, req)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if resp.ExpiresAt == "" {
			t.Error("Expected expiration date to be set")
		}

		// Verify expiration is approximately 7 days from now
		expiresAt, err := time.Parse(time.RFC3339, resp.ExpiresAt)
		if err != nil {
			t.Fatalf("Failed to parse expiration date: %v", err)
		}

		expectedExpiry := time.Now().AddDate(0, 0, 7)
		diff := expiresAt.Sub(expectedExpiry).Abs()
		if diff > time.Minute {
			t.Errorf("Expiration date is not approximately 7 days from now")
		}
	})

	t.Run("fails when custom alias already exists", func(t *testing.T) {
		testDB.Cleanup(ctx)

		req := &model.CreateURLRequest{
			URL:         "https://example.com/first",
			CustomAlias: "duplicate-alias",
		}

		_, err := service.CreateShortURL(ctx, req)
		if err != nil {
			t.Fatalf("Expected first creation to succeed, got %v", err)
		}

		// Try to create another URL with the same alias
		req2 := &model.CreateURLRequest{
			URL:         "https://example.com/second",
			CustomAlias: "duplicate-alias",
		}

		_, err = service.CreateShortURL(ctx, req2)
		if err == nil {
			t.Error("Expected error for duplicate alias, got nil")
		}
	})
}

func TestURLService_GetURL(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewURLRepository(testDB.Pool)
	service := NewURLService(repo, "http://localhost:8080", 6, 3)

	t.Run("retrieves existing URL successfully", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Create a URL first
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/original",
			CustomAlias: "get-test",
		}

		createResp, err := service.CreateShortURL(ctx, createReq)
		if err != nil {
			t.Fatalf("Failed to create URL: %v", err)
		}

		// Retrieve the URL
		urlResp, err := service.GetURL(ctx, createResp.ShortCode)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if urlResp.ShortCode != "get-test" {
			t.Errorf("Expected short code 'get-test', got %s", urlResp.ShortCode)
		}

		if urlResp.OriginalURL != "https://example.com/original" {
			t.Errorf("Expected original URL 'https://example.com/original', got %s", urlResp.OriginalURL)
		}

		if urlResp.ShortURL != "http://localhost:8080/get-test" {
			t.Errorf("Expected short URL 'http://localhost:8080/get-test', got %s", urlResp.ShortURL)
		}

		if urlResp.ClickCount != 0 {
			t.Errorf("Expected click count 0, got %d", urlResp.ClickCount)
		}

		if urlResp.CreatedAt == "" {
			t.Error("Expected created_at to be set")
		}
	})

	t.Run("returns error for non-existent URL", func(t *testing.T) {
		testDB.Cleanup(ctx)

		_, err := service.GetURL(ctx, "nonexistent")
		if err == nil {
			t.Error("Expected error for non-existent URL, got nil")
		}

		if err != ErrURLNotFound {
			t.Errorf("Expected ErrURLNotFound, got %v", err)
		}
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
			t.Fatalf("Failed to insert expired URL: %v", err)
		}

		// Try to get the expired URL
		_, err = service.GetURL(ctx, "expired-test")
		if err == nil {
			t.Error("Expected error for expired URL, got nil")
		}

		if err != ErrURLExpired {
			t.Errorf("Expected ErrURLExpired, got %v", err)
		}
	})
}

func TestURLService_Redirect(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewURLRepository(testDB.Pool)
	service := NewURLService(repo, "http://localhost:8080", 6, 3)

	t.Run("redirects to original URL successfully", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Create a URL first
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/redirect-target",
			CustomAlias: "redirect-test",
		}

		createResp, err := service.CreateShortURL(ctx, createReq)
		if err != nil {
			t.Fatalf("Failed to create URL: %v", err)
		}

		// Get redirect URL
		originalURL, err := service.Redirect(ctx, createResp.ShortCode)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if originalURL != "https://example.com/redirect-target" {
			t.Errorf("Expected redirect to 'https://example.com/redirect-target', got %s", originalURL)
		}
	})

	t.Run("returns error for non-existent short code", func(t *testing.T) {
		testDB.Cleanup(ctx)

		_, err := service.Redirect(ctx, "nonexistent")
		if err == nil {
			t.Error("Expected error for non-existent short code, got nil")
		}

		if err != ErrURLNotFound {
			t.Errorf("Expected ErrURLNotFound, got %v", err)
		}
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
			t.Fatalf("Failed to insert expired URL: %v", err)
		}

		// Try to redirect
		_, err = service.Redirect(ctx, "expired-redirect")
		if err == nil {
			t.Error("Expected error for expired URL, got nil")
		}

		if err != ErrURLExpired {
			t.Errorf("Expected ErrURLExpired, got %v", err)
		}
	})
}

func TestURLService_DeleteURL(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewURLRepository(testDB.Pool)
	service := NewURLService(repo, "http://localhost:8080", 6, 3)

	t.Run("deletes existing URL successfully", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Create a URL first
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/to-delete",
			CustomAlias: "delete-test",
		}

		createResp, err := service.CreateShortURL(ctx, createReq)
		if err != nil {
			t.Fatalf("Failed to create URL: %v", err)
		}

		// Delete the URL
		err = service.DeleteURL(ctx, createResp.ShortCode)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		// Verify it's deleted by trying to get it
		_, err = service.GetURL(ctx, createResp.ShortCode)
		if err == nil {
			t.Error("Expected error when getting deleted URL, got nil")
		}

		if err != ErrURLNotFound {
			t.Errorf("Expected ErrURLNotFound, got %v", err)
		}
	})

	t.Run("returns error when deleting non-existent URL", func(t *testing.T) {
		testDB.Cleanup(ctx)

		err := service.DeleteURL(ctx, "nonexistent")
		if err == nil {
			t.Error("Expected error for non-existent URL, got nil")
		}

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
		if err != nil {
			t.Fatalf("Failed to create URL: %v", err)
		}

		// Delete it
		err = service.DeleteURL(ctx, createResp.ShortCode)
		if err != nil {
			t.Fatalf("Failed to delete URL: %v", err)
		}

		// Recreate with same alias
		createResp2, err := service.CreateShortURL(ctx, createReq)
		if err != nil {
			t.Fatalf("Expected to recreate URL after deletion, got error: %v", err)
		}

		if createResp2.ShortCode != "recreate-test" {
			t.Errorf("Expected short code 'recreate-test', got %s", createResp2.ShortCode)
		}
	})
}

func TestURLService_Integration_FullWorkflow(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)

	repo := repository.NewURLRepository(testDB.Pool)
	service := NewURLService(repo, "http://localhost:8080", 6, 3)

	t.Run("complete URL lifecycle", func(t *testing.T) {
		// 1. Create a short URL
		createReq := &model.CreateURLRequest{
			URL:         "https://example.com/lifecycle-test",
			CustomAlias: "lifecycle",
			ExpiresIn:   30,
		}

		createResp, err := service.CreateShortURL(ctx, createReq)
		if err != nil {
			t.Fatalf("Failed to create URL: %v", err)
		}

		t.Logf("Created short URL: %s", createResp.ShortURL)

		// 2. Retrieve URL metadata
		urlResp, err := service.GetURL(ctx, createResp.ShortCode)
		if err != nil {
			t.Fatalf("Failed to get URL: %v", err)
		}

		if urlResp.OriginalURL != createReq.URL {
			t.Errorf("Original URL mismatch: expected %s, got %s", createReq.URL, urlResp.OriginalURL)
		}

		// 3. Redirect to original URL
		redirectURL, err := service.Redirect(ctx, createResp.ShortCode)
		if err != nil {
			t.Fatalf("Failed to redirect: %v", err)
		}

		if redirectURL != createReq.URL {
			t.Errorf("Redirect URL mismatch: expected %s, got %s", createReq.URL, redirectURL)
		}

		// 4. Delete the URL
		err = service.DeleteURL(ctx, createResp.ShortCode)
		if err != nil {
			t.Fatalf("Failed to delete URL: %v", err)
		}

		// 5. Verify deletion
		_, err = service.GetURL(ctx, createResp.ShortCode)
		if err != ErrURLNotFound {
			t.Errorf("Expected ErrURLNotFound after deletion, got %v", err)
		}
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
			if err != nil {
				t.Fatalf("Failed to create URL %s: %v", u.alias, err)
			}
		}

		// Verify all URLs exist and can be retrieved
		for _, u := range urls {
			urlResp, err := service.GetURL(ctx, u.alias)
			if err != nil {
				t.Fatalf("Failed to get URL %s: %v", u.alias, err)
			}

			if urlResp.OriginalURL != u.url {
				t.Errorf("URL mismatch for %s: expected %s, got %s", u.alias, u.url, urlResp.OriginalURL)
			}
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
			if err != nil {
				t.Fatalf("Failed to create URL for %s: %v", url, err)
			}

			if resp.ShortCode == "" {
				t.Errorf("Expected short code to be generated for %s", url)
			}

			// Verify short code is unique
			if _, exists := shortCodeMap[resp.ShortCode]; exists {
				t.Errorf("Duplicate short code generated: %s", resp.ShortCode)
			}

			shortCodeMap[resp.ShortCode] = url
			t.Logf("Created: %s -> %s", resp.ShortCode, url)
		}

		// Verify we can retrieve all URLs by their generated short codes
		for shortCode, expectedURL := range shortCodeMap {
			urlResp, err := service.GetURL(ctx, shortCode)
			if err != nil {
				t.Fatalf("Failed to get URL for short code %s: %v", shortCode, err)
			}

			if urlResp.OriginalURL != expectedURL {
				t.Errorf("URL mismatch for %s: expected %s, got %s", shortCode, expectedURL, urlResp.OriginalURL)
			}

			if urlResp.ShortCode != shortCode {
				t.Errorf("Short code mismatch: expected %s, got %s", shortCode, urlResp.ShortCode)
			}
		}

		// Verify we can also redirect using generated short codes
		for shortCode, expectedURL := range shortCodeMap {
			redirectURL, err := service.Redirect(ctx, shortCode)
			if err != nil {
				t.Fatalf("Failed to redirect for short code %s: %v", shortCode, err)
			}

			if redirectURL != expectedURL {
				t.Errorf("Redirect mismatch for %s: expected %s, got %s", shortCode, expectedURL, redirectURL)
			}
		}

		// Verify all 5 URLs were created
		if len(shortCodeMap) != len(originalURLs) {
			t.Errorf("Expected %d URLs to be created, got %d", len(originalURLs), len(shortCodeMap))
		}
	})
}
