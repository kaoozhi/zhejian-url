package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zhejian/url-shortener/gateway/internal/model"
	"github.com/zhejian/url-shortener/gateway/internal/testutil"
)

var (
	testDB    *testutil.TestDB
	testCache *testutil.TestCache
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	var err error
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

func TestURLRepository_Create(t *testing.T) {
	repo := NewURLRepository(testDB.Pool)
	ctx := context.Background()

	t.Run("success - create URL without expiry", func(t *testing.T) {
		testDB.Cleanup(ctx)

		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "abc123",
			OriginalURL: "https://example.com",
			CreatedAt:   time.Now(),
			ExpiresAt:   nil,
			ClickCount:  0,
		}

		err := repo.Create(ctx, url)
		require.NoError(t, err)

		// Verify in database
		var count int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "abc123").Scan(&count)
		assert.Equal(t, 1, count)
	})

	t.Run("success - create URL with expiry", func(t *testing.T) {
		testDB.Cleanup(ctx)

		expiresAt := time.Now().AddDate(0, 0, 7)
		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "def456",
			OriginalURL: "https://example.com/page",
			CreatedAt:   time.Now(),
			ExpiresAt:   &expiresAt,
			ClickCount:  0,
		}

		err := repo.Create(ctx, url)
		require.NoError(t, err)

		// Verify expiry saved correctly
		var savedExpiry time.Time
		testDB.Pool.QueryRow(ctx, "SELECT expires_at FROM urls WHERE short_code = $1", "def456").Scan(&savedExpiry)
		assert.False(t, savedExpiry.IsZero(), "expected expires_at to be set")
	})

	t.Run("error - duplicate short code", func(t *testing.T) {
		testDB.Cleanup(ctx)

		url1 := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "dup123",
			OriginalURL: "https://example.com/1",
			CreatedAt:   time.Now(),
		}
		url2 := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "dup123", // Same short code
			OriginalURL: "https://example.com/2",
			CreatedAt:   time.Now(),
		}

		// First insert should succeed
		err := repo.Create(ctx, url1)
		require.NoError(t, err, "first create failed")

		// Second insert should fail with ErrCodeConflict
		err = repo.Create(ctx, url2)
		require.Error(t, err, "expected error for duplicate short code")
		assert.ErrorIs(t, err, ErrCodeConflict)
	})
}

func TestURLRepository_GetByCode(t *testing.T) {
	repo := NewURLRepository(testDB.Pool)
	ctx := context.Background()

	t.Run("success - get existing URL", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Insert test data
		id := uuid.New()
		expiresAt := time.Now().AddDate(0, 0, 7)
		testDB.Pool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at, expires_at)
            VALUES ($1, $2, $3, $4, $5)
        `, id, "abc123", "https://example.com", time.Now(), expiresAt)

		// Get by code
		url, err := repo.GetByCode(ctx, "abc123")
		require.NoError(t, err)

		// Verify fields
		assert.Equal(t, "abc123", url.ShortCode)
		assert.Equal(t, "https://example.com", url.OriginalURL)
		assert.Equal(t, int64(0), url.ClickCount)
		assert.NotNil(t, url.ExpiresAt, "expected expires_at to be set")
	})

	t.Run("success - get URL without expiry", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Insert test data without expiry
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at, click_count)
            VALUES ($1, $2, $3, $4, $5)
        `, id, "noexp1", "https://example.com/no-expiry", time.Now(), 0)

		url, err := repo.GetByCode(ctx, "noexp1")
		require.NoError(t, err)
		assert.Nil(t, url.ExpiresAt, "expected expires_at to be nil")
	})

	t.Run("error - not found", func(t *testing.T) {
		testDB.Cleanup(ctx)

		url, err := repo.GetByCode(ctx, "notexist")
		require.Error(t, err, "expected error for non-existent code")
		assert.ErrorIs(t, err, ErrNotFound)
		assert.Nil(t, url)
	})

	t.Run("error - empty code", func(t *testing.T) {
		testDB.Cleanup(ctx)

		url, err := repo.GetByCode(ctx, "")
		require.Error(t, err, "expected error for empty code")
		assert.Nil(t, url)
	})
}

func TestURLRepository_Delete(t *testing.T) {
	repo := NewURLRepository(testDB.Pool)
	ctx := context.Background()

	t.Run("success - delete existing URL", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Insert test data
		id := uuid.New()
		testDB.Pool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at)
            VALUES ($1, $2, $3, $4)
        `, id, "del123", "https://example.com/delete", time.Now())

		// Verify exists before delete
		var countBefore int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "del123").Scan(&countBefore)
		require.Equal(t, 1, countBefore, "test data not inserted")

		// Delete
		err := repo.Delete(ctx, "del123")
		require.NoError(t, err)

		// Verify deleted
		var countAfter int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "del123").Scan(&countAfter)
		assert.Equal(t, 0, countAfter, "expected 0 rows after delete")
	})

	t.Run("error - delete non-existent URL", func(t *testing.T) {
		testDB.Cleanup(ctx)

		err := repo.Delete(ctx, "notexist")
		require.Error(t, err, "expected error for non-existent code")
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("error - delete empty code", func(t *testing.T) {
		testDB.Cleanup(ctx)

		err := repo.Delete(ctx, "")
		require.Error(t, err, "expected error for empty code")
	})

	t.Run("success - delete does not affect other URLs", func(t *testing.T) {
		testDB.Cleanup(ctx)

		// Insert two URLs
		testDB.Pool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at)
            VALUES ($1, $2, $3, $4)
        `, uuid.New(), "keep01", "https://example.com/keep", time.Now())

		testDB.Pool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at)
            VALUES ($1, $2, $3, $4)
        `, uuid.New(), "del001", "https://example.com/delete", time.Now())

		// Delete only one
		err := repo.Delete(ctx, "del001")
		require.NoError(t, err)

		// Verify other URL still exists
		var count int
		testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "keep01").Scan(&count)
		assert.Equal(t, 1, count, "expected other URL to still exist")
	})
}
