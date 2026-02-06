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

## Phase 5: Observability Foundation (OTel + Prometheus + Jaeger)

### Objective
Add distributed tracing and metrics instrumentation using OpenTelemetry as the unified instrumentation layer, with Prometheus for metrics storage and Jaeger for trace visualization. Setting up observability before adding more services ensures every future service is traced from day one.

### Tasks
1. **Trace Context Propagation & Structured Logging**
   - W3C TraceContext propagation middleware
   - **Structured logging with `slog` (Go standard library)**
     - JSON log format
     - Trace ID and Span ID injected into log fields (correlate logs ↔ traces)
     - HTTP middleware logging (method, path, status, duration)
     - Error logging with trace context
   - Prepares for cross-service propagation (gRPC, AMQP) in later phases

2. **OpenTelemetry SDK Integration**
   - `go.opentelemetry.io/otel` SDK setup in the gateway
   - Trace spans for HTTP handlers (middleware-based)
   - Trace spans for Redis cache operations (hit/miss/error)
   - Trace spans for PostgreSQL queries
   - Span attributes: `http.method`, `http.status_code`, `db.system`, `cache.hit`, etc.

3. **Prometheus Metrics via OTel**
   - OTel metrics SDK with Prometheus exporter
   - `/metrics` endpoint exposed on the gateway
   - Key metrics:
     - `http_request_duration_seconds` (histogram)
     - `cache_hits_total` / `cache_misses_total` (counters)
     - `db_query_duration_seconds` (histogram)
     - `errors_total` by type (counter)
     - `circuit_breaker_state` (gauge)

4. **Infrastructure Setup**
   - OTel Collector in Docker Compose (receives OTLP, exports to Prometheus + Jaeger)
   - Jaeger all-in-one for trace visualization
   - Prometheus for metrics scraping (scrapes OTel Collector)
   - Add to `docker-compose.prod.yml`

### Deliverables
- OTel SDK integrated in gateway (traces + metrics)
- Prometheus `/metrics` endpoint with key application metrics
- Jaeger UI accessible for trace inspection
- OTel Collector configured as telemetry pipeline
- Trace spans covering: HTTP request → cache lookup → DB query → response
- W3C TraceContext propagation ready for future services
- **Structured JSON logs with trace correlation using `slog`**

### Architecture
```
Gateway (OTel SDK)
  │
  ├─ traces (OTLP) ──→ OTel Collector ──→ Jaeger
  │
  └─ metrics (OTLP) ─→ OTel Collector ──→ Prometheus
```

---

## Phase 6: Rust Rate Limiter with gRPC

### Objective
Build a low-latency rate limiting service in Rust with gateway-first architecture.

### Tasks
1. **Architecture Pattern** 🎯
   - Gateway-first: Client → Gateway → Rate Limiter (gRPC)
   - Rate limiter returns (allowed: bool, retry_after: u32)
   - Gateway enforces decision
   - Fail-open strategy when rate limiter unavailable

2. **Rate Limiting Algorithms**
   - Token bucket implementation
   - Redis-backed token storage
   - Per-IP and per-API-key limits
   - Atomic operations for distributed consistency

3. **gRPC Service Interface**
   - `CheckRateLimit(ip, api_key) → RateLimitResponse`
   - Target latency: <5ms p99
   - Bulk check support for batch requests

4. **Gateway Integration**
   - gRPC client in gateway middleware
   - Circuit breaker for rate limiter calls
   - Timeout: 100ms (fail open after timeout)
   - Fallback to local in-memory rate limiting

5. **Toxiproxy Integration** 🎯
   - Route Gateway → Rate Limiter through proxy
   - Chaos scenarios:
     - 100% connection failures
     - 500ms latency injection
     - 50% packet loss
   - Verify circuit breaker and fallback behavior

### Deliverables
- Working Rust rate limiter service
- gRPC interface implemented
- Gateway integration with circuit breaker
- Sub-5ms p99 response time
- Toxiproxy chaos scenarios documented
- Fallback behavior tested (fail open vs fail closed)

### Chaos Scenarios to Document
```bash
# Scenario 1: Rate limiter complete failure
toxiproxy-cli toxic add rate_limiter -t timeout

# Scenario 2: High latency
toxiproxy-cli toxic add rate_limiter -t latency -a latency=500

# Scenario 3: Intermittent failures
toxiproxy-cli toxic add rate_limiter -t limit_data -a bytes=1000
```

---

## Phase 7: Async Click Analytics with RabbitMQ

### Objective
Implement high-throughput click analytics pipeline demonstrating async processing benefits.

### Tasks
1. **RabbitMQ Setup**
   - Topic exchange: `analytics`
   - Queues: `analytics.clicks`, `analytics.clicks.dlq`
   - Dead letter queue configuration
   - Message persistence enabled

2. **Click Event Publishing**
   - Fire-and-forget from redirect handler
   - Event schema:
     ```json
     {
       "short_code": "abc123",
       "clicked_at": "2026-01-22T10:30:00Z",
       "ip": "1.2.3.4",
       "user_agent": "...",
       "referer": "https://twitter.com"
     }
     ```
   - Publisher confirms for reliability
   - Circuit breaker on publish failures (fallback: log locally)

3. **Analytics Worker**
   - Consume from `analytics.clicks` queue
   - Batch processing: 1000 events → 1 DB INSERT
   - Configurable batch size and flush interval
   - Retry logic with exponential backoff
   - DLQ processing for permanent failures

4. **Performance Comparison** 🎯
   - Synchronous mode: Direct DB insert on redirect
   - Asynchronous mode: RabbitMQ + worker
   - Benchmarking with k6:
     - Measure redirect latency (target: <10ms async vs >100ms sync)
     - Measure throughput (target: 5x improvement)

5. **Monitoring**
   - Queue depth metrics
   - Processing rate (events/sec)
   - Batch efficiency (events per INSERT)
   - DLQ depth (alerts on growth)

### Deliverables
- RabbitMQ analytics pipeline working
- k6 load test scripts comparing sync vs async
- Grafana dashboard showing performance improvement
- DLQ handling with retry logic
- Demo script: `make demo-analytics`

### Demo Flow (30 seconds)
```bash
# 1. Baseline (sync): 100 VUs, show latency
k6 run --vus 100 tests/clicks-sync.js

# 2. Switch to async mode
curl -X POST localhost:8080/admin/analytics/async

# 3. High load (async): 1000 VUs, show improvement
k6 run --vus 1000 tests/clicks-async.js

# Result: 5x throughput, 15x latency reduction
```

### Optional: Link Expiration (Nice to Have)
- Delayed message queue for TTL-based cleanup
- `lifecycle.expirations` queue
- Worker processes expired URLs
- Demo with 30s TTL for fast demonstration

---

## Phase 8: Resilience Patterns & Chaos Testing

### Objective
Add circuit breakers, retries, comprehensive failure handling, and Toxiproxy chaos testing infrastructure.

### Tasks
1. **Toxiproxy Integration** 🎯
   - `docker-compose.chaos.yml` with Toxiproxy service
   - Proxy configuration for all external dependencies (Postgres, Redis, RabbitMQ, Rate Limiter)
   - Toxiproxy CLI setup for failure injection
   - Route all service calls through Toxiproxy proxies

2. **Circuit Breakers** 🎯
   - Wrap all external service calls:
     - PostgreSQL queries
     - Redis operations
     - Rate limiter gRPC calls
     - RabbitMQ publish
   - Configurable thresholds (errors, timeout, half-open)
   - Metrics for circuit breaker state

3. **Retry Logic**
   - Exponential backoff with jitter
   - Configurable retry counts per service
   - Idempotency considerations (use request IDs)

4. **Backpressure Handling**
   - Request queue limits (reject with 429 if full)
   - Load shedding strategies (drop low-priority requests)
   - Graceful degradation levels

5. **Advanced Cache Strategies**
   - Bloom filter for existence checks (prevents DB queries for non-existent URLs)
   - Cache warming on startup (top 1000 URLs by click_count)

6. **Concrete Chaos Scenarios** 🎯

   **Scenario 1: PostgreSQL Failure**
   ```bash
   # Inject: 100% connection timeout
   toxiproxy-cli toxic add postgres -t timeout -a timeout=0
   
   # Expected:
   # - Circuit breaker opens after 5 failures
   # - Health check returns unhealthy
   # - Service returns 503 Service Unavailable
   # - Auto-recovery when DB restored
   
   # Verify:
   curl localhost:8080/health/ready  # Returns 503
   toxiproxy-cli toxic remove postgres timeout
   sleep 10
   curl localhost:8080/health/ready  # Returns 200
   ```

   **Scenario 2: Redis Partial Failure**
   ```bash
   # Inject: 50% packet loss
   toxiproxy-cli toxic add redis -t limit_data -a bytes=100
   
   # Expected:
   # - Retries succeed eventually (with backoff)
   # - Latency increases but requests succeed
   # - Cache hit ratio drops, more DB queries
   
   # Verify metrics:
   curl localhost:9090/metrics | grep cache_misses_total
   ```

   **Scenario 3: Rate Limiter Slow**
   ```bash
   # Inject: +500ms latency
   toxiproxy-cli toxic add rate_limiter -t latency -a latency=500
   
   # Expected:
   # - Gateway timeout after 100ms
   # - Circuit breaker opens
   # - Fallback to local rate limiting
   # - URLs still created (degraded mode)
   
   # Verify:
   curl -w "@curl-format.txt" localhost:8080/api/v1/shorten
   # Response time: <150ms (didn't wait for 500ms)
   ```

   **Scenario 4: RabbitMQ Complete Failure**
   ```bash
   # Inject: Stop RabbitMQ entirely
   docker-compose stop rabbitmq
   
   # Expected:
   # - Redirects still work (fire-and-forget)
   # - Analytics events lost (acceptable)
   # - Circuit breaker prevents retry spam
   # - Logs indicate analytics unavailable
   
   # Verify:
   k6 run tests/redirects.js  # Still succeeds
   curl localhost:9090/metrics | grep analytics_publish_errors
   ```

   **Scenario 5: Cascading Failure**
   ```bash
   # Inject: Kill Postgres → Redis overload → Rate limiter can't update
   docker-compose stop postgres
   
   # Expected:
   # - Gateway circuit breaker opens
   # - Redis queries increase (more cache misses)
   # - Rate limiter can't persist limits (uses local state)
   # - Graceful degradation at each layer
   # - System recovers when Postgres restored
   ```

### Deliverables
- `docker-compose.chaos.yml` with Toxiproxy setup
- Circuit breakers on all external dependencies
- Retry logic with configurable backoff
- 5 documented chaos scenarios with verification steps
- Health check aggregating all dependencies
- Grafana dashboard showing circuit breaker states
- Chaos testing scripts (`scripts/chaos-test.sh`)

### Chaos Testing Automation
```bash
# scripts/chaos-test.sh
#!/bin/bash
echo "Running chaos scenario: $1"
case $1 in
  postgres-failure)
    ./scenarios/postgres-failure.sh
    ;;
  redis-degradation)
    ./scenarios/redis-degradation.sh
    ;;
  # ... etc
esac
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
Week 5-6:  Phase 5  - Observability Foundation (OTel + Prometheus + Jaeger)
Week 6-7:  Phase 6  - Rust Rate Limiter + gRPC
Week 7-8:  Phase 7  - RabbitMQ Click Analytics
Week 8-9:  Phase 8  - Resilience Patterns + Toxiproxy
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