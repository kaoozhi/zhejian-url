package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zhejian/url-shortener/gateway/internal/ratelimit"
	"github.com/redis/go-redis/v9"
	"github.com/zhejian/url-shortener/gateway/internal/cache"
	"github.com/zhejian/url-shortener/gateway/internal/server"
	"google.golang.org/grpc"
)

// mockRateLimiterServer is a controllable in-process gRPC rate limiter.
type mockRateLimiterServer struct {
	ratelimit.UnimplementedRateLimiterServer
	mu           sync.Mutex
	allowed      bool
	remaining    int32
	retryAfterMs int64
}

func (m *mockRateLimiterServer) CheckRateLimit(_ context.Context, _ *ratelimit.RateLimitRequest) (*ratelimit.RateLimitResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &ratelimit.RateLimitResponse{
		Allowed:      m.allowed,
		Remaining:    m.remaining,
		RetryAfterMs: m.retryAfterMs,
	}, nil
}

func (m *mockRateLimiterServer) setResponse(allowed bool, remaining int32, retryAfterMs int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allowed = allowed
	m.remaining = remaining
	m.retryAfterMs = retryAfterMs
}

// startMockRateLimiter starts an in-process gRPC server on a random port.
// The *grpc.Server is returned so individual tests can stop it early (e.g. fail-open test).
// t.Cleanup registers Stop as a safety net for all other tests.
func startMockRateLimiter(t *testing.T) (*mockRateLimiterServer, *grpc.Server, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	grpcSrv := grpc.NewServer()
	mock := &mockRateLimiterServer{allowed: true, remaining: 10}
	ratelimit.RegisterRateLimiterServer(grpcSrv, mock)

	go grpcSrv.Serve(lis) //nolint:errcheck
	t.Cleanup(grpcSrv.Stop)

	return mock, grpcSrv, lis.Addr().String()
}

// setupTestServerWithRL wires a real ratelimit.Client to the test HTTP server.
func setupTestServerWithRL(t *testing.T, rlAddr string) (*http.Server, string) {
	t.Helper()
	rlClient, err := ratelimit.NewClient(rlAddr, time.Second, testObs.Logger)
	require.NoError(t, err)
	t.Cleanup(func() { rlClient.Close() })

	gin.SetMode(gin.TestMode)
	srv := server.NewServer(testCfg, testDB.Pool, cache.NewHashRing(map[string]*redis.Client{"node": testCache.Client}, 1), rlClient, testObs, nil)

	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	baseURL := "http://" + lis.Addr().String()
	go func() {
		if err := srv.Serve(lis); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()
	waitForServer(t, baseURL+"/health", 3*time.Second)

	return srv, baseURL
}

// TestRateLimit_Allowed verifies that allowed requests pass through
// and X-RateLimit-Remaining reflects the mock's remaining value.
func TestRateLimit_Allowed(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	mock, _, addr := startMockRateLimiter(t)
	mock.setResponse(true, 9, 0)
	srv, baseURL := setupTestServerWithRL(t, addr)
	defer srv.Shutdown(ctx)

	body, _ := json.Marshal(map[string]string{"url": "https://example.com"})
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "9", resp.Header.Get("X-RateLimit-Remaining"))
}

// TestRateLimit_Denied_Returns429 verifies that denied requests receive a 429
// with the correct X-RateLimit-Remaining and Retry-After headers.
func TestRateLimit_Denied_Returns429(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	mock, _, addr := startMockRateLimiter(t)
	srv, baseURL := setupTestServerWithRL(t, addr)
	defer srv.Shutdown(ctx)
	mock.setResponse(false, 0, 3000)

	body, _ := json.Marshal(map[string]string{"url": "https://example.com"})
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, "0", resp.Header.Get("X-RateLimit-Remaining"))
	assert.Equal(t, "3", resp.Header.Get("Retry-After")) // retryAfterMs / 1000
}

// TestRateLimit_ServiceDown_FailsOpen verifies that when the rate limiter gRPC
// server is unreachable, the gateway fails open and serves the request normally.
func TestRateLimit_ServiceDown_FailsOpen(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	_, grpcSrv, addr := startMockRateLimiter(t)
	srv, baseURL := setupTestServerWithRL(t, addr)
	defer srv.Shutdown(ctx)

	// Bring down the rate limiter before the request.
	grpcSrv.Stop()

	body, _ := json.Marshal(map[string]string{"url": "https://example.com"})
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}
