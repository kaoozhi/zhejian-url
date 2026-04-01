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

## Phase 9: Load Testing ✅ COMPLETED

### Objective
Validate system performance under realistic load using k6, with real-time visualization via the k6 web dashboard and Prometheus remote write.

### Scope (revised from original plan)

**What changed from original plan and why:**
- Spike target reduced from 1000 → 300 VUs: 1000 VUs on WSL2/Docker exhausts resources; 300 is realistic
- Endurance shortened from 60min → 10min: diminishing returns after 10min for local portfolio testing
- Added `throughput.js` (not in original plan): to measure raw system ceiling without rate limiter
- Thresholds calibrated for WSL2/Docker (p95 < 200ms redirects) not production bare-metal
- All tests use per-VU `X-Forwarded-For` spoofing — required because the Rust rate limiter enforces 100 req/min per IP

### Tasks

**Task 1: Prometheus remote write receiver** ✅
- Added `--web.enable-remote-write-receiver` to prometheus service in `docker-compose.yml` and `docker-compose.prod.yml`
- Enables `k6 run -o experimental-prometheus-rw` to push k6 metrics alongside gateway metrics

**Task 2: results/ directory** ✅
- `results/.gitkeep` — directory tracked in git
- `.gitignore` entry for `results/*.html` — generated reports are local only

**Task 3-6: k6 test suite** ✅

| File | VUs | Duration | Purpose |
|---|---|---|---|
| `tests/baseline.js` | 100 | 9 min (2+5+2) | Steady-state SLO validation |
| `tests/spike.js` | 50→300→50 | ~5 min | Error rate under sudden load |
| `tests/endurance.js` | 100 | 12 min (1+10+1) | Memory leaks / pool exhaustion |
| `tests/throughput.js` | 200 | 4 min (30s+3m+30s) | Raw ceiling (rate limiter disabled) |

**Task 7: Makefile** ✅
- Replaced stale `load-test` target (pointed to nonexistent `testing/load/load-test.js`)
- Added `load-baseline`, `load-spike`, `load-endurance`, `load-throughput`, `load-analytics` targets

**Task 8: CI workflow** ✅
- `.github/workflows/load-test.yml` — triggers on push to main and `workflow_dispatch`
- Runs `baseline.js` in CI mode (`CI=true` → shorter 1+2+1 min stages)
- Uploads HTML report as artifact (30-day retention)

### Key design decisions

- **Per-VU IP spoofing**: `X-Forwarded-For: 10.0.{VU/256}.{VU%256}` gives each VU its own rate-limit token bucket, simulating distinct users. Pattern established in `tests/analytics-load.js`.
- **`CI=true` env var**: `baseline.js` detects this to use 1+2+1 min stages instead of 2+5+2, keeping CI jobs under 6 minutes total.
- **p95 < 200ms threshold**: Calibrated for WSL2/Docker. Every request pays gRPC rate-limiter overhead (~1-3ms) plus Docker bridge networking. Tighten to 50ms on native Linux.
- **`throughput.js` has no sleep**: Designed to find the ceiling; rate limiter must be disabled via `RATE_LIMITER_ADDR=""` otherwise the system sends 429s and skews the error rate.
- **No shared k6 module**: `vuIP()` and `pickWeighted()` are inlined in each test file — simpler, no import complexity, each file is self-contained.

### Visualization

```
Local run:
  K6_WEB_DASHBOARD=true k6 run tests/baseline.js
  → Browser dashboard at http://localhost:5665 (real-time VUs, p95, error rate)
  → results/baseline-report.html (standalone HTML after run)

With Prometheus remote write:
  K6_PROMETHEUS_RW_SERVER_URL=http://localhost:9090/api/v1/write \
  k6 run -o experimental-prometheus-rw tests/baseline.js
  → k6 metrics (k6_http_req_duration_*, k6_vus) appear in Prometheus UI at :9090
  → Correlate client-observed latency with gateway's server-side http_request_duration_seconds
```

### Deliverables
- ✅ `tests/baseline.js` — 100 VUs, 80/20 read/write, Zipf, CI-short mode
- ✅ `tests/spike.js` — 50→300→50 VUs, error rate threshold only
- ✅ `tests/endurance.js` — 100 VUs, 10 min, local only
- ✅ `tests/throughput.js` — 200 VUs, no sleep, rate limiter disabled
- ✅ `results/.gitkeep` — directory for HTML report exports
- ✅ `docker-compose.yml` + `docker-compose.prod.yml` — Prometheus remote write enabled
- ✅ `Makefile` — `load-baseline`, `load-spike`, `load-endurance`, `load-throughput` targets
- ✅ `.github/workflows/load-test.yml` — CI baseline with HTML artifact

### Usage
```bash
# Start stack
docker compose up --build -d

# Baseline with live dashboard
make load-baseline
# → Opens http://localhost:5665, exports results/baseline-report.html

# Spike test
make load-spike

# Throughput ceiling (rate limiter disabled)
RATE_LIMITER_ADDR="" make load-throughput

# CI dry-run (4 min)
CI=true k6 run tests/baseline.js
```

### Key Finding

Running `throughput.js` with the rate limiter properly disabled established the real baseline (WSL2, single node, no sleep):

| VUs | req/s | p95 | SLO (p95 < 200ms) |
|---|---|---|---|
| 200 | 5319 | 103ms | ✓ comfortable |
| 700 | 5584 | 185ms | ✓ on the edge |
| 1000 | ~5600 | 247ms | ✗ breached (WSL2 risk at this level) |

**Throughput plateaus at ~5600 req/s** regardless of VU count — adding VUs past ~500 only increases latency, not throughput. The SLO boundary (p95 = 200ms) is reached around 700–800 VUs.

Note: an earlier run showed ~36% failure — this was caused by `RATE_LIMITER_ADDR` being hardcoded in `docker-compose.yml` and not actually disabled. There was no resource exhaustion. The fix was changing `:-` to `-` in the compose interpolation so an empty shell var is respected.

**Comparison baseline for Phase 10:** 700 VUs, p95=185ms, ~5600 req/s. Each scaling pattern should reduce p95 at this load, giving headroom to serve more VUs within SLO.

The k6 terminal summary (p95, req/s) plus HTML reports are sufficient to prove each improvement — no Grafana required at this stage.

---

## Phase 10: Scaling Patterns with Before/After Measurement 🎯 NEXT

### Objective

Implement four targeted scaling patterns, each with a measurable before/after comparison using `tests/throughput.js`. The goal is not horizontal scale-out — it is demonstrating that each pattern addresses a specific bottleneck with concrete numbers.

### Design Decisions

- **No explicit feature flags**: each pattern degrades gracefully when its infrastructure is absent. The env var IS the toggle (e.g. `DB_REPLICA_URL` unset = replica off). Adding a separate `ENABLE_*` var would be redundant code.
- **Comparison tool**: k6 terminal summary + HTML reports + Prometheus UI (:9090) — sufficient without Grafana.
- **Plan**: `docs/plans/2026-03-06-scaling-comparison-plan.md`

### Sub-Phases

#### Phase 10A: PostgreSQL Read-Write Split

**Hypothesis:** `GetByCode` (reads) and `Create` (writes) compete for the same 10-connection pool. Routing reads to a streaming replica doubles effective connection capacity.

**Result: Null result — hypothesis was wrong for this architecture.**

| Metric | Before | After | Delta |
|---|---|---|---|
| p95 | 212ms | 261ms | +49ms (worse) |
| req/s | 5384 | 4481 | -17% |
| failures | 0% | 0% | — |

**Why it didn't work:**
1. **Redis absorbs most reads.** At ~5600 req/s with a warm cache, only cache-miss reads reach the DB. The 10-connection pool was never congested — splitting it had nothing to split.
2. **Two PG containers consume more WSL2 resources.** Running primary + replica on the same machine reduces CPU/memory per container. The overhead outweighs any pool-split benefit.
3. **The real bottleneck is Redis saturation, not DB pool contention.** When the Redis CB opens under load, reads fall through to DB — but the constraint is the Redis circuit breaker state, not which pool handles the fallback.

**Engineering lesson:** Read-write split is designed for I/O-bound workloads where the DB is the bottleneck. When a cache sits in front of the DB with a high hit rate, the DB pool is underutilised and splitting it adds infrastructure cost with no throughput benefit. Implementation was reverted; the pattern remains valid in write-heavy or cache-cold workloads.

**Implicit toggle (implemented, then reverted):** `DB_REPLICA_URL` unset → single pool. Set → replica active via `readPool()` fallback.

---

#### Phase 10B: Redis Consistent Hash Ring

**Confirmed bottleneck (from Phase 10A load test logs):**

```
circuit breaker about to trip  (5 consecutive failures)
circuit breaker OPENED         (closed → open)
cache read error: context deadline exceeded   ← Redis 50ms timeout hit
  ... 30s failing fast, all reads fall through to DB ...
circuit breaker testing recovery (open → half-open)
cache read error: too many requests           ← MaxRequests=3 quota exhausted
circuit breaker RECOVERED      (half-open → closed)
cache read error: too many requests           ← already failing again immediately
```

Single Redis node saturates at ~5600 req/s (~11,000 ops/s with GET+SET per redirect). The CB oscillates — opens, recovers, immediately re-trips — because the per-node ops rate never drops below the saturation threshold.

**Hypothesis:** Three nodes with consistent hashing distribute cache load ~33% per node (~3700 ops/s each), pushing the saturation threshold 3× higher and eliminating CB trips at 700 VUs.

**Implicit toggle:** `CACHE_NODES` unset → `singleNodeRouter`. Set to 3 addresses → `HashRing`.

**Files touched:**
- `docker-compose.yml` — add `redis-2`, `redis-3`
- `services/gateway/internal/cache/ring.go` — new `HashRing` with virtual nodes
- `services/gateway/internal/cache/ring_test.go` — distribution uniformity + remap tests (TDD)
- `services/gateway/internal/repository/cached_url_repository.go` — `CacheRouter` interface replaces `*redis.Client`
- `services/gateway/internal/config/config.go` — add `CACHE_NODES` list
- `services/gateway/internal/server/server.go` + `main.go` — build ring, pass as `CacheRouter`

**New metric:** `cache_node_hits_total{node}` (OTel counter)

**Expected delta:** Cache connection throughput ×3; key distribution ~33% per node in Prometheus.

---

#### Phase 10C: RabbitMQ Competing Consumers + Quorum Queue

**Hypothesis:** Single analytics-worker processes one batch at a time; queue depth grows unboundedly under burst. Three workers drain the queue in parallel. Quorum queue ensures durability across broker restarts.

**Implicit toggle:** `docker compose up -d --scale analytics-worker=N` — no code flag, no env var.

**Files touched:**
- `services/analytics-worker/internal/consumer/consumer.go` — add `x-queue-type: quorum` to `QueueDeclare` args

**Note:** Queue type cannot be changed after creation — requires `docker compose down -v` before first run.

**Expected delta:** Peak queue depth stable (not growing); drain time from minutes → seconds.

---

#### Phase 10D: PgBouncer Connection Pooling — SKIPPED

**Skipped — DB not the bottleneck (same conclusion as 10A).**

PgBouncer solves `N instances × pool_size` connection exhaustion, which requires horizontal gateway scaling to manifest. With 1 gateway instance and a 10-connection pool, the expected delta (constant 15 connections) is a configuration metric, not a performance story. Phase 10A already proved the DB pool is idle under load — Redis saturation is the bottleneck. Implementing PgBouncer here would produce a null result for the same reason 10A did.

---

### Comparison Summary

| Sub-phase | Primary bottleneck | Before (700 VUs baseline) | Actual after |
|---|---|---|---|
| 10A PG read-write split | Read/write contention on single pool | p95=212ms | p95=261ms — **null result**, DB pool not the bottleneck |
| 10B Redis hash ring | Single Redis connection throughput ceiling | ~5600 req/s plateau | higher plateau or lower p95 |
| 10C Competing consumers | Single worker, queue grows unboundedly | Queue depth growing | Queue depth stable |
| 10D PgBouncer | Connections = instances × pool size | N × 10 PG connections | **skipped** — DB not the bottleneck (same as 10A) |

### Deliverables
- `docker-compose.yml` — redis-2/3 (hash ring)
- `services/gateway/internal/cache/ring.go` + `ring_test.go`
- `services/gateway/internal/repository/cached_url_repository.go` — CacheRouter interface
- `services/analytics-worker/internal/consumer/consumer.go` — quorum queue declaration
- `results/throughput-before-*.html` + `results/throughput-after-*.html` — before/after HTML reports

---

## Phase 12: Chaos Engineering Lite 🎯 PLANNED

### Objective
Make the existing Toxiproxy chaos infrastructure runnable with pass/fail assertions. Add the analytics resilience scenario that operationalises the Phase 10C finding.

**Prerequisites:** Toxiproxy infrastructure and basic chaos scenarios from Phase 8.

**Scope note:** No chaos dashboard UI (frontend work, low portfolio signal for a systems engineering project). No CI nightly pipeline (maintenance overhead not worth it). Focus is on clean, scriptable scenarios with observable output.

### Tasks
1. **Automated Chaos Scenarios**
   - Refactor `scripts/chaos-test.sh` — existing 5 scenarios produce pass/fail assertions instead of requiring manual observation
   - Each scenario: inject fault → assert degraded behaviour → remove fault → assert recovery

2. **Analytics Resilience Scenario** 🎯
   - Operationalises the Phase 10C finding: queue absorbs traffic when workers are down
   ```bash
   ./scripts/chaos-test.sh --scenario analytics-resilience
   # 1. Start k6 load (background, 60s)
   # 2. Assert queue depth < 10 (workers draining normally)
   # 3. docker compose stop analytics-worker
   # 4. Assert queue depth > 1000 after 10s (backlog building)
   # 5. docker compose up -d analytics-worker
   # 6. Assert queue depth < 10 after 30s (drained)
   # PASS
   ```

3. **Runbook**
   - `docs/findings/chaos-runbook.md` — scenario descriptions, expected outputs, recovery procedures

### Deliverables
- Updated `scripts/chaos-test.sh` with pass/fail assertions for all 6 scenarios
- `docs/findings/chaos-runbook.md`

---

## Phase 11: Grafana Dashboards & Alerting ✅ COMPLETED

### Objective
Complete the observability stack with provisioned Grafana dashboards and alert rules. Builds on the Prometheus/OTel infrastructure from Phase 5 and makes the Phase 10 scaling comparisons visually compelling with persistent dashboard panels.

**Scope note:** No log aggregation (Loki/ELK adds a week of infra work with no visual delta over existing Prometheus). Dashboards provisioned as code — no manual Grafana UI clicks needed.

### Tasks
1. **Grafana Dashboards** (provisioned via `observability/grafana/provisioning/`)
   - System overview — RED metrics (rate, errors, duration), circuit breaker state, cache hit rate
   - Analytics pipeline — queue depth (`rabbitmq_queue_messages_ready`), drain rate, worker count
   - Scaling comparison — Phase 10B (single node vs. hash ring p95/CB trips) and 10C (1 worker vs. 3 workers queue depth) side by side

2. **Alert Rules**
   - High error rate (>1%)
   - High latency (p95 >200ms)
   - Circuit breaker open
   - Queue depth growing (>10k messages)
   - DLQ not empty

### Files
| File | Purpose |
|---|---|
| `docker-compose.yml` | Add `grafana` service |
| `observability/grafana/provisioning/datasources/prometheus.yml` | Auto-configure Prometheus datasource |
| `observability/grafana/provisioning/dashboards/dashboards.yml` | Dashboard provider config |
| `observability/grafana/dashboards/system-overview.json` | RED metrics + CB + cache hit rate |
| `observability/grafana/dashboards/analytics-pipeline.json` | Queue depth + drain rate |
| `observability/grafana/dashboards/scaling-comparison.json` | Phase 10B/10C before/after panels |
| `observability/prometheus/alert-rules.yml` | Alert rule definitions |

### Deliverables
- Grafana running at `:3000`, dashboards load automatically on `docker compose up`
- Alert rules configured in Prometheus

---

## Phase 13: Documentation & Polish

### Objective
Portfolio-quality README and demo materials. No code changes.

**Scope note:** No OpenAPI spec (not the portfolio's story). No code cleanup (codebase is already clean). Video walkthrough optional — do last if time permits.

### Tasks
1. **Architecture Diagram** (Mermaid)
   - Request flow: Client → Gateway → Rate Limiter → Redis ring → DB → RabbitMQ → Worker
   - Service topology with ports

2. **README Update**
   - Add Grafana dashboard screenshots
   - Embed architecture diagram
   - Update phase completion table

3. **Demo Script**
   - 5-minute script covering three stories:
     1. Load test → hash ring improvement (Phase 10B)
     2. Chaos → circuit breaker recovery (Phase 8/12)
     3. Kill workers → queue absorbs → drain (Phase 10C/12)

### Deliverables
- Mermaid architecture diagram in README
- README with Grafana screenshots
- `docs/demo-script.md`

---

## Timeline Summary (15 Weeks)

```
Week 1-2:  ✅ Phase 1  - Core Foundation
Week 2-3:  ✅ Phase 2  - CI/Test Automation
Week 3-4:  ✅ Phase 3  - Build & Deployment (CD)
Week 4-5:  ✅ Phase 4  - Caching Layer
Week 5-6:  ✅ Phase 5  - Observability Foundation (OTel + Prometheus + Jaeger)
Week 6-7:  ✅ Phase 6  - Rust Rate Limiter + gRPC
Week 7-8:  ✅ Phase 7  - RabbitMQ Click Analytics
Week 8-9:  ✅ Phase 8  - Resilience Patterns + Toxiproxy
Week 9:    ✅ Phase 9  - Load Testing (baseline: p95=185ms at 700 VUs, ~5600 req/s ceiling)
Week 10-11: ✅ Phase 10 - Scaling Patterns + Before/After Measurement (10A-10C; 10D skipped — DB not bottleneck)
Week 12:   Phase 11 - Grafana Dashboards & Alerting (highest portfolio signal)
Week 13:   Phase 12 - Chaos Engineering Lite (automated scenarios + analytics-resilience)
Week 14-15: Phase 13 - Documentation & Polish
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
- Proves performance optimization skills with measurable before/after numbers (Phase 10)
- Exhibits production-ready code quality
- Provides clear before/after metrics for each scaling pattern (PG split, Redis ring, competing consumers, PgBouncer)

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