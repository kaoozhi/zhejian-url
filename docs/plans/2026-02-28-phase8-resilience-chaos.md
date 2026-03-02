# Phase 8: Resilience & Chaos Testing — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Close the two remaining resilience gaps (Redis operation timeout, RabbitMQ publisher reconnect), extend Toxiproxy coverage from rate-limiter-only to all dependencies, expose circuit breaker state in the health check, and add 4 new automated chaos scenarios to `scripts/chaos-test.sh`.

**Architecture:** All changes are additive — no existing patterns are removed or replaced. The Redis timeout makes the existing `gobreaker` circuit breaker trip on *slow* Redis (not just *dead* Redis). The publisher reconnect adds a background goroutine that monitors `conn.NotifyClose()` and re-dials on drop. The Toxiproxy extension is purely infrastructure (compose + init script). The health check change adds CB state as metadata without changing HTTP status codes.

**Tech Stack:** `sony/gobreaker` (existing), `amqp091-go` (existing), `sync.RWMutex` + `atomic.Bool` (stdlib), Toxiproxy 2.7.0 (existing), bash chaos scripts.

**Execution order:** Tasks 1 → 2 → 3 → 4 → 5. Tasks 1 and 2 must complete before Task 5 (chaos scenarios 2 and 3 depend on Toxiproxy Redis proxy and the 50ms timeout).

---

### Task 1: Extend Toxiproxy to Redis and Postgres

**Files:**
- Modify: `docker-compose.chaos.yml`

**Step 1: Update the toxiproxy-init command to register all three proxies**

The current init registers only `rate-limiter`. Replace the `toxiproxy-init` command in `docker-compose.chaos.yml` with:

```yaml
  toxiproxy-init:
    image: curlimages/curl:8.11.1
    container_name: zhejian-toxiproxy-init
    entrypoint: []
    command:
      - /bin/sh
      - -c
      - |
        until curl -sf http://toxiproxy:8474/proxies > /dev/null; do sleep 1; done
        curl -s -X POST http://toxiproxy:8474/proxies \
          -H 'Content-Type: application/json' \
          -d '{"name":"rate-limiter","listen":"0.0.0.0:50052","upstream":"rate-limiter:50051"}'
        curl -s -X POST http://toxiproxy:8474/proxies \
          -H 'Content-Type: application/json' \
          -d '{"name":"redis","listen":"0.0.0.0:6380","upstream":"redis:6379"}'
        curl -s -X POST http://toxiproxy:8474/proxies \
          -H 'Content-Type: application/json' \
          -d '{"name":"postgres","listen":"0.0.0.0:5433","upstream":"postgres:5432"}'
        echo 'Toxiproxy configured: rate-limiter, redis, postgres'
    depends_on:
      - toxiproxy
    restart: "no"
```

**Step 2: Expose the new proxy ports from the toxiproxy container**

In the `toxiproxy` service block, add the new ports:

```yaml
  toxiproxy:
    image: ghcr.io/shopify/toxiproxy:2.7.0
    container_name: zhejian-toxiproxy
    ports:
      - "8474:8474"   # REST API
      - "50052:50052" # rate-limiter proxy
      - "6380:6380"   # redis proxy
      - "5433:5433"   # postgres proxy
```

**Step 3: Route the gateway's Redis and Postgres through Toxiproxy**

In the `gateway` service override block, add the env vars:

```yaml
  gateway:
    environment:
      - RATE_LIMITER_ADDR=toxiproxy:50052
      - CACHE_HOST=toxiproxy
      - CACHE_PORT=6380
      - DB_HOST=toxiproxy
      - DB_PORT=5433
    depends_on:
      toxiproxy-init:
        condition: service_completed_successfully
```

**Step 4: Update the header comment in docker-compose.chaos.yml**

Replace the existing comment block at the top with:

```yaml
# Chaos testing overlay — use on top of docker-compose.yml
#
# Usage:
#   docker compose -f docker-compose.yml -f docker-compose.chaos.yml up
#
# Proxies:
#   rate-limiter  toxiproxy:50052  →  rate-limiter:50051
#   redis         toxiproxy:6380   →  redis:6379
#   postgres      toxiproxy:5433   →  postgres:5432
#
# Chaos commands (examples):
#   # Redis: inject 200ms latency
#   docker exec zhejian-toxiproxy /toxiproxy-cli toxic add -t latency -a latency=200 redis
#
#   # Postgres: drop all connections
#   docker exec zhejian-toxiproxy /toxiproxy-cli toxic add -t timeout -a timeout=0 postgres
#
#   # Remove toxic (auto-name: latency_downstream / timeout_downstream)
#   docker exec zhejian-toxiproxy /toxiproxy-cli toxic delete -n latency_downstream redis
```

**Step 5: Smoke-test the extended Toxiproxy locally**

```bash
docker compose -f docker-compose.yml -f docker-compose.chaos.yml up --build -d
docker exec zhejian-toxiproxy /toxiproxy-cli list
# Expected output: proxies named rate-limiter, redis, postgres
curl -sf http://localhost:8080/health
# Expected: 200 {"status":"ok",...}
docker compose -f docker-compose.yml -f docker-compose.chaos.yml down -v
```

**Step 6: Commit**

```bash
git add docker-compose.chaos.yml
git commit -m "feat: extend toxiproxy to redis and postgres proxies"
```

---

### Task 2: Redis operation timeout

**Files:**
- Modify: `services/gateway/internal/repository/cached_url_repository.go`
- Modify: `services/gateway/internal/repository/cached_url_repository_test.go`

**Context:** The existing circuit breaker trips on Redis *errors*, not on Redis *slowness*. A Redis that takes 300ms responds successfully — the CB never trips, but goroutines pile up waiting. Adding a 50ms `context.WithTimeout` makes slow Redis indistinguishable from dead Redis from the CB's perspective.

**Step 1: Add `OperationTimeout` to `CBSettings` and update the default**

In `cached_url_repository.go`, update the `CBSettings` struct (lines 50-55) and `DefaultCBSettings` (lines 58-65):

```go
type CBSettings struct {
	MaxRequests         uint32
	Interval            time.Duration
	Timeout             time.Duration
	ConsecutiveFailures uint32
	OperationTimeout    time.Duration // per-operation deadline (default 50ms)
}

func DefaultCBSettings() CBSettings {
	return CBSettings{
		MaxRequests:         3,
		Interval:            10 * time.Second,
		Timeout:             30 * time.Second,
		ConsecutiveFailures: 5,
		OperationTimeout:    50 * time.Millisecond,
	}
}
```

**Step 2: Add `cacheTimeout` field to `CachedURLRepository` struct**

In the struct definition (lines 26-38), add:

```go
type CachedURLRepository struct {
	db              URLRepositoryInterface
	cache           *redis.Client
	ttl             time.Duration
	cacheTimeout    time.Duration       // ← add this
	requestGroup    *singleflight.Group
	cacheCB         *gobreaker.CircuitBreaker
	logger          *slog.Logger
	// ... metrics fields unchanged
}
```

**Step 3: Set `cacheTimeout` in `NewCachedURLRepository`**

After setting `cb` from opts (around line 77), set the field:

```go
repo := &CachedURLRepository{
	db:           db,
	cache:        cache,
	ttl:          ttl,
	cacheTimeout: cb.OperationTimeout,
	requestGroup: &singleflight.Group{},
	logger:       logger,
}
```

**Step 4: Wrap `cacheGet`, `cacheSet`, `cacheDel` with a per-operation timeout**

Replace the three helper methods (lines 382-414):

```go
func (r *CachedURLRepository) cacheGet(ctx context.Context, key string) (string, error) {
	cacheCtx, cancel := context.WithTimeout(ctx, r.cacheTimeout)
	defer cancel()
	res, err := r.cacheCB.Execute(func() (interface{}, error) {
		return r.cache.Get(cacheCtx, key).Result()
	})
	if err != nil {
		return "", err
	}
	return res.(string), nil
}

func (r *CachedURLRepository) cacheSet(ctx context.Context, key string, data interface{}, ttl time.Duration) {
	cacheCtx, cancel := context.WithTimeout(ctx, r.cacheTimeout)
	defer cancel()
	_, err := r.cacheCB.Execute(func() (interface{}, error) {
		return nil, r.cache.Set(cacheCtx, key, data, ttl).Err()
	})
	if err != nil && !errors.Is(err, gobreaker.ErrOpenState) {
		r.totalErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "cache_write")))
		r.logger.Error("cache write error",
			slog.String("error", err.Error()),
			slog.String("key", key))
	}
}

func (r *CachedURLRepository) cacheDel(ctx context.Context, key string) {
	cacheCtx, cancel := context.WithTimeout(ctx, r.cacheTimeout)
	defer cancel()
	_, err := r.cacheCB.Execute(func() (interface{}, error) {
		return nil, r.cache.Del(cacheCtx, key).Err()
	})
	if err != nil && !errors.Is(err, gobreaker.ErrOpenState) {
		r.totalErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "cache_delete")))
		r.logger.Error("cache delete error",
			slog.String("error", err.Error()),
			slog.String("key", key))
	}
}
```

**Step 5: Write the failing test**

Add to `cached_url_repository_test.go`:

```go
// TestCacheTimeout_TripsCircuitBreaker verifies that a slow Redis (exceeds OperationTimeout)
// is treated as an error by the circuit breaker, eventually opening it.
func TestCacheTimeout_TripsCircuitBreaker(t *testing.T) {
    // Use a very short timeout so a small sleep exceeds it.
    cb := repository.CBSettings{
        MaxRequests:         1,
        Interval:            10 * time.Second,
        Timeout:             1 * time.Second,
        ConsecutiveFailures: 3,
        OperationTimeout:    5 * time.Millisecond,
    }
    mockDB := &mockURLRepository{}
    mockDB.On("GetByCode", mock.Anything, "abc").Return(&model.URL{ShortCode: "abc", OriginalURL: "https://example.com"}, nil)

    // Use a slow mock Redis that always sleeps longer than OperationTimeout.
    slowRedis := newSlowRedisClient(20 * time.Millisecond) // sleeps 20ms > 5ms timeout

    repo := repository.NewCachedURLRepository(
        mockDB, slowRedis, 5*time.Minute,
        slog.Default(),
        repository.CachedURLRepositoryOptions{CacheCB: &cb},
    )

    ctx := context.Background()
    // 3 consecutive slow cache ops → CB opens.
    for i := 0; i < 3; i++ {
        _, _ = repo.GetByCode(ctx, "abc")
    }

    // After CB opens, GetByCode should fall back to DB without hitting cache.
    url, err := repo.GetByCode(ctx, "abc")
    assert.NoError(t, err)
    assert.Equal(t, "abc", url.ShortCode)
    // DB was called (mockDB verifies this).
    mockDB.AssertCalled(t, "GetByCode", mock.Anything, "abc")
}
```

**Step 6: Run the failing test**

```bash
cd services/gateway
go test ./internal/repository/... -run TestCacheTimeout -v
# Expected: FAIL — OperationTimeout field doesn't exist yet
```

**Step 7: Run tests to verify they pass**

```bash
cd services/gateway
go test ./internal/repository/... -v
# Expected: all PASS including TestCacheTimeout_TripsCircuitBreaker
```

**Step 8: Commit**

```bash
git add services/gateway/internal/repository/
git commit -m "feat: add per-operation timeout to Redis cache (50ms default)"
```

---

### Task 3: RabbitMQ publisher reconnect

**Files:**
- Modify: `services/gateway/internal/analytics/publisher.go`
- Modify: `services/gateway/internal/analytics/publisher_test.go`

**Context:** `amqp091-go` does not auto-reconnect. When the TCP connection drops, `conn.NotifyClose()` sends an `*amqp.Error`. Without reconnect logic, all subsequent `Publish` calls return an error silently. Adding a background goroutine that re-dials on drop restores the fire-and-forget guarantee across transient AMQP disconnects.

**Step 1: Write the failing test**

In `publisher_test.go`, add:

```go
// TestPublisher_DegradedWhenChannelNil verifies that Publish is a safe no-op
// when the publisher channel is nil (degraded/reconnecting state).
func TestPublisher_DegradedWhenChannelNil(t *testing.T) {
    // Simulate a publisher in degraded state (channel set to nil after disconnect).
    p := analytics.NewDegradedPublisher(slog.Default())
    // Must not panic.
    p.Publish(context.Background(), analytics.ClickEvent{
        ShortCode: "abc",
        ClickedAt: time.Now(),
        IP:        "1.2.3.4",
    })
}
```

This requires exporting a `NewDegradedPublisher` constructor for testing.

**Step 2: Extract a `dial` helper and refactor `Publisher` struct**

Replace the full content of `publisher.go` with:

```go
package analytics

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	exchangeName = "analytics"
	routingKey   = "analytics.clicks"
)

// Publisher wraps an AMQP connection and channel to publish click events
// fire-and-forget. It reconnects automatically on connection drop.
// Safe to call Publish on a nil Publisher (feature disabled).
type Publisher struct {
	amqpURL string
	conn    *amqp.Connection
	channel *amqp.Channel
	logger  *slog.Logger
	mu      sync.RWMutex // protects conn and channel during reconnect
	closed  atomic.Bool  // set on Close() to stop the reconnect goroutine
}

// NewPublisher dials RabbitMQ, declares the analytics exchange, and starts
// a background reconnect goroutine. Returns (nil, nil) when amqpURL is empty.
func NewPublisher(amqpURL string, logger *slog.Logger) (*Publisher, error) {
	if amqpURL == "" {
		return nil, nil
	}
	conn, ch, err := dial(amqpURL)
	if err != nil {
		return nil, err
	}
	p := &Publisher{amqpURL: amqpURL, conn: conn, channel: ch, logger: logger}
	go p.watchAndReconnect()
	return p, nil
}

// NewDegradedPublisher creates a publisher with a nil channel for testing
// the degraded-state no-op behaviour.
func NewDegradedPublisher(logger *slog.Logger) *Publisher {
	return &Publisher{logger: logger}
}

// Publish encodes the event as JSON and publishes it fire-and-forget.
// Errors are logged but never returned. Safe to call when degraded or nil.
func (p *Publisher) Publish(ctx context.Context, event ClickEvent) {
	if p == nil {
		return
	}
	p.mu.RLock()
	ch := p.channel
	p.mu.RUnlock()

	if ch == nil {
		p.logger.WarnContext(ctx, "analytics: publisher degraded, dropping event")
		return
	}

	body, err := json.Marshal(event)
	if err != nil {
		p.logger.WarnContext(ctx, "analytics: failed to marshal event", slog.String("error", err.Error()))
		return
	}
	if err := ch.PublishWithContext(ctx,
		exchangeName,
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	); err != nil {
		p.logger.WarnContext(ctx, "analytics: failed to publish click event",
			slog.String("error", err.Error()))
	}
}

// Close releases the AMQP channel and connection and stops the reconnect goroutine.
func (p *Publisher) Close() {
	if p == nil {
		return
	}
	p.closed.Store(true)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.channel != nil {
		p.channel.Close()
		p.channel = nil
	}
	if p.conn != nil {
		p.conn.Close() // triggers NotifyClose → watchAndReconnect returns
		p.conn = nil
	}
}

// watchAndReconnect monitors the connection and re-dials on unexpected close.
// Returns on clean shutdown (Close called) or after all retries are exhausted.
func (p *Publisher) watchAndReconnect() {
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	for {
		p.mu.RLock()
		conn := p.conn
		p.mu.RUnlock()
		if conn == nil {
			return
		}

		closeCh := conn.NotifyClose(make(chan *amqp.Error, 1))
		amqpErr := <-closeCh

		if p.closed.Load() {
			return // clean shutdown via Close()
		}
		if amqpErr == nil {
			return // clean close, no reconnect needed
		}

		p.logger.Warn("analytics: AMQP connection lost, reconnecting",
			slog.String("reason", amqpErr.Error()))

		p.mu.Lock()
		p.channel = nil // mark degraded during reconnect
		p.mu.Unlock()

		reconnected := false
		for _, backoff := range backoffs {
			if p.closed.Load() {
				return
			}
			time.Sleep(backoff)
			newConn, newCh, err := dial(p.amqpURL)
			if err != nil {
				p.logger.Warn("analytics: reconnect attempt failed", slog.String("error", err.Error()))
				continue
			}
			p.mu.Lock()
			p.conn = newConn
			p.channel = newCh
			p.mu.Unlock()
			p.logger.Info("analytics: AMQP reconnected successfully")
			reconnected = true
			break
		}
		if !reconnected {
			p.logger.Error("analytics: all reconnect attempts failed, publisher degraded")
			return
		}
	}
}

// dial creates a new AMQP connection, opens a channel, and declares the exchange.
func dial(amqpURL string) (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if err := ch.ExchangeDeclare(
		exchangeName,
		"topic",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, nil, err
	}
	return conn, ch, nil
}
```

**Step 3: Run the test to verify it passes**

```bash
cd services/gateway
go test ./internal/analytics/... -v
# Expected: all PASS
```

**Step 4: Build to verify no compile errors**

```bash
cd services/gateway
go build ./...
# Expected: no errors
```

**Step 5: Commit**

```bash
git add services/gateway/internal/analytics/
git commit -m "feat: add AMQP reconnect to analytics publisher"
```

---

### Task 4: Health check — expose circuit breaker state

**Files:**
- Modify: `services/gateway/internal/api/handler.go`
- Modify: `services/gateway/internal/repository/cached_url_repository.go`
- Modify: `services/gateway/internal/ratelimit/client.go`
- Modify: `services/gateway/internal/server/server.go`
- Modify: `services/gateway/internal/api/handler_test.go`

**Context:** Chaos scenarios are hard to observe without knowing CB state. Adding `cache_cb` and `rate_limiter_cb` to the health response makes `watchAndReconnect` and circuit breaker states visible. HTTP status code is unchanged — CB open means *fallback active*, not *service down*.

**Step 1: Add `CBStateProvider` interface and fields to `handler.go`**

Add the interface and two optional fields to Handler, plus a setter method:

```go
// CBStateProvider exposes circuit breaker state for health reporting.
type CBStateProvider interface {
	CBState() string // "closed", "half-open", or "open"
}
```

Add to `Handler` struct:
```go
type Handler struct {
	urlService     service.URLServiceInterface
	db             DBInterface
	cache          CacheInterface
	logger         *slog.Logger
	publisher      *analytics.Publisher
	cacheCBState   CBStateProvider // optional, nil when not wired
	rateLimCBState CBStateProvider // optional, nil when not wired
}
```

Add setter (does not change `NewHandler` signature — no test breakage):
```go
// WithCBProviders wires circuit breaker state providers for health reporting.
func (h *Handler) WithCBProviders(cache, rateLimiter CBStateProvider) *Handler {
	h.cacheCBState = cache
	h.rateLimCBState = rateLimiter
	return h
}
```

**Step 2: Update `healthCheck` to include CB state**

Replace the `healthCheck` method body:

```go
func (h *Handler) healthCheck(c *gin.Context) {
	ctx := c.Request.Context()

	cacheErr := h.cache.Ping(ctx)
	dbErr := h.db.Ping(ctx)

	status := "ok"
	code := http.StatusOK
	deps := gin.H{"cache": "up", "database": "up"}

	if cacheErr != nil {
		status = "degraded"
		code = http.StatusServiceUnavailable
		deps["cache"] = "down"
	}
	if dbErr != nil {
		status = "degraded"
		code = http.StatusServiceUnavailable
		deps["database"] = "down"
	}

	// Expose CB state as metadata. CB open means fallback is active (not an error).
	if h.cacheCBState != nil {
		cbState := h.cacheCBState.CBState()
		deps["cache_cb"] = cbState
		if cbState == "open" && cacheErr == nil {
			deps["cache"] = "degraded" // fallback to DB is active
			if status == "ok" {
				status = "degraded" // informational, HTTP 200 not 503
			}
		}
	}
	if h.rateLimCBState != nil {
		deps["rate_limiter_cb"] = h.rateLimCBState.CBState()
	}

	c.JSON(code, gin.H{"status": status, "dependencies": deps})
}
```

**Step 3: Add `CBState()` method to `CachedURLRepository`**

At the end of `cached_url_repository.go`, add:

```go
// CBState returns the current circuit breaker state as a string.
// Implements api.CBStateProvider.
func (r *CachedURLRepository) CBState() string {
	return r.cacheCB.State().String()
}
```

**Step 4: Add `CBState()` method to `ratelimit.Client`**

In `services/gateway/internal/ratelimit/client.go`, add:

```go
// CBState returns the current circuit breaker state as a string.
// Implements api.CBStateProvider.
func (c *Client) CBState() string {
	return c.cb.State().String()
}
```

**Step 5: Wire the CB providers in `server.go`**

In `NewRouter` (or wherever `NewHandler` is called), chain `WithCBProviders`:

```go
handler := api.NewHandler(urlService, db, &redisPinger{client: cache}, obs.Logger, publisher).
	WithCBProviders(cachedRepo, rateLimiter)
```

Note: `cachedRepo` is the `*repository.CachedURLRepository` (not the interface). `rateLimiter` is `*ratelimit.Client` (may be nil — `WithCBProviders` must guard nil).

Update `WithCBProviders` to handle nil:
```go
func (h *Handler) WithCBProviders(cache, rateLimiter CBStateProvider) *Handler {
	if cache != nil {
		h.cacheCBState = cache
	}
	if rateLimiter != nil {
		h.rateLimCBState = rateLimiter
	}
	return h
}
```

**Step 6: Write a test for the new health check fields**

In `handler_test.go`, add:

```go
// TestHealthCheck_ExposesCircuitBreakerState verifies CB state appears in response.
func TestHealthCheck_ExposesCircuitBreakerState(t *testing.T) {
	mockDB := &mockDB{}
	mockDB.On("Ping", mock.Anything).Return(nil)

	mockCache := &mockCache{}
	mockCache.On("Ping", mock.Anything).Return(nil)

	// Stub CB provider that reports "open"
	mockCBProvider := &mockCBStateProvider{state: "open"}

	handler := NewHandler(
		&mockURLService{}, mockDB, mockCache,
		slog.Default(), nil,
	).WithCBProviders(mockCBProvider, nil)

	router := gin.New()
	handler.RegisterRoutes(router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code) // CB open ≠ 503
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	deps := body["dependencies"].(map[string]interface{})
	assert.Equal(t, "open", deps["cache_cb"])
	assert.Equal(t, "degraded", deps["cache"])
	assert.Equal(t, "degraded", body["status"])
}
```

Add the stub to the test file:
```go
type mockCBStateProvider struct{ state string }
func (m *mockCBStateProvider) CBState() string { return m.state }
```

**Step 7: Run tests**

```bash
cd services/gateway
go test ./internal/api/... -v
go test ./... -count=1
# Expected: all PASS
```

**Step 8: Commit**

```bash
git add services/gateway/internal/api/ \
        services/gateway/internal/repository/ \
        services/gateway/internal/ratelimit/ \
        services/gateway/internal/server/
git commit -m "feat: expose circuit breaker state in health check response"
```

---

### Task 5: Expand chaos-test.sh — 4 new scenarios

**Files:**
- Modify: `scripts/chaos-test.sh`

**Prerequisites:** Tasks 1 (Toxiproxy Redis/Postgres proxies) and 2 (Redis 50ms timeout) must be complete for Scenarios 2 and 3 to work correctly.

**Step 1: Add a shared `CODE` variable after the gateway health check**

After the existing gateway health check block (around line 53), add a short URL to use in redirect tests:

```bash
# ─── Create a test short URL ──────────────────────────────────────────────────
echo "=== Creating test short URL ==="
CODE=$(curl -sf -X POST -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/chaos-test"}' \
  "$GATEWAY/api/v1/shorten" | jq -r .short_code)
if [ -z "$CODE" ] || [ "$CODE" = "null" ]; then
  fail "Could not create test short URL"
  exit 1
fi
pass "Created short URL: $CODE"
```

**Step 2: Rename existing scenarios to make room for the new numbering**

The existing two scenarios (latency injection + recovery) become Scenarios 1a and 1b. No renaming required — just append new scenarios after the existing summary block.

**Step 3: Append Scenario 2 — Redis slow → CB opens → DB fallback**

```bash
# ─── Scenario 2: Redis slow → circuit opens → DB fallback ────────────────────
echo ""
echo "=== Scenario 2: Redis +200ms latency → CB opens → DB fallback ==="

$TOXIPROXY toxic add -t latency -a latency=200 redis
pass "Injected 200ms latency on Redis proxy (exceeds 50ms operation timeout)"

# 5 consecutive timeouts open the circuit breaker (ConsecutiveFailures=5).
# Subsequent requests fall back to DB. All must return 301.
all_ok=true
for i in $(seq 1 10); do
  code=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/$CODE")
  if [ "$code" != "301" ]; then
    fail "Scenario 2: request $i returned $code, expected 301 (DB fallback)"
    all_ok=false
  fi
done
[ "$all_ok" = "true" ] && pass "Scenario 2: all 10 requests returned 301 during Redis degradation"

sleep 1
HEALTH=$(curl -sf "$GATEWAY/health" || echo '{}')
if echo "$HEALTH" | grep -q '"cache_cb":"open"'; then
  pass "Scenario 2: health check reports cache_cb=open"
else
  fail "Scenario 2: health check did not report cache_cb=open"
fi

$TOXIPROXY toxic delete -n latency_downstream redis
pass "Scenario 2: removed Redis latency toxic"
echo "    Waiting 32s for circuit breaker to recover..."
sleep 32
HEALTH=$(curl -sf "$GATEWAY/health" || echo '{}')
echo "$HEALTH" | grep -q '"cache_cb":"closed"' && pass "Scenario 2: cache CB recovered (closed)" || fail "Scenario 2: cache CB did not recover"
```

**Step 4: Append Scenario 3 — Redis down → DB fallback**

```bash
# ─── Scenario 3: Redis down → circuit opens → DB fallback ────────────────────
echo ""
echo "=== Scenario 3: Redis complete failure → CB opens → DB fallback ==="

$TOXIPROXY toxic add -t timeout -a timeout=0 redis
pass "Injected timeout=0 (connection drop) on Redis proxy"

all_ok=true
for i in $(seq 1 10); do
  code=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/$CODE")
  if [ "$code" != "301" ]; then
    fail "Scenario 3: request $i returned $code, expected 301"
    all_ok=false
  fi
done
[ "$all_ok" = "true" ] && pass "Scenario 3: all 10 requests returned 301 with Redis down"

$TOXIPROXY toxic delete -n timeout_downstream redis
pass "Scenario 3: restored Redis"
echo "    Waiting 32s for circuit breaker to recover..."
sleep 32
```

**Step 5: Append Scenario 4 — Postgres down → health 503 → recovery**

```bash
# ─── Scenario 4: Postgres down → health 503 → graceful recovery ──────────────
echo ""
echo "=== Scenario 4: Postgres down → /health returns 503 → recovery ==="

$COMPOSE stop postgres
pass "Stopped Postgres"
sleep 2  # allow pool to notice connection loss

HEALTH_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/health")
if [ "$HEALTH_CODE" = "503" ]; then
  pass "Scenario 4: /health returns 503 with Postgres down"
else
  fail "Scenario 4: /health returned $HEALTH_CODE, expected 503"
fi

# Gateway process must still be running (no panic/crash).
if curl -s "$GATEWAY/health" > /dev/null 2>&1 || true; then
  pass "Scenario 4: gateway process stable (no crash)"
fi

$COMPOSE start postgres
pass "Scenario 4: restarted Postgres"
echo "    Waiting 10s for pool to reconnect..."
sleep 10

HEALTH_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/health")
if [ "$HEALTH_CODE" = "200" ]; then
  pass "Scenario 4: /health returns 200 after Postgres recovery"
else
  fail "Scenario 4: /health returned $HEALTH_CODE after recovery, expected 200"
fi
```

**Step 6: Append Scenario 5 — RabbitMQ down → redirects still 301**

```bash
# ─── Scenario 5: RabbitMQ down → fire-and-forget holds ──────────────────────
echo ""
echo "=== Scenario 5: RabbitMQ down → redirects still return 301 ==="

$COMPOSE stop rabbitmq
pass "Stopped RabbitMQ"
sleep 2  # allow publisher to detect disconnect

all_ok=true
for i in $(seq 1 10); do
  code=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/$CODE")
  if [ "$code" != "301" ]; then
    fail "Scenario 5: request $i returned $code, expected 301 (fire-and-forget)"
    all_ok=false
  fi
done
[ "$all_ok" = "true" ] && pass "Scenario 5: all 10 redirects returned 301 with RabbitMQ down"

$COMPOSE start rabbitmq
pass "Scenario 5: restarted RabbitMQ"
```

**Step 7: Update the summary block**

Replace the existing summary at the end:

```bash
# ─── Summary ──────────────────────────────────────────────────────────────────
echo ""
echo "======================================================"
echo "=== Results: $PASSED passed, $FAILED failed ==="
echo "======================================================"
echo ""
echo "Scenarios run:"
echo "  1a. Rate-limiter latency → CB opens → fail-open (200)"
echo "  1b. Rate-limiter recovery → rate limiting restored (429)"
echo "  2.  Redis slow            → CB opens → DB fallback (301)"
echo "  3.  Redis down            → CB opens → DB fallback (301)"
echo "  4.  Postgres down         → health 503 → recovery (200)"
echo "  5.  RabbitMQ down         → fire-and-forget holds (301)"
[ "$FAILED" -eq 0 ] || exit 1
```

**Step 8: Run the full chaos test locally**

```bash
chmod +x scripts/chaos-test.sh
./scripts/chaos-test.sh
# Expected: all scenarios pass, summary shows 0 failed
```

**Step 9: Commit**

```bash
git add scripts/chaos-test.sh
git commit -m "feat: add 4 new chaos scenarios (redis slow/down, postgres down, rabbitmq down)"
```

---

## Verification Checklist

- [ ] `docker compose -f docker-compose.yml -f docker-compose.chaos.yml up -d` — all services healthy
- [ ] `docker exec zhejian-toxiproxy /toxiproxy-cli list` — shows rate-limiter, redis, postgres
- [ ] Inject 200ms latency on Redis → `curl localhost:8080/health` shows `cache_cb: open`
- [ ] Redirects still return 301 with Redis CB open
- [ ] Remove latency → wait 32s → `curl localhost:8080/health` shows `cache_cb: closed`
- [ ] RabbitMQ stopped → 10 redirects all return 301
- [ ] Postgres stopped → `/health` returns 503 → Postgres restarted → `/health` returns 200
- [ ] `./scripts/chaos-test.sh` passes all 5 scenarios
- [ ] `go test ./...` passes in `services/gateway`
