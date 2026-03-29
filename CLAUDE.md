# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Overview

Production-grade URL shortener built as a portfolio project. The full build roadmap is in `docs/BUILD_PHASES.md` — read it before starting any new phase. The active implementation plan for the next phase lives in `docs/plans/`.

## Services

Three independent modules — each has its own build toolchain:

| Service | Language | Path | Module |
|---|---|---|---|
| Gateway | Go | `services/gateway/` | `github.com/zhejian/url-shortener/gateway` |
| Analytics Worker | Go | `services/analytics-worker/` | `github.com/zhejian/url-shortener/analytics-worker` |
| Rate Limiter | Rust | `services/rate-limiter/` | — |

## Commands

### Go (Gateway and Analytics Worker)

```bash
# Run all tests (from service root)
cd services/gateway && go test ./... -v
cd services/analytics-worker && go test ./... -v

# Run a single test
cd services/gateway && go test ./internal/repository/... -run TestCachedURLRepository -v

# Build
cd services/gateway && go build -o bin/gateway cmd/server/main.go

# Lint
cd services/gateway && golangci-lint run
```

### Rust (Rate Limiter)

```bash
cd services/rate-limiter && cargo test
cd services/rate-limiter && cargo clippy
cd services/rate-limiter && cargo build --release
```

### Docker Compose

```bash
# Full dev stack (gateway at :8080, Prometheus :9090, Jaeger :16686, RabbitMQ :15672)
docker compose up --build -d

# Chaos overlay — adds Toxiproxy in front of rate-limiter, Redis, Postgres
docker compose -f docker-compose.yml -f docker-compose.chaos.yml up --build -d

# Run chaos scenarios
./scripts/chaos-test.sh
./scripts/chaos-test.sh --scenario 2
```

### Load Testing (k6)

```bash
# Baseline (100 VUs, 9 min, rate limiter active, per-VU IP spoofing)
make load-baseline

# Throughput ceiling — rate limiter MUST be disabled first
RATE_LIMITER_ADDR="" docker compose up -d gateway
make load-throughput          # 1000 VUs, no sleep, baseline: p95=193ms ~8100 req/s

# Capture error details
k6 run --out json=results/raw.json tests/throughput.js
jq -r 'select(.metric == "http_reqs") | .data.tags.error // "none"' results/raw.json | sort | uniq -c | sort -rn
```

## Architecture

### Request Flow

```
Client → Gateway (Gin)
  → RateLimit middleware (gRPC, 100ms timeout, fail-open)
  → CachedURLRepository (Redis cache-aside → singleflight → pgxpool)
  → fire-and-forget Publish(ClickEvent) → RabbitMQ
                                              → analytics-worker (batch flush → Postgres)
```

### Gateway Internal Layers

- `internal/config/` — env var loading via `godotenv`; `AMQP_URL=""` disables analytics, `RATE_LIMITER_ADDR=""` disables rate limiter, `DB_REPLICA_URL=""` reads fall back to write pool
- `internal/infra/` — `pgxpool` (MaxConns=10) and Redis client construction
- `internal/repository/` — `URLRepository` (pgx), wrapped by `CachedURLRepository` (Redis + `gobreaker` circuit breaker + `singleflight`)
- `internal/analytics/publisher.go` — AMQP connection with background `watchAndReconnect` goroutine; nil `amqpErr` on graceful broker shutdown is handled (does NOT return early)
- `internal/ratelimit/` — hand-written gRPC stubs (no `protoc` needed at runtime; generated files committed)
- `internal/api/handler.go` — `/health` exposes `amqp_connected`, `cache_cb`, `rate_limiter_cb` states

### CachedURLRepository Key Behaviors

- Circuit breaker (`gobreaker`): 5 consecutive failures → open (30s) → half-open; `redis.Nil` is NOT a failure
- Cache operations wrap a 50ms `context.WithTimeout` (`CACHE_OPERATION_TIMEOUT`); TCP timeouts are belt-and-suspenders (`CACHE_READ_TIMEOUT`, `CACHE_WRITE_TIMEOUT`)
- Negative caching: 1-minute sentinel `__NOT_FOUND__` prevents repeated DB lookups for missing keys
- `singleflight`: concurrent cache misses for the same key collapse into one DB query

### Analytics Worker

Separate Go module. `consumer.go` declares `analytics.clicks` as a **durable** queue (will become quorum in Phase 10C). Queue type cannot be changed after creation — requires `docker compose down -v` to reset.

Flush strategy: batch of 100 OR 5s ticker. On DB error: returns without ack/nack → process exits → `restart: on-failure` → RabbitMQ requeues unacked messages.

## Testing Infrastructure

Integration tests use **testcontainers-go** — they require a Docker daemon and spin up real Postgres/Redis/RabbitMQ containers automatically. No manual setup needed.

```go
// Test helpers in services/gateway/internal/testutil/
testutil.SetupTestDB(ctx)      // Postgres + migrations applied
testutil.NewCacheClient(t)     // Redis
testutil.NewRabbitMQ(t)        // RabbitMQ
```

Migrations path is resolved relative to `testutil/testdb.go` via `runtime.Caller(0)` → points to `migrations/schema/`.

## Docker Compose Notes

- `RATE_LIMITER_ADDR` uses `${RATE_LIMITER_ADDR-rate-limiter:50051}` (single hyphen, not `:-`) — empty string disables it. Setting in `.env` overrides shell env, so clear it from `.env` if disabling for throughput tests.
- `docker-compose.chaos.yml` sets `CACHE_READ_TIMEOUT=100ms` so 200ms Toxiproxy latency reliably trips the Redis circuit breaker.
- Prometheus has `--web.enable-remote-write-receiver` for k6 remote write: `k6 run -o experimental-prometheus-rw tests/baseline.js`

## Active Plans

- **Phase 10 (NEXT):** `docs/plans/2026-03-06-scaling-comparison-plan.md` — PG read-write split (10A), Redis consistent hash ring (10B), RabbitMQ competing consumers + quorum queue (10C), PgBouncer (10D). Each pattern uses env vars as implicit toggles; no explicit feature flags.
- Throughput baseline for comparisons: **1000 VUs, p95=193ms, ~8100 req/s** (rate limiter disabled, WSL2, 3-node ring).
