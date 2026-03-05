# URL Shortener Build Phases - Production-Grade Plan

This document outlines the refined build order optimized for production quality and realistic failure simulation.

---

## Phase 1: Core Foundation ✅ COMPLETED

### Objective
Get a minimal working URL shortener with PostgreSQL storage.

### Tasks
1. **Database Schema Design**
   - URLs table (short_code, original_url, created_at, expires_at, click_count)
   - Analytics table (click_id, short_code, clicked_at, ip, user_agent, referer)
   - Indexes for fast lookups (short_code, created_at)

2. **Go Gateway - Basic CRUD**
   - POST /api/v1/shorten - Create short URL
   - GET /:code - Redirect to original URL
   - GET /api/v1/urls/:code - Get URL metadata
   - DELETE /api/v1/urls/:code - Delete URL

3. **Short Code Generation**
   - Base62 encoding
   - Collision detection
   - Optional custom aliases

### Deliverables
- Working API with PostgreSQL
- Basic error handling
- Request logging
- Unit tests for core logic

---

## Phase 2: CI & Test Automation ✅ COMPLETED

### Objective
Verify code quality and run tests automatically on every push and pull request.

### Tasks
1. **CI Workflow**
   - GitHub Actions workflow on push/PR
   - Steps: `go fmt`, `go vet`, `golangci-lint`, `go test ./... -v`
   - Fast unit tests + integration tests with dockerized Postgres

### Deliverables
- `.github/workflows/ci.yml`
- Linting and test jobs passing
- Code coverage reporting (target: 70%+)

---

## Phase 3: Build & Deployment (CD) ✅ COMPLETED

### Objective
Production-ready containerization with automated database migrations.

### Implementation

1. **Init Container Pattern** ✅
   - Migration service runs golang-migrate before gateway starts
   - `migrations/Dockerfile` + `migrate-entrypoint.sh`
   - Generic SQL file copying: `COPY *.sql ./`
   - Down migrations for rollback capability

2. **Multi-Stage Dockerfiles** ✅
   - Gateway: Go builder + Alpine runtime (static binary)
   - Built-in health check: `curl -f /health`
   - Optimized image sizes

3. **Docker Compose V2 Production** ✅
   - `docker-compose.prod.yml` with service dependencies
   - Startup flow: Postgres (healthy) → Migrations (completed) → Gateway
   - Environment config via `.env`
   - PostgreSQL persistent volumes

4. **Migration Strategy** ✅
   - golang-migrate v4.19.1
   - Idempotent migrations with IF NOT EXISTS
   - Schema tracking via `schema_migrations` table

5. **CI/CD Production Build Testing** ✅
   - GitHub Actions workflow for automated production build verification
   - Docker Compose build and startup verification
   - Migration completion verification
   - Health endpoint smoke test with retry logic
   - Automatic cleanup with volume removal

### Deliverables
- ✅ `services/gateway/Dockerfile`
- ✅ `migrations/Dockerfile` + `migrate-entrypoint.sh`
- ✅ `docker-compose.prod.yml` (Docker Compose V2)
- ✅ `migrations/schema/000001_init.down.sql`
- ✅ `.github/workflows/production.yml` (CI/CD build testing)
- ✅ Working deployment with health checks

### Verified Results
```bash
# Local: Services start in correct order
postgres → migrations (exited 0) → gateway (running)

# Local: Health endpoint works
curl http://localhost:8080/health
# {"status":"ok"}

# CI/CD: Workflow validates
# ✓ Docker Compose builds successfully
# ✓ Migrations complete successfully (checks for "Migrations completed!" log)
# ✓ Gateway health endpoint responds
# ✓ Automatic cleanup
```

---

## Phase 4: Caching Layer ✅ COMPLETED

### Objective
Add Redis caching with graceful degradation.

### Tasks
1. **Redis Integration** ✅
   - Connection pool with retry logic
   - Cache-aside pattern (read-through on miss)
   - Write-through on URL creation
   - TTL-based expiration

2. **Cache Strategies** ✅
   - Cache stampede prevention (singleflight pattern)
   - Negative caching for non-existent URLs

3. **Graceful Degradation** ✅
   - Circuit breaker for Redis calls
   - Fall back to PostgreSQL on cache failure
   - Configurable failure injection for testing

### Deliverables
- ✅ Redis caching working
- ✅ Measurable latency improvement (baseline: <50ms p99)
- ✅ Cache hit ratio tracking (target: >80%)
- ✅ Failure injection config for Redis

### Performance Targets
- Cache hit: <5ms p99
- Cache miss: <50ms p99
- Cache hit ratio: >80% under steady load

---

## Phase 5: Observability Foundation (OTel + Prometheus + Jaeger) ✅ COMPLETED

### Objective
Add distributed tracing and metrics instrumentation using OpenTelemetry as the unified instrumentation layer, with Prometheus for metrics storage and Jaeger for trace visualization. Setting up observability before adding more services ensures every future service is traced from day one.

### Tasks
1. **Trace Context Propagation & Structured Logging** ✅
   - W3C TraceContext propagation middleware (`propagation.TraceContext{}`)
   - **Structured logging with `slog` (Go standard library)**
     - JSON log format
     - Trace ID and Span ID injected into log fields (correlate logs ↔ traces)
     - HTTP middleware logging (method, path, status, duration)
     - Error logging with trace context
   - Prepares for cross-service propagation (gRPC, AMQP) in later phases

2. **OpenTelemetry SDK Integration** ✅
   - `go.opentelemetry.io/otel` SDK setup in the gateway
   - Trace spans for HTTP handlers (`otelgin` middleware)
   - Trace spans for Redis cache operations (hit/miss/error with `cache.hit`, `cache.negative` attributes)
   - Trace spans for PostgreSQL queries (`db.system`, `db.operation` attributes)
   - Span attributes: `http.method`, `http.status_code`, `db.system`, `cache.hit`, etc.

3. **Prometheus Metrics via OTel** ✅
   - OTel metrics SDK with Prometheus exporter (`MeterProvider`)
   - `/metrics` endpoint exposed on the gateway (`promhttp.Handler()`)
   - Key metrics:
     - `http_request_duration_seconds` (histogram) — via metrics middleware
     - `http_requests_total` (counter) — via metrics middleware
     - `cache_hits_total` / `cache_misses_total` (counters)
     - `db_query_duration_seconds` (histogram)
     - `errors_total` by type (counter)
     - `circuit_breaker_state` (gauge — observable callback)

4. **Infrastructure Setup** ✅
   - Jaeger all-in-one for trace visualization (OTLP gRPC on :4317, UI on :16686)
   - Prometheus for metrics scraping (scrapes gateway `/metrics`, UI on :9090)
   - Added to both `docker-compose.yml` (dev) and `docker-compose.prod.yml`
   - Direct connections (Gateway → Jaeger, Prometheus → Gateway) — OTel Collector deferred to Phase 6+ when multiple services need a central pipeline

### Deliverables
- ✅ OTel SDK integrated in gateway (traces + metrics)
- ✅ Prometheus `/metrics` endpoint with key application metrics
- ✅ Jaeger UI accessible for trace inspection
- ✅ Trace spans covering: HTTP request → cache lookup → DB query → response
- ✅ W3C TraceContext propagation ready for future services
- ✅ **Structured JSON logs with trace correlation using `slog`**

### Architecture
```
Gateway (OTel SDK)
  │
  ├─ traces (OTLP gRPC) ──→ Jaeger :4317
  │
  └─ /metrics ──────────────→ Prometheus scrapes :8080/metrics
```

### Implementation Details
- `observability/tracer.go` — TracerProvider with OTLP gRPC exporter
- `observability/meter.go` — MeterProvider with Prometheus exporter
- `observability/logger.go` — slog JSON logger
- `observability/observability.go` — unified Setup/Shutdown
- `middleware/logging.go` — HTTP request logging with trace correlation
- `middleware/metrics.go` — HTTP request duration + count metrics
- `repository/cached_url_repository.go` — cache/DB spans + metrics
- `observability/prometheus/prometheus.yml` — Prometheus scrape config (prod)
- `observability/prometheus/prometheus.dev.yml` — Prometheus scrape config (dev)

---

## Phase 6: Rust Rate Limiter with gRPC ✅ COMPLETED

### Objective
Build a low-latency rate limiting service in Rust with gateway-first architecture. Demonstrates cross-language gRPC, token bucket algorithm, Redis-backed distributed state, and circuit breaker fail-open pattern.

### Scope (adjusted from original plan)
- Per-IP rate limiting: 100 req/min, all endpoints
- Fail-open on circuit breaker (no local fallback)
- Toxiproxy wired in from day 1 for immediate chaos testing
- API key rate limiting deferred to future

### Tasks
1. **Proto Definition**
   - `proto/ratelimit.proto` at repo root (shared between Go + Rust)
   - `RateLimitService.CheckRateLimit(ip) → (allowed, remaining, retry_after_ms)`

2. **Rust gRPC Service**
   - `services/rate-limiter/` with tonic + tokio + redis crate
   - Token bucket algorithm via atomic Redis Lua script (single round-trip)
   - `REDIS_URL`, `GRPC_PORT` (50051), `RATE_LIMIT` (100/min), `BURST` (50) env vars
   - Multi-stage Dockerfile (Rust builder → Alpine runtime)

3. **Gateway Integration**
   - Hand-written Go gRPC client stubs in `services/gateway/internal/ratelimit/`
   - `RateLimiterConfig` in config (`RATE_LIMITER_ADDR`, `RATE_LIMITER_TIMEOUT=100ms`)
   - Rate limit middleware: returns 429 with `Retry-After` header, fails open on error
   - Circuit breaker: 3 consecutive failures to trip, 30s recovery
   - Middleware order: `otelgin → Logging → Metrics → RateLimit → routes`

4. **Toxiproxy Infrastructure** 🎯
   - Toxiproxy in Docker Compose as proxy in front of rate-limiter
   - Gateway always connects via `toxiproxy:50052` (not directly to rate-limiter)
   - `toxiproxy-init` one-shot container configures proxy rule on startup
   - Zero application code changes needed for Phase 8 chaos testing

### Architecture
```
Client → Gateway (Go/Gin)
              │
              ├─ RateLimit middleware ─── gRPC 100ms timeout ──→ toxiproxy:50052
              │       ↓ fail-open                                       │
              │  (circuit breaker)                              rate-limiter:50051 (Rust)
              └─ routes                                                  │
                                                                   Redis HMSET/Lua
```

### Deliverables
- ✅ `proto/ratelimit.proto`
- ✅ `services/rate-limiter/` — Rust gRPC service
- ✅ `services/gateway/internal/ratelimit/` — Go gRPC client + middleware
- ✅ Token bucket enforcing 100 req/min per IP
- ✅ Circuit breaker fail-open verified via Toxiproxy latency injection
- ✅ `docker-compose.chaos.yml` — Toxiproxy overlay (composable with dev + prod stacks)
- ✅ `scripts/chaos-test.sh` — automated chaos scenarios (latency injection → circuit open → recovery)
- ✅ `.github/workflows/chaos.yml` — CI chaos workflow (manual + auto on PR/push to main)

### Chaos Verification (available immediately after Phase 6)
```bash
# Scenario 1: Rate limiter complete failure → fail-open
docker exec zhejian-toxiproxy /toxiproxy-cli toxic add rate-limiter -t timeout -a timeout=0
curl http://localhost:8080/health  # 200 (fail-open)

# Scenario 2: 500ms latency → hits 100ms gRPC timeout → circuit breaker opens
docker exec zhejian-toxiproxy /toxiproxy-cli toxic add rate-limiter -t latency -a latency=500

# Scenario 3: Normal operation → 429 after 100 requests/min
docker exec zhejian-toxiproxy /toxiproxy-cli toxic remove rate-limiter latency
for i in $(seq 1 110); do curl -s -o /dev/null -w "%{http_code}\n" localhost:8080/health; done
```

---

## Phase 7: Async Click Analytics with RabbitMQ ✅ COMPLETED

### Objective
Demonstrate async processing with a message queue: the redirect handler publishes click events fire-and-forget, RabbitMQ absorbs the burst, and a separate Go worker batch-inserts events into PostgreSQL. Story told through architecture and metrics — no sync/async toggle needed.

### Scope (adjusted from original plan)
- Fire-and-forget publish — RabbitMQ unavailability is logged only, never surfaces to client
- Analytics worker: separate Go service, own Docker container and `go.mod`
- Batch strategy: flush when 100 events accumulated OR 5s timeout (whichever first)
- No sync/async toggle — complexity without portfolio value
- Traffic simulation via k6 with Zipf distribution (no real users needed)

### Tasks
1. **Migration** — `analytics` table + indexes on `short_code` and `clicked_at`

2. **Gateway publisher** (`services/gateway/internal/analytics/`)
   - `Publisher` wraps `amqp091-go` connection + channel
   - `NewPublisher(amqpURL)` returns `nil, nil` if `AMQP_URL` empty (feature disabled)
   - `Publish(ctx, ClickEvent)` called in goroutine after redirect — never blocks response
   - Event schema: `short_code`, `clicked_at`, `ip`, `referer` (user_agent dropped)

3. **RabbitMQ topology** (declared by worker on startup, idempotent)
   - Exchange: `analytics` (topic, durable)
   - Queue: `analytics.clicks` (durable, dead-letter → DLQ)
   - DLQ: `analytics.clicks.dlq` (durable)

4. **Analytics worker** (`services/analytics-worker/`)
   - Consumes `analytics.clicks` with manual ack, prefetch = 2× batch size
   - Flush: bulk `INSERT INTO analytics ...` → `Ack(multiple=true)`
   - On malformed JSON: individual `Nack(requeue=false)` → routed to DLQ (permanent failure)
   - On DB error: return error without ack/nack → `log.Fatalf` → process exits (code 1)
     → `restart: on-failure` restarts the container → RabbitMQ requeues all unacked
     messages automatically when the connection closes — no data loss, no tight retry loop
   - Graceful shutdown: flushes remaining batch before exit

5. **k6 simulation** (`tests/analytics-load.js`)
   - Setup: creates 10 short URLs
   - Load: 50 VUs × 65s with Zipf weights (top URL ~40%, second ~20%)
   - Each VU uses a distinct `X-Forwarded-For` IP to stay within the per-IP rate limit
   - Sleep 700ms per iteration (1.4 req/s ≤ 100 req/min per-IP limit)
   - Generated ~4100 events in a real run with Zipf distribution confirmed in DB

### Architecture
```
Client → Gateway (Go/Gin)
              │  redirect → 301 → go Publish(ClickEvent)  [fire-and-forget]
              │
              └─── amqp091-go ──→ RabbitMQ
                                     exchange: analytics (topic)
                                     queue: analytics.clicks (durable)
                                     DLQ:  analytics.clicks.dlq (malformed JSON)
                                           │
                                           ▼
                                 analytics-worker (Go)
                                   batch (100 OR 5s) → bulk INSERT
                                   DB error → crash → docker restart → requeue
                                           │
                                           ▼
                                      PostgreSQL → analytics table
```

### Deliverables
- `migrations/schema/000002_analytics.up.sql` — analytics table + indexes
- `services/gateway/internal/analytics/` — Publisher (amqp091-go, fire-and-forget)
- `services/analytics-worker/` — Go consumer service with batch flush + DLQ routing
- `tests/analytics-load.js` — k6 Zipf simulation with per-VU IP spoofing
- RabbitMQ + analytics-worker added to `docker-compose.yml` and `docker-compose.prod.yml`
- `go.yml` CI: analytics-worker build + vet + test + lint job (separate Go module)
- `production.yml` CI: analytics smoke test — redirect → sleep 10s → verify DB row
- `production.yml` CI: fire-and-forget resilience test — stop worker → assert 301 still returned

### Verification
```bash
# Start stack
docker compose up --build -d

# Fire 5 redirects, wait for flush, check rows
CODE=$(curl -sf -X POST -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com"}' http://localhost:8080/api/v1/shorten | jq -r .short_code)
for i in $(seq 1 5); do curl -s -o /dev/null "http://localhost:8080/$CODE"; done
sleep 6
docker compose exec postgres psql -U zhejian -d urlshortener \
  -c "SELECT count(*) FROM analytics;"
# Expected: 5

# k6 simulation
k6 run tests/analytics-load.js
# Expected: ~4000 rows with Zipf distribution

# Resilience: stop worker → redirects still 301 (fire-and-forget, gateway unaffected)
docker compose stop analytics-worker
curl -s -o /dev/null -w "%{http_code}" "http://localhost:8080/$CODE"
# Expected: 301

# Resilience: restart worker → drains queue, resumes processing
docker compose start analytics-worker
sleep 6
docker compose exec postgres psql -U zhejian -d urlshortener \
  -c "SELECT count(*) FROM analytics;"
# Expected: same count as before stop (no double-counting, no data loss)
```

---

## Phase 8: Resilience Patterns & Chaos Testing ✅ COMPLETED

### Objective
Close the remaining resilience gaps (Redis operation timeout, RabbitMQ publisher reconnect), extend Toxiproxy coverage from rate-limiter-only to all dependencies, improve health check observability, and build a comprehensive automated chaos test suite with 5 scenarios that prove each pattern holds under failure.

### What already existed (from earlier phases)
- Circuit breaker on Redis (`sony/gobreaker`) — 5 consecutive failures → open → DB fallback
- Circuit breaker on Rate Limiter (`sony/gobreaker`) — 5 consecutive failures → open → fail-open
- Fail-open for rate limiter — unavailable → allow all requests
- Cache-aside with DB fallback, singleflight, negative caching
- HTTP timeouts (10s), gRPC timeout (100ms), graceful shutdown
- Toxiproxy + `chaos-test.sh` — **rate-limiter only** (2 scenarios: latency + recovery)

### What was dropped from original plan and why
- **Circuit breaker on Postgres** — DB is the critical path; if it's down you 503 either way; fail-fast is already provided by pgx pool timeout
- **Circuit breaker on RabbitMQ publish** — publisher already swallows errors silently (fire-and-forget); a CB adds no observable difference
- **Exponential backoff / retry logic** — DB queries should fail fast; analytics retries via docker restart; short code collision retry already exists
- **Backpressure / request queue limits** — the Rust rate limiter already handles request rate at the token bucket level
- **Bloom filter** — negative caching (1-minute sentinel) already prevents repeated DB queries for non-existent URLs
- **Cache warming** — premature optimisation; relevant only at scale

### Tasks

**Task 1: Extend Toxiproxy to Redis and Postgres** ✅

Added Redis and Postgres proxies to `docker-compose.chaos.yml` and routed gateway through them via env vars — no gateway code changes required.

```yaml
# docker-compose.chaos.yml
toxiproxy-init:
  # registers:
  #   redis    → redis:6379    (listen :6380)
  #   postgres → postgres:5432 (listen :5433)

gateway:
  environment:
    CACHE_HOST: toxiproxy
    CACHE_PORT: "6380"
    # Tighten Redis timeouts so Scenario 2 latency injection (200ms)
    # reliably exceeds the client deadline and triggers the circuit breaker.
    CACHE_READ_TIMEOUT: "100ms"
    CACHE_WRITE_TIMEOUT: "100ms"
    DB_HOST: toxiproxy
    DB_PORT: "5433"
```

**Task 2: Redis operation timeout** ✅

- Per-operation `context.WithTimeout(ctx, 50ms)` wrapping `cacheGet` / `cacheSet` / `cacheDel`
- Slow Redis → deadline exceeded → CB failure → 5 ops → CB opens → DB fallback
- `CACHE_OPERATION_TIMEOUT` env var (default 50ms); configurable via `CBSettings.OperationTimeout`
- Redis TCP timeouts added as belt-and-suspenders: `CACHE_READ_TIMEOUT` / `CACHE_WRITE_TIMEOUT` (configurable via `CacheConfig`, default 500ms, chaos overlay uses 100ms)

**Task 3: RabbitMQ publisher reconnect** ✅

- Background goroutine monitors `conn.NotifyClose()` channel
- On close: re-dials with backoff (3 attempts: 1s / 2s / 4s), atomically swaps `conn` + `channel`
- If all retries fail: publisher degrades to no-ops until next reconnect attempt
- Publisher AMQP heartbeat set to 2s (`amqp.DialConfig`) — worst-case disconnect detection ≤4s even on WSL2 where TCP RST may not propagate immediately to idle connections
- Errors logged only — redirect path is never affected

**Task 4: Health check — expose circuit breaker state** ✅

- Added `cache_cb` and `rate_limiter_cb` fields: `"closed"` / `"half-open"` / `"open"`
- `"degraded"` status when CB is open (fallback active, not down) vs `"down"` when ping fails
- HTTP 503 only when Postgres or Redis is confirmed unreachable (preserves existing behaviour)

```json
{
  "status": "degraded",
  "dependencies": {
    "database":        "up",
    "cache":           "degraded",
    "cache_cb":        "open",
    "rate_limiter_cb": "closed"
  }
}
```

**Task 5: Expand chaos-test.sh — 5 complete scenarios** ✅

| # | Failure injected | Pattern under test | Pass condition |
|---|---|---|---|
| 1 | Rate-limiter +500ms latency | CB + fail-open | All 10 requests 200, CB state logged, 429 restored after recovery |
| 2 | Redis +200ms latency via Toxiproxy | Redis CB + DB fallback | All 10 requests 301, `cache_cb=open` in health, CB closes after recovery |
| 3 | Redis down (toxic timeout=0) | Redis CB open → DB fallback | All 10 requests 301, CB recovers |
| 4 | Postgres down (`docker compose stop`) | Health 503, graceful degradation | `/health` = 503, redirects stable, Postgres restart → 200 |
| 5a | RabbitMQ `docker compose stop` | Fire-and-forget analytics | All 10 redirects 301 |
| 5b | RabbitMQ disconnect | Publisher reconnect | Gateway + worker both log disconnect within ≤4s |

Each scenario: inject → verify → remove → verify recovery.

### Key design decisions
- **`CACHE_READ_TIMEOUT=100ms` in chaos overlay**: 200ms Toxiproxy latency reliably exceeds the 100ms TCP deadline → CB trip. Production default is 500ms.
- **`context.WithTimeout` vs TCP ReadTimeout**: context deadline fires first when propagated; TCP timeout is the backstop for library-level bypass. Both serve distinct failure modes.
- **AMQP heartbeat=2s**: negotiated with RabbitMQ server (min of client/server); worst-case detection = 2 missed beats = 4s. Removes dependency on WSL2 TCP RST propagation.
- **CB health check during Redis chaos**: `redisPinger.Ping()` also times out (100ms < 200ms), so health returns 503. `curl -s` (not `-sf`) is used to get the JSON body regardless of HTTP status.
- **CB HalfOpen → Closed recovery**: requires `MaxRequests=3` successful `cacheCB.Execute()` calls. Three probe redirects are fired after the 32s CB timeout.

### Deliverables
- ✅ `docker-compose.chaos.yml` — Toxiproxy extended to Redis + Postgres proxies
- ✅ `services/gateway/internal/config/config.go` — `CACHE_READ_TIMEOUT` / `CACHE_WRITE_TIMEOUT` env vars
- ✅ `services/gateway/internal/infra/infra.go` — configurable Redis `ReadTimeout` / `WriteTimeout`
- ✅ `services/gateway/internal/repository/cached_url_repository.go` — Redis operation timeout (50ms), configurable via `CBSettings.OperationTimeout`
- ✅ `services/gateway/internal/analytics/publisher.go` — AMQP reconnect with backoff + 2s heartbeat
- ✅ `services/gateway/internal/api/handler.go` — CB state in health check response
- ✅ `services/gateway/internal/server/server.go` — nil-safe `CBStateProvider` wiring
- ✅ `scripts/chaos-test.sh` — 5 scenarios with explicit pass/fail assertions and recovery verification

### Verification
```bash
# Run all 5 chaos scenarios
./scripts/chaos-test.sh

# Run a specific scenario
./scripts/chaos-test.sh --scenario 2

# Expected output: all assertions pass
# === Results: N passed, 0 failed ===
```

---

## Phase 9: Load Testing

### Objective
Validate system performance under realistic load.

### Tasks
1. **k6 Test Scenarios**

   **Baseline Load Test**
   ```javascript
   // tests/baseline.js
   export let options = {
     stages: [
       { duration: '2m', target: 100 },   // Ramp up
       { duration: '5m', target: 100 },   // Steady state
       { duration: '2m', target: 0 },     // Ramp down
     ],
     thresholds: {
       http_req_duration: ['p(95)<100'],  // 95% under 100ms
       http_req_failed: ['rate<0.01'],    // <1% errors
     },
   };
   ```

   **Spike Test**
   ```javascript
   // tests/spike.js
   export let options = {
     stages: [
       { duration: '1m', target: 100 },
       { duration: '30s', target: 1000 },  // Sudden spike
       { duration: '1m', target: 100 },
     ],
   };
   ```

   **Endurance Test**
   ```javascript
   // tests/endurance.js
   export let options = {
     stages: [
       { duration: '5m', target: 200 },
       { duration: '60m', target: 200 },  // Sustained load
     ],
   };
   ```

2. **Performance Targets**
   - URL creation: <100ms p95, 500 req/s
   - Redirects (cached): <10ms p95, 5000 req/s
   - Redirects (uncached): <50ms p95, 1000 req/s

3. **Realistic Traffic Patterns**
   - 80% reads (redirects), 20% writes (create)
   - Zipf distribution for URL popularity
   - Realistic user agents and referers

### Deliverables
- k6 test suite (baseline, spike, endurance)
- Performance test results documented
- CI integration (run on main branch)

---

## Phase 10: Chaos Engineering Deep Dive 🎯 EXTENDED

### Objective
Comprehensive chaos testing with automated scenarios and visual dashboards.

**Prerequisites:** Toxiproxy infrastructure and basic chaos scenarios from Phase 8.

### Tasks
1. **Automated Chaos Scenarios**
   - All 5 scenarios from Phase 8 automated
   - Chaos testing pipeline in CI (optional, nightly)
   - Experiment tracking (what, when, result)

2. **Chaos Testing Dashboard** 🆕
   - Real-time metrics during chaos experiments
   - Experiment history and results
   - UI to trigger Toxiproxy failures
   - Recovery time tracking

3. **Chaos Scenarios with Analytics** 🎯
   - Run analytics load test DURING chaos
   - Verify queue absorbs traffic when worker dies
   - Measure recovery time when services restored

   **Example: Analytics Resilience Test**
   ```bash
   # Start analytics load
   k6 run --vus 1000 --duration 120s clicks.js &
   
   # t=30s: Kill all analytics workers
   docker-compose stop analytics-worker
   
   # Observe:
   # - Redirects still fast (<10ms)
   # - Queue depth grows
   # - No analytics processed
   
   # t=60s: Restart workers
   docker-compose up -d analytics-worker
   
   # Observe:
   # - Queue drains
   # - All events eventually processed
   # - Zero data loss
   
   # Result: System resilient to worker failures
   ```

4. **Documentation**
   - Chaos testing runbook
   - Expected behavior for each scenario
   - Recovery procedures

### Deliverables
- 10+ chaos scenarios automated
- Chaos dashboard (optional: simple HTML/React)
- Video/GIF demos of key scenarios
- Complete chaos testing documentation

---

## Phase 11: Dashboards & Alerting

### Objective
Complete the observability stack with Grafana dashboards and alerting rules. Builds on the OTel/Prometheus/Jaeger infrastructure from Phase 5.

### Tasks
1. **Grafana Dashboards**
   - System overview (RED metrics: Rate, Errors, Duration)
   - Per-service dashboards (Gateway, Rate Limiter, Workers)
   - Chaos testing dashboard (circuit breaker states, recovery times)
   - Analytics pipeline dashboard (queue depth, processing rate)
   - Trace integration (link from dashboard panels to Jaeger traces)

2. **Alert Rules**
   - High error rate (>1%)
   - High latency (p95 >200ms)
   - Circuit breaker open
   - Queue depth growing (>10k messages)
   - DLQ not empty

3. **Enhanced Logging & Aggregation** (builds on Phase 5 `slog` implementation)
   - Detailed request/response body logging (optional, configurable)
   - Log aggregation setup (Loki, ELK, or CloudWatch)
   - Log-based alerting rules
   - Log retention policies

### Deliverables
- Production-ready Grafana dashboards
- Alert rules configured (metrics + logs)
- Log aggregation infrastructure (Loki/ELK/CloudWatch)
- Enhanced logging with request/response details

---

## Phase 12: Documentation & Polish

### Objective
Create portfolio-quality documentation and demo materials.

### Tasks
1. **Architecture Documentation**
   - System architecture diagram
   - Data flow diagrams
   - Deployment architecture
   - Decision logs (ADRs)

2. **README Excellence**
   - Clear project overview
   - Quick start guide
   - Demo instructions
   - Architecture section
   - Performance benchmarks
   - Chaos testing showcase

3. **Demo Materials**
   - 5-minute demo script
   - Screenshots/GIFs of chaos scenarios
   - Performance comparison charts
   - Video walkthrough (optional)

4. **Code Quality**
   - Code cleanup and refactoring
   - Comprehensive comments
   - API documentation (OpenAPI spec)

### Deliverables
- Professional README with visuals
- Architecture diagrams
- Demo script and materials
- Clean, well-documented codebase

---

## Timeline Summary (13 Weeks)

```
Week 1-2:  ✅ Phase 1  - Core Foundation
Week 2-3:  ✅ Phase 2  - CI/Test Automation
Week 3-4:  ✅ Phase 3  - Build & Deployment (CD)
Week 4-5:  ✅ Phase 4  - Caching Layer
Week 5-6:  ✅ Phase 5  - Observability Foundation (OTel + Prometheus + Jaeger)
Week 6-7:  ✅ Phase 6  - Rust Rate Limiter + gRPC
Week 7-8:  ✅ Phase 7  - RabbitMQ Click Analytics
Week 8-9:  ✅ Phase 8  - Resilience Patterns + Toxiproxy
Week 9:    Phase 9  - Load Testing
Week 10-11: Phase 10 - Chaos Engineering (Extended)
Week 12:   Phase 11 - Dashboards & Alerting
Week 13:   Phase 12 - Documentation & Polish
```

---

## Key Changes from Original Plan

1. **Moved CD (Phase 3) before complex features** - Deploy infrastructure early
2. **Observability before new services (Phase 5)** - OTel + Prometheus + Jaeger set up before rate limiter and analytics, so every service is traced from day one
3. **Toxiproxy deferred to Phase 8** - Integrated with resilience patterns when there are multiple services to test
4. **Focused RabbitMQ on click analytics** - Clear, demonstrable use case
5. **Made link expiration optional** - Nice to have, not critical path
6. **Extended chaos testing (Phase 10)** - More time for comprehensive scenarios
7. **Concrete chaos scenarios with verification** - Not just theory, actual runnable tests

---

## Success Criteria

**Production-Grade Indicators:**
- ✅ Deployed system with CI/CD pipeline
- ✅ Sub-10ms redirect latency (p95)
- ✅ Handles 5000+ req/s for redirects
- ✅ 5+ chaos scenarios documented and automated
- ✅ Circuit breakers prevent cascading failures
- ✅ Zero data loss in analytics pipeline
- ✅ Grafana dashboards showing system health
- ✅ Professional documentation with architecture diagrams

**Portfolio Impact:**
- Shows distributed systems expertise
- Demonstrates chaos engineering practices
- Proves performance optimization skills
- Exhibits production-ready code quality
- Provides clear before/after metrics (RabbitMQ benefit)

---

## Quick Reference: Make Targets

```makefile
# Development
make dev                    # Start all services locally
make test                   # Run all tests
make lint                   # Run linters

# Deployment
make build                  # Build container images
make deploy-staging         # Deploy to staging
make deploy-prod            # Deploy to production

# Chaos Testing
make chaos-postgres         # PostgreSQL failure scenario
make chaos-redis            # Redis degradation scenario
make chaos-rate-limiter     # Rate limiter failure scenario
make chaos-rabbitmq         # RabbitMQ failure scenario
make chaos-all              # Run all chaos scenarios

# Demos
make demo-analytics         # Analytics performance demo
make demo-expiration        # Link expiration demo (optional)

# Load Testing
make load-baseline          # Baseline load test
make load-spike             # Spike test
make load-endurance         # Endurance test
```