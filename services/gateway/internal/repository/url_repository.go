package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhejian/url-shortener/gateway/internal/model"
)

var (
	ErrNotFound     = errors.New("url not found")
	ErrCodeConflict = errors.New("short code already exists")
)

// URLRepository handles database operations for URLs
type URLRepository struct {
	db *pgxpool.Pool
}

// NewURLRepository creates a new URL repository
func NewURLRepository(db *pgxpool.Pool) *URLRepository {
	return &URLRepository{db: db}
}

// Create inserts a new URL record into the database
func (r *URLRepository) Create(ctx context.Context, url *model.URL) error {
	// TODO: Implement database insert
	// - Insert into urls table (short_code, original_url, expires_at)
	// - Return ErrCodeConflict if short_code already exists
	// - Set url.ID and url.CreatedAt from returned values
	return nil
}

// GetByCode retrieves a URL by its short code
func (r *URLRepository) GetByCode(ctx context.Context, code string) (*model.URL, error) {
	// TODO: Implement database select
	// - Query urls table by short_code
	// - Return ErrNotFound if not exists
	// - Return the URL model
	return nil, nil
}

// Delete removes a URL by its short code
func (r *URLRepository) Delete(ctx context.Context, code string) error {
	// TODO: Implement database delete
	// - Delete from urls table by short_code
	// - Return ErrNotFound if no rows affected
	return nil
}

// IncrementClickCount increments the click counter for a URL
func (r *URLRepository) IncrementClickCount(ctx context.Context, code string) error {
	// TODO: Implement click count increment
	// - UPDATE urls SET click_count = click_count + 1 WHERE short_code = $1
	return nil
}

// CodeExists checks if a short code already exists
func (r *URLRepository) CodeExists(ctx context.Context, code string) (bool, error) {
	// TODO: Implement existence check
	// - SELECT EXISTS(SELECT 1 FROM urls WHERE short_code = $1)
	return false, nil
}

// NewPostgresPool creates a new PostgreSQL connection pool
func NewPostgresPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	// TODO: Implement connection pool creation
	// - Parse connection string
	// - Configure pool settings (max conns, timeouts, etc.)
	// - Return the pool
	return nil, nil
}
