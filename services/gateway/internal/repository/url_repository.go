package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	query := `
        INSERT INTO urls (id, short_code, original_url, expires_at)
        VALUES ($1, $2, $3, $4)
        RETURNING id, created_at
    `
	err := r.db.QueryRow(
		ctx,
		query,
		url.ID,
		url.ShortCode,
		url.OriginalURL,
		url.ExpiresAt,
	).Scan(&url.ID, &url.CreatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrCodeConflict
		}
		return err
	}

	return nil
}

// GetByCode retrieves a URL by its short code
func (r *URLRepository) GetByCode(ctx context.Context, code string) (*model.URL, error) {
	query :=
		`SELECT id, short_code, original_url, created_at, expires_at 
		FROM urls 
		WHERE short_code = $1`
	var url model.URL
	err := r.db.QueryRow(ctx, query, code).Scan(&url.ID,
		&url.ShortCode,
		&url.OriginalURL,
		&url.CreatedAt,
		&url.ExpiresAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &url, nil
}

// Delete removes a URL by its short code
func (r *URLRepository) Delete(ctx context.Context, code string) error {
	// TODO: Implement database delete
	// - Delete from urls table by short_code
	// - Return ErrNotFound if no rows affected
	query := `DELETE FROM urls WHERE short_code=$1`
	result, err := r.db.Exec(ctx, query, code)
	if err != nil {
		return err
	}
	// Check if any rows were deleted
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
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
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	// - Configure pool settings (max conns, timeouts, etc.)
	// Configure pool settings
	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute

	// Create the pool
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	// Test the connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	// - Return the pool
	return pool, nil
}
