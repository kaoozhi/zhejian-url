package infra_test

import (
	"context"
	"os"
	"testing"

	"github.com/zhejian/url-shortener/gateway/internal/infra"
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

	code := m.Run()

	testDB.Teardown(ctx)
	os.Exit(code)
}

func TestNewPostgresPool(t *testing.T) {
	ctx := context.Background()

	t.Run("success - valid connection string", func(t *testing.T) {
		connString, err := testDB.Container().ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatalf("failed to get connection string: %v", err)
		}

		pool, err := infra.NewPostgresPool(ctx, connString)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		defer pool.Close()

		if err := pool.Ping(ctx); err != nil {
			t.Errorf("pool ping failed: %v", err)
		}
	})

	t.Run("error - invalid connection string", func(t *testing.T) {
		_, err := infra.NewPostgresPool(ctx, "invalid://connection")
		if err == nil {
			t.Fatal("expected error for invalid connection string")
		}
	})

	t.Run("error - unreachable host", func(t *testing.T) {
		_, err := infra.NewPostgresPool(ctx, "postgres://user:pass@localhost:59999/db?sslmode=disable")
		if err == nil {
			t.Fatal("expected error for unreachable host")
		}
	})
}

func TestNewCacheClient(t *testing.T) {
	ctx := context.Background()

	testCache, err := testutil.SetupTestCache(ctx)
	if err != nil {
		t.Fatalf("failed to setup test cache: %v", err)
	}
	defer testCache.Teardown(ctx)

	t.Run("success - valid connection string", func(t *testing.T) {
		connString, err := testCache.Container().ConnectionString(ctx)
		if err != nil {
			t.Fatalf("failed to get connection string: %v", err)
		}

		client, err := infra.NewCacheClient(ctx, connString)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		defer client.Close()

		if err := client.Ping(ctx).Err(); err != nil {
			t.Errorf("client ping failed: %v", err)
		}
	})

	t.Run("error - invalid connection string", func(t *testing.T) {
		_, err := infra.NewCacheClient(ctx, "invalid://connection")
		if err == nil {
			t.Fatal("expected error for invalid connection string")
		}
	})

	t.Run("error - unreachable host", func(t *testing.T) {
		_, err := infra.NewCacheClient(ctx, "redis://localhost:59999")
		if err == nil {
			t.Fatal("expected error for unreachable host")
		}
	})
}
