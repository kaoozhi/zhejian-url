package repository

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/zhejian/url-shortener/gateway/internal/model"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Start postgres container automatically
	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_db"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		panic("failed to start postgres container: " + err.Error())
	}

	// Get connection string (random port assigned)
	connString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("failed to get connection string: " + err.Error())
	}

	// Run migrations
	if err := runMigrations(connString); err != nil {
		panic("failed to run migrations: " + err.Error())
	}

	// Create connection pool
	testPool, err = pgxpool.New(ctx, connString)
	if err != nil {
		panic("failed to create connection pool: " + err.Error())
	}

	// Run tests
	code := m.Run()

	// Cleanup
	testPool.Close()
	container.Terminate(ctx)
	os.Exit(code)
}

func runMigrations(connString string) error {
	// Get path to migrations folder relative to this file
	_, filename, _, _ := runtime.Caller(0)
	migrationsPath := filepath.Join(filepath.Dir(filename), "../../../../migrations")

	m, err := migrate.New(
		"file://"+migrationsPath,
		connString,
	)
	if err != nil {
		return err
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

func TestURLRepository_Create(t *testing.T) {
	repo := NewURLRepository(testPool)
	ctx := context.Background()

	// Cleanup before tests
	cleanup := func() {
		testPool.Exec(ctx, "TRUNCATE TABLE urls RESTART IDENTITY")
	}

	t.Run("success - create URL without expiry", func(t *testing.T) {
		cleanup()

		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   "abc123",
			OriginalURL: "https://example.com",
			CreatedAt:   time.Now(),
			ExpiresAt:   nil,
			ClickCount:  0,
		}

		err := repo.Create(ctx, url)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify in database
		var count int
		testPool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "abc123").Scan(&count)
		if count != 1 {
			t.Errorf("expected 1 row, got %d", count)
		}
	})

	t.Run("success - create URL with expiry", func(t *testing.T) {
		cleanup()

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
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify expiry saved correctly
		var savedExpiry time.Time
		testPool.QueryRow(ctx, "SELECT expires_at FROM urls WHERE short_code = $1", "def456").Scan(&savedExpiry)
		if savedExpiry.IsZero() {
			t.Error("expected expires_at to be set")
		}
	})

	t.Run("error - duplicate short code", func(t *testing.T) {
		cleanup()

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
		if err != nil {
			t.Fatalf("first create failed: %v", err)
		}

		// Second insert should fail with ErrCodeConflict
		err = repo.Create(ctx, url2)
		if err == nil {
			t.Fatal("expected error for duplicate short code")
		}
		if err != ErrCodeConflict {
			t.Errorf("expected ErrCodeConflict, got %v", err)
		}
	})
}

func TestURLRepository_GetByCode(t *testing.T) {
	repo := NewURLRepository(testPool)
	ctx := context.Background()

	cleanup := func() {
		testPool.Exec(ctx, "TRUNCATE TABLE urls RESTART IDENTITY")
	}

	t.Run("success - get existing URL", func(t *testing.T) {
		cleanup()

		// Insert test data
		id := uuid.New()
		expiresAt := time.Now().AddDate(0, 0, 7)
		testPool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at, expires_at)
            VALUES ($1, $2, $3, $4, $5)
        `, id, "abc123", "https://example.com", time.Now(), expiresAt)

		// Get by code
		url, err := repo.GetByCode(ctx, "abc123")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify fields
		if url.ShortCode != "abc123" {
			t.Errorf("expected short_code 'abc123', got '%s'", url.ShortCode)
		}
		if url.OriginalURL != "https://example.com" {
			t.Errorf("expected original_url 'https://example.com', got '%s'", url.OriginalURL)
		}
		if url.ClickCount != 0 {
			t.Errorf("expected click_count 0, got %d", url.ClickCount)
		}
		if url.ExpiresAt == nil {
			t.Error("expected expires_at to be set")
		}
	})

	t.Run("success - get URL without expiry", func(t *testing.T) {
		cleanup()

		// Insert test data without expiry
		id := uuid.New()
		testPool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at, click_count)
            VALUES ($1, $2, $3, $4, $5)
        `, id, "noexp1", "https://example.com/no-expiry", time.Now(), 0)

		url, err := repo.GetByCode(ctx, "noexp1")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if url.ExpiresAt != nil {
			t.Error("expected expires_at to be nil")
		}
	})

	t.Run("error - not found", func(t *testing.T) {
		cleanup()

		url, err := repo.GetByCode(ctx, "notexist")
		if err == nil {
			t.Fatal("expected error for non-existent code")
		}
		if err != ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
		if url != nil {
			t.Error("expected url to be nil")
		}
	})

	t.Run("error - empty code", func(t *testing.T) {
		cleanup()

		url, err := repo.GetByCode(ctx, "")
		if err == nil {
			t.Fatal("expected error for empty code")
		}
		if url != nil {
			t.Error("expected url to be nil")
		}
	})
}

func TestURLRepository_Delete(t *testing.T) {
	repo := NewURLRepository(testPool)
	ctx := context.Background()

	cleanup := func() {
		testPool.Exec(ctx, "TRUNCATE TABLE urls RESTART IDENTITY")
	}

	t.Run("success - delete existing URL", func(t *testing.T) {
		cleanup()

		// Insert test data
		id := uuid.New()
		testPool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at)
            VALUES ($1, $2, $3, $4)
        `, id, "del123", "https://example.com/delete", time.Now())

		// Verify exists before delete
		var countBefore int
		testPool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "del123").Scan(&countBefore)
		if countBefore != 1 {
			t.Fatal("test data not inserted")
		}

		// Delete
		err := repo.Delete(ctx, "del123")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify deleted
		var countAfter int
		testPool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "del123").Scan(&countAfter)
		if countAfter != 0 {
			t.Errorf("expected 0 rows after delete, got %d", countAfter)
		}
	})

	t.Run("error - delete non-existent URL", func(t *testing.T) {
		cleanup()

		err := repo.Delete(ctx, "notexist")
		if err == nil {
			t.Fatal("expected error for non-existent code")
		}
		if err != ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("error - delete empty code", func(t *testing.T) {
		cleanup()

		err := repo.Delete(ctx, "")
		if err == nil {
			t.Fatal("expected error for empty code")
		}
	})

	t.Run("success - delete does not affect other URLs", func(t *testing.T) {
		cleanup()

		// Insert two URLs
		testPool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at)
            VALUES ($1, $2, $3, $4)
        `, uuid.New(), "keep01", "https://example.com/keep", time.Now())

		testPool.Exec(ctx, `
            INSERT INTO urls (id, short_code, original_url, created_at)
            VALUES ($1, $2, $3, $4)
        `, uuid.New(), "del001", "https://example.com/delete", time.Now())

		// Delete only one
		err := repo.Delete(ctx, "del001")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify other URL still exists
		var count int
		testPool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", "keep01").Scan(&count)
		if count != 1 {
			t.Errorf("expected other URL to still exist, got count %d", count)
		}
	})
}
