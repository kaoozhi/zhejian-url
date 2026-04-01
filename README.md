[![Go](https://github.com/kaoozhi/zhejian-url/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/kaoozhi/zhejian-url/actions/workflows/go.yml)
[![Rust](https://github.com/kaoozhi/zhejian-url/actions/workflows/rust.yml/badge.svg?branch=main)](https://github.com/kaoozhi/zhejian-url/actions/workflows/rust.yml)
[![Docker Compose](https://github.com/kaoozhi/zhejian-url/actions/workflows/production.yml/badge.svg)](https://github.com/kaoozhi/zhejian-url/actions/workflows/production.yml)
[![Chaos](https://github.com/kaoozhi/zhejian-url/actions/workflows/chaos.yml/badge.svg?branch=main)](https://github.com/kaoozhi/zhejian-url/actions/workflows/chaos.yml)
[![codecov](https://codecov.io/gh/kaoozhi/zhejian-url/branch/main/graph/badge.svg?token=XPQ70FTF7M)](https://codecov.io/gh/kaoozhi/zhejian-url)

# Zhejian URL Shortener

A production-grade URL shortener built incrementally across 13 engineering phases — from a bare CRUD API to a distributed system with a consistent hash ring, chaos-tested resilience patterns, and load-tested throughput benchmarks.

**Key results:**
- **8,100 req/s** redirect throughput at p95=193ms (WSL2, 1000 VUs, 0% errors, rate limiter disabled)
- 3-node Redis ring eliminated CB oscillation seen with single-node saturation
- 5 automated chaos scenarios (Redis latency, Redis down, Postgres degradation, rate-limiter faults, network partition)

---

## Architecture

```
Client
  │
  ▼
Gateway (Go/Gin, :8080)
  ├── OTel middleware → Jaeger (:16686)
  ├── Metrics middleware → Prometheus (:9090)
  ├── Rate-limit middleware
  │     └── gRPC (100ms timeout, fail-open) → Rust rate-limiter (:50051)
  │                                                └── Redis token bucket
  │
  ├── GET /:code  ──► CachedURLRepository
  │                       ├── Redis hash ring (3 nodes, SHA-256, 150 vnodes)
  │                       │     circuit breaker + singleflight + negative cache
  │                       └── PostgreSQL (pgxpool, fallback on CB open)
  │
  ├── POST /api/v1/shorten ──► URLRepository → PostgreSQL
  │                                └── fire-and-forget Publish(ClickEvent)
  │                                        └── RabbitMQ → analytics-worker
  │                                                            └── batch flush → PostgreSQL
  │
  └── GET /health  (amqp_connected, cache_cb, rate_limiter_cb states)
```

---

## Technology Stack

| Layer | Technology | Notes |
|---|---|---|
| API Gateway | Go 1.23, Gin | Middleware chain: tracing → logging → metrics → rate-limit |
| Rate Limiter | Rust, tonic (gRPC), tokio | Token bucket via atomic Lua script; single Redis round-trip |
| Cache | Redis 8 (3-node hash ring) | SHA-256 consistent hashing, 150 vnodes/node, circuit breaker |
| Database | PostgreSQL 16, pgx/v5 pool | pgxpool MaxConns=10; read path ~99% served from cache |
| Message broker | RabbitMQ 4, durable queue | Fire-and-forget publishes; worker batch-flushes 100 events or 5s |
| Analytics Worker | Go 1.23 | Separate module; `restart: on-failure` + nack-free error recovery |
| Observability | OpenTelemetry SDK, Jaeger, Prometheus | Trace IDs injected into slog fields; cache_node labels on metrics |
| Chaos testing | Toxiproxy, bash scenarios | 5 fault scenarios; chaos overlay via `docker-compose.chaos.yml` |
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
curl http://localhost:8080/health
# {"status":"ok","amqp_connected":true,"cache_cb":"closed","rate_limiter_cb":"closed"}

# Create a short URL
curl -s -X POST http://localhost:8080/api/v1/shorten \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com"}' | jq .
# {"short_url":"http://localhost:8080/AbCd3F","short_code":"AbCd3F"}

# Follow the redirect
curl -v http://localhost:8080/AbCd3F
# HTTP/1.1 301  Location: https://example.com
```

**Observability UIs:**

| Service | URL |
|---|---|
| Jaeger (distributed traces) | http://localhost:16686 |
| Prometheus (metrics) | http://localhost:9090 |
| RabbitMQ management | http://localhost:15672 (guest/guest) |

---

## Engineering Highlights

### 1. Redis Consistent Hash Ring (Phase 10B)

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

### 2. Circuit Breaker Tuning (Phase 8 → 10B)

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

Cache calls also use a 50ms `context.WithTimeout` (`CACHE_OPERATION_TIMEOUT`) independent of TCP timeouts — ensures the gateway never blocks on a slow Redis node longer than one request's budget.

---

### 3. Phase 10A: Null Result (DB Read-Write Split)

The hypothesis was that DB pool contention limited throughput. After implementing PG read-write split (separate `readDB`/`writeDB` pools):

| | p95 | req/s |
|---|---|---|
| Before (single pool) | 207ms | 5,575 |
| After (split pools) | 261ms | 4,481 |

**Root cause:** Redis cache absorbs ~99% of redirect reads. The DB pool was never congested — it was idle most of the time. Running two PG containers on WSL2 consumed enough host resources to make after worse than before. The implementation was reverted. This is documented as a deliberate engineering finding: measuring before optimizing.

---

### 4. Chaos Testing (Phase 8)

Five automated fault injection scenarios using Toxiproxy:

| Scenario | Fault | Expected behavior |
|---|---|---|
| Redis latency | 200ms added latency to all Redis nodes | Cache CB opens; fallback to DB; fail-open |
| Redis down | Connection refused | CB stays open; DB handles all reads |
| Postgres degradation | 500ms added latency | Slow redirects; no errors |
| Rate limiter fault | gRPC connection reset | Fail-open: all requests proceed |
| Network partition | All Toxiproxy proxies down | CB open; graceful degradation |

```bash
# Run all chaos scenarios
docker compose -f docker-compose.yml -f docker-compose.chaos.yml up --build -d
./scripts/chaos-test.sh

# Run a specific scenario
./scripts/chaos-test.sh --scenario 2
```

---

### 5. Analytics Pipeline + Competing Consumers (Phase 7 → 10C)

Click events flow fire-and-forget so redirects are never blocked waiting for analytics:

```
GET /:code → 301 response (returned immediately)
              └── goroutine: Publish(ClickEvent) → RabbitMQ
                                                      └── analytics-worker(s)
                                                              ├── batch of 100 events
                                                              └── 5s ticker flush
                                                                    └── INSERT INTO analytics
```

The analytics worker uses a no-ack-on-error pattern: DB errors exit the process; `restart: on-failure` in Docker Compose requeues unacked messages automatically. The AMQP connection has a background `watchAndReconnect` goroutine for broker restarts.

**Phase 10C — competing consumers:** a single worker cannot drain the queue fast enough under high load. Grafana dashboard (Analytics Pipeline) shows the contrast clearly.

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

## Project Structure

```
zhejian-url/
├── services/
│   ├── gateway/               # Go API Gateway (main service)
│   │   ├── cmd/server/        # Entry point
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
│   └── prometheus/            # Prometheus scrape configs
├── docs/
│   ├── BUILD_PHASES.md        # Full 10-phase build roadmap with findings
│   └── plans/                 # Per-phase implementation plans
├── docker-compose.yml         # Dev stack (3 Redis nodes, RabbitMQ, Jaeger)
├── docker-compose.prod.yml    # Production stack
├── docker-compose.chaos.yml   # Chaos overlay (adds Toxiproxy)
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
| `AMQP_URL` | `amqp://guest:guest@rabbitmq:5672/` | Set to `""` to disable analytics |
| `CACHE_NODES` | `redis-1:6379,redis-2:6379,redis-3:6379` | Comma-separated Redis nodes |
| `CACHE_OPERATION_TIMEOUT` | `150ms` | Per-cache-call context deadline |
| `CACHE_CB_MIN_REQUESTS` | `50` | CB window size before rate check |
| `CACHE_CB_FAILURE_RATE` | `0.2` | CB trip threshold (0.0–1.0) |
| `DB_REPLICA_URL` | `""` | Read replica (Phase 10A — reverted, documented) |

---

## Build Phases

| Phase | Description | Status |
|---|---|---|
| 1 | Core CRUD API + PostgreSQL schema | ✅ Complete |
| 2 | CI + test automation (GitHub Actions) | ✅ Complete |
| 3 | Docker Compose + init-container migrations | ✅ Complete |
| 4 | Redis cache-aside + circuit breaker + singleflight | ✅ Complete |
| 5 | OTel tracing + Prometheus metrics + structured logging | ✅ Complete |
| 6 | Rust gRPC rate limiter (token bucket, fail-open) | ✅ Complete |
| 7 | Analytics pipeline (RabbitMQ + async worker) | ✅ Complete |
| 8 | Chaos testing (Toxiproxy, 5 scenarios, CI workflow) | ✅ Complete |
| 9 | k6 load testing (baseline/spike/endurance/throughput) | ✅ Complete |
| 10A | PG read-write split | ✅ Attempted + reverted (null result, documented) |
| 10B | Redis consistent hash ring (3 nodes) | ✅ Complete |
| 10C | RabbitMQ competing consumers + quorum queue | ✅ Complete |
| 10D | PgBouncer connection pooling | Skipped (DB not bottleneck, same as 10A) |
| 11 | Grafana dashboards & alerting | Planned |
| 12 | Chaos engineering lite (automated scenarios) | Planned |
| 13 | Documentation & polish | Planned |

See [docs/BUILD_PHASES.md](docs/BUILD_PHASES.md) for detailed task lists, design rationale, and measured results per phase.

---

## License

MIT
