package infra_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, err, "failed to get connection string")

		pool, err := infra.NewPostgresPool(ctx, connString)
		require.NoError(t, err)
		defer pool.Close()

		assert.NoError(t, pool.Ping(ctx), "pool ping failed")
	})

	t.Run("error - invalid connection string", func(t *testing.T) {
		_, err := infra.NewPostgresPool(ctx, "invalid://connection")
		require.Error(t, err, "expected error for invalid connection string")
	})

	t.Run("error - unreachable host", func(t *testing.T) {
		_, err := infra.NewPostgresPool(ctx, "postgres://user:pass@localhost:59999/db?sslmode=disable")
		require.Error(t, err, "expected error for unreachable host")
	})
}

func TestNewCacheClient(t *testing.T) {
	ctx := context.Background()

	testCache, err := testutil.SetupTestCache(ctx)
	require.NoError(t, err, "failed to setup test cache")
	defer testCache.Teardown(ctx)

	t.Run("success - valid connection string", func(t *testing.T) {
		connString, err := testCache.Container().ConnectionString(ctx)
		require.NoError(t, err, "failed to get connection string")

		client, err := infra.NewCacheClient(ctx, connString)
		require.NoError(t, err)
		defer client.Close()

		assert.NoError(t, client.Ping(ctx).Err(), "client ping failed")
	})

	t.Run("error - invalid connection string", func(t *testing.T) {
		_, err := infra.NewCacheClient(ctx, "invalid://connection")
		require.Error(t, err, "expected error for invalid connection string")
	})

	t.Run("error - unreachable host", func(t *testing.T) {
		_, err := infra.NewCacheClient(ctx, "redis://localhost:59999")
		require.Error(t, err, "expected error for unreachable host")
	})
}
