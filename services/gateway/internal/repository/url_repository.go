package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhejian/url-shortener/gateway/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	ctx, span := tracer.Start(ctx, "db.insert",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", "INSERT"),
			attribute.String("db.sql.table", "urls"),
			attribute.String("short_code", url.ShortCode),
		),
	)
	defer span.End()

	// Insert a new URL record. If the short code already exists the
	// database will return a unique-constraint error which we map to
	// ErrCodeConflict so callers can handle alias collisions.
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
		span.RecordError(err)
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
	ctx, span := tracer.Start(ctx, "db.select",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", "SELECT"),
			attribute.String("db.sql.table", "urls"),
			attribute.String("short_code", code),
		),
	)
	defer span.End()

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
		span.RecordError(err)
		return nil, err
	}
	return &url, nil
}

// Delete removes a URL by its short code
func (r *URLRepository) Delete(ctx context.Context, code string) error {
	ctx, span := tracer.Start(ctx, "db.delete",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", "DELETE"),
			attribute.String("db.sql.table", "urls"),
			attribute.String("short_code", code),
		),
	)
	defer span.End()

	// Delete a URL by short code and return ErrNotFound when no rows
	// are affected so callers can translate to a 404 response.
	query := `DELETE FROM urls WHERE short_code=$1`
	result, err := r.db.Exec(ctx, query, code)
	if err != nil {
		span.RecordError(err)
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// // IncrementClickCount increments the click counter for a URL
// func (r *URLRepository) IncrementClickCount(ctx context.Context, code string) error {
// 	// TODO: Implement click count increment
// 	// - UPDATE urls SET click_count = click_count + 1 WHERE short_code = $1
// 	return nil
// }
