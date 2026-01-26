package testutil

import (
	"context"
	"path/filepath"
	"runtime"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestDB holds test database resources
type TestDB struct {
	Pool      *pgxpool.Pool
	container *postgres.PostgresContainer
}

// SetupTestDB creates a new test database with migrations applied
func SetupTestDB(ctx context.Context) (*TestDB, error) {
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
		return nil, err
	}

	connString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		if terr := container.Terminate(ctx); terr != nil {
			err = terr
		}
		return nil, err
	}

	if err := runMigrations(connString); err != nil {
		if terr := container.Terminate(ctx); terr != nil {
			err = terr
		}
		return nil, err
	}

	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		if terr := container.Terminate(ctx); terr != nil {
			err = terr
		}
		return nil, err
	}

	return &TestDB{Pool: pool, container: container}, nil
}

// Cleanup truncates all tables
func (t *TestDB) Cleanup(ctx context.Context) {
	if t == nil || t.Pool == nil {
		return
	}
	if _, err := t.Pool.Exec(ctx, "TRUNCATE TABLE urls RESTART IDENTITY"); err != nil {
		return
	}
}

// Teardown closes connections and terminates container
func (t *TestDB) Teardown(ctx context.Context) {
	if t.Pool != nil {
		t.Pool.Close()
	}
	if t.container != nil {
		if err := t.container.Terminate(ctx); err != nil {
			return
		}
	}
}

func runMigrations(connString string) error {
	_, filename, _, _ := runtime.Caller(0)
	migrationsPath := filepath.Join(filepath.Dir(filename), "../../../../migrations/schema")

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
