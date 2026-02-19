package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/sony/gobreaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn    *grpc.ClientConn
	stub    RateLimitServiceClient
	cb      *gobreaker.CircuitBreaker
	timeout time.Duration
	logger  *slog.Logger
}

func NewClient(addr string, timeout time.Duration, logger *slog.Logger) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))

	if err != nil {
		return nil, err
	}

	c := &Client{
		conn:    conn,
		stub:    NewRateLimitServiceClient(conn),
		timeout: timeout,
		logger:  logger,
	}

	c.cb = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "rate-limiter",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
		OnStateChange: func(_ string, from, to gobreaker.State) {
			logger.Warn("rate-limiter circuit breaker state change",
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
		},
	})
	return c, nil
}

func (c *Client) Check(ctx context.Context, ip string) (allowed bool, remaining int32, retryAfterMs int64, err error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	res, cbErr := c.cb.Execute(func() (interface{}, error) {
		return c.stub.CheckRateLimit(callCtx, &RateLimitRequest{Ip: ip})
	})

	if cbErr != nil {
		if errors.Is(cbErr, gobreaker.ErrOpenState) {
			c.logger.WarnContext(ctx, "rate limiter circuit breaker open, failing open",
				slog.String("ip", ip),
			)
		} else {
			c.logger.WarnContext(ctx, "rate limiter call failed, failing open",
				slog.String("ip", ip),
				slog.Any("error", cbErr),
			)
		}
		return true, 0, 0, cbErr
	}

	resp := res.(*RateLimitResponse)
	return resp.Allowed, resp.Remaining, resp.RetryAfterMs, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}
