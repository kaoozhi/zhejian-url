package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClickEvent is the analytics record written to the database.
type ClickEvent struct {
	ShortCode string
	ClickedAt time.Time
	IP        string
	Referer   string
}

// Repository wraps a pgxpool.Pool to write analytics events.
type Repository struct {
	db *pgxpool.Pool
}

// New returns a new Repository backed by the given connection pool.
func New(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// BulkInsert inserts all events in a single SQL statement — one round-trip
// regardless of batch size.
//
// For a batch of N events it builds:
//
//	INSERT INTO analytics (short_code, clicked_at, ip, referer)
//	VALUES ($1,$2,$3,$4), ($5,$6,$7,$8), ...
//
// pgx positional parameters ($1, $2, …) are numbered from 1 and each event
// occupies 4 consecutive slots: short_code, clicked_at, ip, referer.
// All values are passed as a flat []any slice and pgx maps each $N to
// args[N-1].
func (r *Repository) BulkInsert(ctx context.Context, events []ClickEvent) error {
	if len(events) == 0 {
		return nil
	}

	// Pre-allocate one placeholder tuple per event.
	placeholders := make([]string, len(events))
	// Pre-allocate the args slice: 4 values × N events.
	args := make([]any, 0, len(events)*4)

	for i, e := range events {
		base := i * 4
		// ($1,$2,$3,$4) for i=0, ($5,$6,$7,$8) for i=1, etc.
		placeholders[i] = fmt.Sprintf("($%d,$%d,$%d,$%d)", base+1, base+2, base+3, base+4)
		args = append(args, e.ShortCode, e.ClickedAt, e.IP, e.Referer)
	}

	query := "INSERT INTO analytics (short_code, clicked_at, ip, referer) VALUES " +
		strings.Join(placeholders, ", ")

	_, err := r.db.Exec(ctx, query, args...)
	return err
}
