[![Go](https://github.com/kaoozhi/zhejian-url/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/kaoozhi/zhejian-url/actions/workflows/go.yml)
[![Rust](https://github.com/kaoozhi/zhejian-url/actions/workflows/rust.yml/badge.svg?branch=main)](https://github.com/kaoozhi/zhejian-url/actions/workflows/rust.yml)
[![Docker Compose](https://github.com/kaoozhi/zhejian-url/actions/workflows/production.yml/badge.svg)](https://github.com/kaoozhi/zhejian-url/actions/workflows/production.yml)
[![Chaos](https://github.com/kaoozhi/zhejian-url/actions/workflows/chaos.yml/badge.svg?branch=main)](https://github.com/kaoozhi/zhejian-url/actions/workflows/chaos.yml)
[![codecov](https://codecov.io/gh/kaoozhi/zhejian-url/branch/main/graph/badge.svg?token=XPQ70FTF7M)](https://codecov.io/gh/kaoozhi/zhejian-url)

# Zhejian URL Shortener

A URL shortener with a CQRS service split, distributed caching, async analytics pipeline, and automated chaos testing — built in Go and Rust with full observability. The architecture is designed for horizontal scaling: stateless read/write services behind nginx, a Redis consistent hash ring, competing consumers on RabbitMQ quorum queues, and circuit breakers on every external dependency. Every resilience claim is verified under fault injection.

**What this project demonstrates:**
- **Distributed systems design** — CQRS read/write split with nginx method-based routing, consistent hash ring (SHA-256, 150 vnodes), singleflight + circuit breaker + negative caching to prevent DB stampedes
- **Resilience engineering** — 6 automated chaos scenarios (Toxiproxy) verify fail-open rate limiting, cache fallback to DB, AMQP auto-reconnect, and quorum queue durability under node failure
- **Data-driven decisions** — load testing showed the DB pool was never the bottleneck (Redis absorbs ~99% of reads), so the DB pool split was reverted. The read/write *service* split addresses the real scaling axis: reads dominate traffic and can scale independently with their own cache, rate limiter, and replica DB pool — while writes stay on a single primary with no rate limiter overhead
- **Polyglot services** — Go (Gin, pgx, AMQP, OTel) for the API layer; Rust (tonic, tokio) for a gRPC rate limiter with atomic Lua token bucket; full CI pipeline with chaos tests in GitHub Actions

---

## Architecture

```
Client
  │
  ▼
nginx (:80)  — routes by HTTP method
  ├── GET /*           ──► read-service (:8080)
  └── POST/DELETE/*    ──► write-service (:8081)


read-service (Go/Gin)
  ├── OTel middleware → Jaeger (:16686)
  ├── Metrics middleware → Prometheus (:9090)
  ├── Rate-limit middleware
  │     └── gRPC (100ms timeout, fail-open) → Rust rate-limiter (:50051)
  │                                                └── Redis token bucket
  │
  ├── GET /:code  ──► CachedURLRepository ──► 301 redirect
  │                       ├── Redis hash ring (3 nodes, SHA-256, 150 vnodes)
  │                       │     circuit breaker + singleflight + negative cache
  │                       └── PostgreSQL replica pool (falls back to primary)
  │                    └── fire-and-forget Publish(ClickEvent)
  │                            └── RabbitMQ → analytics-worker(s)
  │                                               └── batch flush → PostgreSQL
  │
  └── GET /health  (amqp_connected, cache_cb, rate_limiter_cb states)


write-service (Go/Gin)
  ├── OTel middleware → Jaeger (:16686)
  ├── Metrics middleware → Prometheus (:9090)
  │
  ├── POST /api/v1/shorten ──► URLRepository → PostgreSQL primary pool
  │                                └── cache-aside write → Redis hash ring
  ├── DELETE /api/v1/urls/:code ──► URLRepository → PostgreSQL primary pool
  │                                    └── cache invalidation → Redis hash ring
  │
  └── GET /health
```

---

## Technology Stack

| Layer | Technology | Notes |
|---|---|---|
| API Gateway | Go 1.25, Gin, nginx | read-service (GET redirects, rate-limit, cache, AMQP publish) and write-service (POST/DELETE, no rate-limit); nginx routes by HTTP method |
| Rate Limiter | Rust, tonic (gRPC), tokio | Token bucket via atomic Lua script; single Redis round-trip |
| Cache | Redis 8 (3-node hash ring) | SHA-256 consistent hashing, 150 vnodes/node, circuit breaker |
| Database | PostgreSQL 16, pgx/v5 pool | Separate pools per service: read (MaxConns=5), write (MaxConns=10); read path ~99% served from cache |
| Message broker | RabbitMQ 4, quorum queue | Fire-and-forget from read-service isolates redirect latency from analytics; worker batch-flushes 100 events or 5s ticker |
| Analytics Worker | Go 1.25 | Separate module; on DB error, process exits without ack — `restart: on-failure` in Compose requeues messages automatically, preserving at-least-once delivery without explicit nack logic |
| Observability | OpenTelemetry SDK, Jaeger, Prometheus, Grafana | Trace IDs injected into slog fields; cache_node labels on metrics; Grafana dashboards for analytics pipeline and cache ring |
| Chaos testing | Toxiproxy, bash scenarios | 6 automated fault injection scenarios; chaos overlay via `docker-compose.chaos.yml` |
| Load testing | k6 | Baseline / spike / endurance / throughput tests; web dashboard |
| CI/CD | GitHub Actions | Go lint+test, Rust clippy+test, prod build smoke test, chaos CI |

---

## Quick Start

```bash
# Clone and start the full dev stack
git clone https://github.com/kaoozhi/zhejian-url
cd zhejian-url
docker compose up --build -d

# Verify everything is up
curl http://localhost/health
# {"status":"ok","amqp_connected":true,"cache_cb":"closed","rate_limiter_cb":"closed"}

# Create a short URL
curl -s -X POST http://localhost/api/v1/shorten \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com"}' | jq .
# {"short_url":"http://localhost/AbCd3F","short_code":"AbCd3F"}

# Follow the redirect
curl -v http://localhost/AbCd3F
# HTTP/1.1 301  Location: https://example.com
```

**Observability UIs:**

| Service | URL |
|---|---|
| Jaeger (distributed traces) | http://localhost:16686 |
| Prometheus (metrics) | http://localhost:9090 |
| Grafana (dashboards) | http://localhost:3000 |
| RabbitMQ management | http://localhost:15672 (guest/guest) |

---

## Engineering Highlights

### 1. Automated Chaos Engineering

Resilience properties written in code but never exercised under failure are claims, not facts. A Toxiproxy overlay (`docker-compose.chaos.yml`) and an automated bash suite (`chaos-test.sh`) execute six fault injection scenarios against the running stack to verify each architectural response.

| Component | Injected Fault | Architectural Response (Verified Behavior) |
|---|---|---|
| **Rate Limiter** | +500ms gRPC latency | Circuit breaker trips; system **fails-open** to allow traffic; recovers automatically. |
| **Cache Ring** | +200ms Redis latency | 50ms operation timeout triggers; cache CB opens; reads **fallback to PostgreSQL**. |
| **Cache Ring** | Total Redis outage (TCP timeout) | CB opens instantly; zero downtime as PostgreSQL safely absorbs the read path. |
| **Database** | PostgreSQL container stopped | `/health` reliably degrades to HTTP 503; auto-recovers to 200 on DB restart. |
| **Analytics Queue**| RabbitMQ network partition | Publisher auto-reconnects with backoff; consumer ensures **at-least-once delivery**. |
| **Message Broker**| `SIGKILL` on Replica Node | **Quorum queue** maintains availability and guarantees zero message loss during node failure. |

- [`scripts/chaos-test.sh`](scripts/chaos-test.sh) — chaos scenarios 

```bash
# Run all chaos scenarios
./scripts/chaos-test.sh prod --cluster --workers 3 # activate RabbitMQ cluster and 3 analytics workers

# Run a specific scenario
./scripts/chaos-test.sh --scenario 2
```

---

### 2. Rust Rate Limiter — Distributed Token Bucket

In-process rate limiting breaks the moment you run more than one read-service instance — each process has its own counter, and the limits are never shared. Moving the state to Redis solves the coordination problem, but a naïve read-modify-write leaves a TOCTOU race. A Lua script eliminates both.

**The algorithm** — the token bucket Lua script executes atomically on Redis: `HMGET` reads current tokens and last-refill timestamp, computes elapsed time to refill, deducts one token if available, writes back with `HMSET`, sets a 2-minute expiry. All in a single round-trip.

```lua
local elapsed    = math.max(0, now - last_refill)
local new_tokens = math.min(burst, tokens + elapsed * rate / 60000)
-- allowed = new_tokens >= 1; remaining = floor(new_tokens - 1)
```

**Why Rust** — the rate limiter sits on the hot path of every redirect. Rust's async runtime (tokio) and zero-cost abstractions avoid GC pauses that would add unpredictable tail latency. `redis::aio::ConnectionManager` gives a multiplexed, auto-reconnecting connection; `tonic` provides the gRPC server. The Go read-service uses hand-written gRPC stubs so `protoc` is not required at runtime.

**Fail-open design** — read-service calls the rate limiter with a 100ms gRPC timeout. On timeout or error, the circuit breaker opens and all requests proceed. Rate enforcement is a best-effort availability improvement, not a hard gate that can take the service down.

**Verified by chaos** — scenario 1 in `chaos-test.sh` injects 500ms latency on the gRPC link, confirms the circuit breaker opens, verifies all requests still return 200 (fail-open), then confirms 429 enforcement resumes after recovery.

**Key files:**
- [`services/rate-limiter/src/token_bucket.lua`](services/rate-limiter/src/token_bucket.lua) — atomic Lua token bucket
- [`services/rate-limiter/src/token_bucket.rs`](services/rate-limiter/src/token_bucket.rs) — Rust wrapper + testcontainers integration tests
- [`services/gateway/internal/ratelimit/`](services/gateway/internal/ratelimit/) — hand-written gRPC client stubs

---

### 3. Analytics Pipeline and Competing Consumers

Click events must not block redirects. Publishing fire-and-forget isolates redirect latency from analytics write latency — but it shifts the problem: the queue must drain fast enough under load that backlog does not grow unboundedly.

```
GET /:code → 301 response (returned immediately)
              └── goroutine: Publish(ClickEvent) → RabbitMQ
                                                      └── analytics-worker(s)
                                                              ├── batch of 100 events
                                                              └── 5s ticker flush
                                                                    └── INSERT INTO analytics
```

The analytics worker uses a no-ack-on-error pattern: DB errors exit the process; `restart: on-failure` in Docker Compose requeues unacked messages automatically. The AMQP connection has a background `watchAndReconnect` goroutine for broker restarts.

With a single worker, the queue cannot drain fast enough under high load. Grafana dashboard (Analytics Pipeline) shows the contrast clearly.

**Before — 1 worker:**

![1 worker — queue grows unboundedly](docs/findings/1_worker.png)

Ready (backlog) climbs to ~4,000; unacked stays ~200 — only one worker's prefetch window in flight. The staircase shape reflects batch flush cycles (100 events or 5s ticker).

**After — 3 workers:**

![3 workers — queue stays near zero](docs/findings/3_workers.png)

Ready stays ~0 — messages are consumed as fast as they arrive. Unacked rises to ~600 (3 workers × ~200 each, all actively processing batches in parallel). The high unacked count here is healthy: it means messages are in-flight, not queued.

No code change for competing consumers — `docker compose up -d --scale analytics-worker=3` is sufficient. The only code change was upgrading to a **quorum queue** for broker-crash durability:

```go
args := amqp.Table{
    "x-dead-letter-exchange":    "",
    "x-dead-letter-routing-key": dlqName,
    "x-queue-type":              "quorum", // Raft-replicated; survives node crashes
}
```

| Metric | 1 worker | 3 workers |
|---|---|---|
| Peak ready (backlog) | ~4,000 | ~0 |
| Peak unacked (in-flight) | ~200 | ~600 |
| Redirect p95 | Unchanged | Unchanged |

**Key files:**
- [`services/analytics-worker/internal/consumer/consumer.go`](services/analytics-worker/internal/consumer/consumer.go) — batch consumer + quorum queue declaration
- [`docs/findings/2026-03-31-phase-10c-competing-consumers.md`](docs/findings/2026-03-31-phase-10c-competing-consumers.md) — full findings

---

### 4. Redis Consistent Hash Ring

With a single Redis node, all cache keys hash to one instance — any hotspot or saturation event affects every key. A consistent hash ring distributes routing across nodes so that key assignment is stable under topology changes and load is spread by design, not by luck.

A SHA-256 consistent hash ring routes cache keys across 3 Redis nodes. All tests ran on a single machine (Mac or WSL2), so all three Redis processes share the same CPU, memory, and loopback interface — adding nodes redistributes routing, not capacity. Throughput was identical between single-node and ring configurations at high VU counts: on a shared host, the ring is a **routing-correctness exercise**, not a throughput experiment.

The observable result in Prometheus is **even key distribution across nodes** (see dashboard screenshot below). To see a real throughput benefit, the nodes would need to run on separate machines — out of scope for this project.

![load distribution across node](docs/findings/cache_node_load.png)

**Design decisions:**
- 150 virtual nodes per real node → ≈2–3% distribution error (verified in unit tests)
- `hashKey`: SHA-256 → `uint32` via `binary.BigEndian.Uint32(h[:4])` — uniform without modular bias
- `findIndex`: lower-bound binary search on sorted vnode slice — O(log N) routing
- `Add`/`Remove` methods for dynamic topology; consistent hashing means only ~1/N keys remap on node change
- `ClientProvider` interface: `CachedURLRepository` is unchanged whether backed by one node or a ring

**Key files:**
- [`services/gateway/internal/cache/ring.go`](services/gateway/internal/cache/ring.go) — hash ring implementation
- [`services/gateway/internal/cache/ring_test.go`](services/gateway/internal/cache/ring_test.go) — distribution, remap, determinism tests
- [`services/gateway/internal/infra/infra.go`](services/gateway/internal/infra/infra.go) — `NewCacheRings` multi-node client construction

---

### 5. Cache Layer Resilience: Four Defensive Patterns

A Redis cache miss under concurrent load without defences causes a DB stampede. Four layers prevent it — each addressing a distinct failure mode:

| Pattern | What it prevents |
|---|---|
| `singleflight.Do` | N concurrent misses for the same key → 1 DB query; all callers share the result |
| Circuit breaker (dual-condition) | Sustained Redis failure → fail to DB; recovers automatically |
| Negative caching (`__NOT_FOUND__`) | Repeated lookups for non-existent keys hit DB only once |
| `context.WithTimeout(50ms)` | Slow Redis node never blocks longer than one request's budget |

`singleflight.Do` groups concurrent cache misses for the same key into a single DB query. The callback re-checks the cache first (double-checked locking: a previous call may have already populated it). The DB query uses `context.WithoutCancel` — if one of the N waiting callers cancels its request, the query continues for the others.

Default 5-consecutive-failure trips caused false positives during brief Redis latency spikes. Revised to a dual-condition breaker:

```go
// Primary: rate-based (more nuanced — filters one-off errors)
if counts.Requests >= cb.MinRequestsToTrip && rate > cb.FailureRateThreshold {
    return true
}
// Secondary: absolute fast-path for total outages
return counts.ConsecutiveFailures >= cb.ConsecutiveFailures
```

Defaults: 50-request window, 20% failure rate, 20 consecutive failures. All tunable via env vars (`CACHE_CB_*`).

Negative caching stores a `__NOT_FOUND__` sentinel for missing keys (1-minute TTL), preventing repeated DB lookups when a short code does not exist.

Cache calls also use a 50ms `context.WithTimeout` (`CACHE_OPERATION_TIMEOUT`) independent of TCP timeouts — ensures the read-service never blocks on a slow Redis node longer than one request's budget.

**Key files:**
- [`services/gateway/internal/repository/cached_url_repository.go`](services/gateway/internal/repository/cached_url_repository.go) — all four patterns implemented here

---

### 6. CQRS Service Split — Scaling Reads Independently

URL shortening is read-heavy: every short URL is created once but redirected many times. A monolithic gateway forces both paths to share resources, deploy together, and scale as a unit. The read/write split separates them so each can be sized, tuned, and scaled for its actual workload.

```
nginx (:80)
  ├── GET  → read-service  (rate limiter, cache ring, replica DB pool, AMQP publisher)
  └── POST/DELETE → write-service  (primary DB pool, cache invalidation, no rate limiter)
```

**Why split at the service level, not just the DB pool level?** A DB pool split was implemented and load tested first. The data showed it didn't help:

| | p95 | req/s |
|---|---|---|
| Before (single pool) | 207ms | 5,575 |
| After (split DB pools) | 261ms | 4,481 |

Redis absorbs ~99% of redirect reads — the DB pool was never the bottleneck. Running two PostgreSQL containers on the same host only added CPU contention. The pool split was reverted.

The *service* split addresses the real scaling axis: read-service handles the high-volume redirect path with its own cache, rate limiter, and replica-capable DB pool. Write-service handles low-volume creates with a primary DB pool and no rate limiter overhead. Each is stateless and independently deployable — in production, you'd run N read-service instances behind a load balancer while write-service stays at 1-2 instances.

**Key files:**
- [`services/gateway/cmd/read-server/`](services/gateway/cmd/read-server/) — read-service entry point
- [`services/gateway/cmd/write-server/`](services/gateway/cmd/write-server/) — write-service entry point
- [`infrastructure/nginx/nginx.conf`](infrastructure/nginx/nginx.conf) — method-based routing

---

## Load Testing

The k6 test suite covers four scenarios, each measuring a different property:

| Test | VUs | Purpose |
|---|---|---|
| Baseline | 100 | p95 SLO validation under steady load with rate limiter active |
| Spike | 50→300 | Behavior during rapid VU ramp (circuit breakers, queue buildup) |
| Throughput ceiling | 1000 | Raw req/s ceiling with rate limiter disabled |
| Endurance | 100, 12min | Memory leaks, connection pool exhaustion, queue growth over time |

### A note on single-machine numbers

Every service in this project — read-service, write-service, nginx, Redis (3 nodes), PostgreSQL, RabbitMQ, rate limiter, k6 — runs on the same WSL2/Mac host and shares the same CPU and memory. Under these conditions, **the throughput ceiling (~8,100 req/s) is a property of the host, not of the architecture**. Adding nodes or splitting services on the same machine redistributes CPU contention without removing it, which is why the DB read-write split showed no gain and the hash ring showed no throughput increase.

The load tests serve two purposes here:
- **Correctness validation** — confirming that the architecture behaves correctly under load (zero errors, circuit breakers trip and recover as expected, competing consumers drain the queue)
- **Relative comparison** — measuring the same configuration change twice on the same hardware (e.g., 1 worker vs. 3 workers, single-node vs. hash ring) to isolate the effect of that change

What matters most is that the architecture is designed to scale horizontally: read-service and write-service are stateless and independently deployable, the Redis ring handles node additions with minimal key remapping, and competing consumers scale with `--scale analytics-worker=N`. On separate machines, each of these patterns would show real throughput gains. On a single host, the load tests confirm the patterns work correctly — not how fast they can go.

### Results (single-machine baseline)

| Test | p95 | req/s | Error rate |
|---|---|---|---|
| Baseline (rate limiter active) | ~200ms | — | 0% |
| Throughput ceiling | 193ms | ~8,100 | 0% |

```bash
make load-baseline          # 100 VUs, 9min, rate limiter active
make load-spike             # 50→300 VU spike
make load-endurance         # 12min stability

# Throughput ceiling (rate limiter must be disabled first)
RATE_LIMITER_ADDR="" make load-throughput-ring    # 3-node hash ring
RATE_LIMITER_ADDR="" make load-throughput-single  # single node (for comparison)
```

---

## Project Structure

```
zhejian-url/
├── services/
│   ├── gateway/               # Go services (shared module)
│   │   ├── cmd/read-server/   # read-service entry point (GET redirects)
│   │   ├── cmd/write-server/  # write-service entry point (POST/DELETE)
│   │   └── internal/
│   │       ├── api/           # HTTP handlers + health endpoint
│   │       ├── cache/         # ClientProvider interface + HashRing
│   │       ├── config/        # Env var loading (godotenv)
│   │       ├── infra/         # pgxpool + Redis client construction
│   │       ├── middleware/     # Logging, metrics, rate-limit
│   │       ├── observability/ # OTel setup (tracer, meter, logger)
│   │       ├── ratelimit/     # Hand-written gRPC client stubs
│   │       ├── repository/    # URLRepository + CachedURLRepository
│   │       ├── server/        # Router wiring
│   │       └── service/       # URL shortening business logic
│   ├── rate-limiter/          # Rust gRPC service (tonic + tokio)
│   └── analytics-worker/      # Go consumer (separate module)
├── migrations/                # SQL schema + golang-migrate container
├── tests/                     # k6 load test scripts
│   ├── baseline.js            # 100 VUs, p95 SLO validation
│   ├── spike.js               # 50→300 VU spike test
│   ├── throughput.js          # Raw ceiling (rate limiter disabled)
│   ├── endurance.js           # 12-minute stability test
│   ├── hotkey.js              # Hot-key concentration (Phase 11 prep)
│   └── analytics-load.js      # Analytics pipeline load test
├── scripts/
│   └── chaos-test.sh          # Toxiproxy fault injection scenarios
├── observability/
│   ├── prometheus/            # Prometheus scrape configs + alert rules
│   └── grafana/               # Dashboards + provisioning (analytics pipeline, cache ring)
├── docs/
│   ├── BUILD_PHASES.md        # Build history and per-phase findings
│   └── plans/                 # Per-phase implementation plans
├── infrastructure/nginx/      # nginx routing config (POST/DELETE → write, GET → read)
├── docker-compose.yml                  # Dev stack (read-service, write-service, nginx, Redis, RabbitMQ, Jaeger)
├── docker-compose.prod.yml             # Production stack
├── docker-compose.chaos.yml            # Chaos overlay (adds Toxiproxy)
├── docker-compose.rabbitmq-cluster.yml # 3-node RabbitMQ cluster overlay
└── Makefile                   # load-baseline, load-throughput-*, load-hotkey-*
```

---

## Running Tests

```bash
# Unit + integration tests (spins up real Postgres/Redis/RabbitMQ via testcontainers)
cd services/gateway && go test ./... -v
cd services/analytics-worker && go test ./... -v

# Rust rate limiter
cd services/rate-limiter && cargo test && cargo clippy

# Load tests (k6 required)
make load-baseline          # 100 VUs, 9min, rate limiter active
make load-spike             # 50→300 VU spike
make load-endurance         # 12min stability (local only)

# Throughput ceiling (rate limiter must be disabled)
RATE_LIMITER_ADDR="" make load-throughput-ring    # 3-node hash ring
RATE_LIMITER_ADDR="" make load-throughput-single  # single node (for comparison)
```

---

## Configuration

All configuration is via environment variables. Key toggles:

| Variable | Default | Effect |
|---|---|---|
| `RATE_LIMITER_ADDR` | `rate-limiter:50051` | Set to `""` to disable rate limiting |
| `AMQP_URL` | `amqp://guest:guest@rabbitmq-1:5672/` | Set to `""` to disable analytics (disabled on write-service) |
| `CACHE_NODES` | `redis-1:6379,redis-2:6379,redis-3:6379` | Comma-separated Redis nodes |
| `CACHE_OPERATION_TIMEOUT` | `150ms` | Per-cache-call context deadline |
| `CACHE_CB_MIN_REQUESTS` | `50` | CB window size before rate check |
| `CACHE_CB_FAILURE_RATE` | `0.2` | CB trip threshold (0.0–1.0) |
| `DB_REPLICA_HOST` | `""` | Postgres read replica hostname for read-service; falls back to primary when empty |
| `DB_MAX_CONNS` | `5` (read), `10` (write) | pgxpool max connections per service |
| `READ_PORT` | `8080` | read-service listen port |
| `WRITE_PORT` | `8081` | write-service listen port |

---

## License

MIT
