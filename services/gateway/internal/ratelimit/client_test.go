package ratelimit

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/sony/gobreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockStub implements RateLimiterClient with a fixed response.
type mockStub struct {
	resp *RateLimitResponse
	err  error
}

func (m *mockStub) CheckRateLimit(_ context.Context, _ *RateLimitRequest, _ ...grpc.CallOption) (*RateLimitResponse, error) {
	return m.resp, m.err
}

// newTestClient builds a Client with an injected stub, bypassing gRPC dial.
// The circuit breaker is configured to trip after 5 consecutive failures,
// matching the production setting.
func newTestClient(stub RateLimiterClient) *Client {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := &Client{
		stub:    stub,
		timeout: time.Second,
		logger:  logger,
	}
	c.cb = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name: "test",
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
	})
	return c
}

func TestCheck_Allowed(t *testing.T) {
	stub := &mockStub{
		resp: &RateLimitResponse{Allowed: true, Remaining: 9, RetryAfterMs: 0},
	}
	c := newTestClient(stub)

	allowed, remaining, retryAfterMs, err := c.Check(context.Background(), "1.2.3.4")

	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, int32(9), remaining)
	assert.Equal(t, int64(0), retryAfterMs)
}

func TestCheck_Denied(t *testing.T) {
	stub := &mockStub{
		resp: &RateLimitResponse{Allowed: false, Remaining: 0, RetryAfterMs: 3000},
	}
	c := newTestClient(stub)

	allowed, remaining, retryAfterMs, err := c.Check(context.Background(), "1.2.3.4")

	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, int32(0), remaining)
	assert.Equal(t, int64(3000), retryAfterMs)
}

func TestCheck_GRPCError_FailsOpen(t *testing.T) {
	stub := &mockStub{
		err: status.Error(codes.Unavailable, "service unavailable"),
	}
	c := newTestClient(stub)

	allowed, _, _, err := c.Check(context.Background(), "1.2.3.4")

	assert.Error(t, err)
	assert.True(t, allowed, "should fail open on gRPC error")
}

func TestCheck_CircuitBreaker_TripsAndFailsOpen(t *testing.T) {
	stub := &mockStub{
		err: status.Error(codes.Unavailable, "service unavailable"),
	}
	c := newTestClient(stub)
	ctx := context.Background()

	// Exhaust the 5 consecutive failures required to trip the breaker.
	for i := 0; i < 5; i++ {
		c.Check(ctx, "1.2.3.4")
	}

	// Circuit is now open: the CB short-circuits immediately without calling the stub.
	allowed, _, _, err := c.Check(ctx, "1.2.3.4")

	assert.ErrorIs(t, err, gobreaker.ErrOpenState)
	assert.True(t, allowed, "should fail open when circuit breaker is open")
}
