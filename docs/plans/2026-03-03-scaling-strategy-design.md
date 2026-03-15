# Scaling Strategy Design

**Project:** zhejian-url — URL Shortener
**Date:** 2026-03-03
**Phase:** Post-Phase 8 (resilience complete)

---

## Goal

Demonstrate three distinct, industry-relevant scaling patterns — one per infrastructure layer — each independently testable with load and chaos tests. The system stays on Docker Compose throughout; complexity is in the application code, not in cloud-managed services.

---

## Workload Profile

Understanding the URL shortener's access pattern is what justifies each scaling choice:

| Operation | Volume | Direction |
|---|---|---|
| `GET /:code` (redirect) | Very high | Read-dominant |
| `GET /api/v1/urls/:code` | Low | Read |
| `POST /api/v1/shorten` | Low | Write |
| `DELETE /api/v1/urls/:code` | Very low | Write |
| Analytics click events | High (async) | Write (queue) |

The system is **overwhelmingly read-heavy**. This asymmetry directly motivates the PostgreSQL and Redis strategies.

---

## Layer 1: Redis — Consistent Hash Ring

### Problem

A single Redis node is a throughput ceiling and a single point of failure for the cache. Adding a second node with naive modulo hashing (`key % N`) remaps ~50% of all keys on every topology change — acceptable for 2 nodes, catastrophic for N.

### Pattern: Client-side consistent hashing with virtual nodes

Spin up 3 Redis nodes. In Go, implement a `HashRing` that maps any cache key deterministically to one of the three nodes. Each physical node owns V=150 virtual nodes spread uniformly around the ring, ensuring even key distribution even with heterogeneous key spaces.

```
                    ring (hash space 0 … 2³²-1)
        ┌─────────────────────────────────────────┐
        │  [vnode-r1] [vnode-r2] [vnode-r3] ...   │
        │  [vnode-r2] [vnode-r1] [vnode-r3] ...   │
        └─────────────────────────────────────────┘
                          │
          Get("abc123") → hash("abc123") → binary search → redis-2
          Get("xyz999") → hash("xyz999") → binary search → redis-1
```

**Key property demonstrated:** removing one node only remaps ~1/3 of keys, not all of them. With 3 nodes this contrast is clear in a load test (miss spike is proportional, not catastrophic).

### Go implementation outline

```
services/gateway/internal/cache/
  ring.go          — HashRing struct: sorted vnodes, Add/Remove/Get(key)
  ring_test.go     — distribution uniformity, node removal remaps 1/3 of keys
```

`CachedURLRepository` receives the ring via a `CacheRouter` interface:

```go
type CacheRouter interface {
    ClientFor(key string) *redis.Client
}
```

`cacheGet`, `cacheSet`, `cacheDel` call `r.router.ClientFor(key)` to obtain the correct client before executing. The existing circuit breaker wraps each per-node call unchanged.

### Infrastructure

```yaml
# docker-compose.yml additions
redis-1:
  image: redis:7-alpine
redis-2:
  image: redis:7-alpine
redis-3:
  image: redis:7-alpine
```

No Redis Cluster config needed — routing is entirely client-side in Go.

### Metrics added

```
cache_node_hits_total{node="redis-1|redis-2|redis-3"}   # distribution
cache_node_errors_total{node="..."}                      # per-node health
```

### Load test story

```
k6 steady load (200 VUs, redirects)
→ Prometheus shows cache_node_hits_total distributed ~33% each
→ All 3 nodes active, p99 latency stable
```

### Chaos test story

```
Scenario: remove redis-2 mid-load
→ cache_node_errors_total{node="redis-2"} spikes
→ ~33% of keys miss (CB absorbs, DB fallback for those keys)
→ remaining 67% of keys still served from redis-1 and redis-3
→ re-add redis-2 → keys gradually repopulate, miss rate drops
→ Contrast: naive modulo → 50%+ miss; consistent hash → ~33% miss
```

---

## Layer 2: PostgreSQL — Read-Write Split (Phase A) + PgBouncer (Phase B)

### Problem (Phase A)

A single PostgreSQL pool serves both reads and writes from the same connection budget. At high redirect load, `GetByCode` queries compete with `Create`/`Delete` for connections. Since reads dominate (~95%+ of traffic), a read replica lets reads scale independently without touching the primary.

### Pattern Phase A: Streaming replication + dual pgxpool

One primary (writes) and one replica (reads) via PostgreSQL streaming replication. The Go gateway gets two `pgxpool` instances routed in the repository layer:

```
Gateway
  writeDB (pgxpool → primary:5432)   ← Create, Delete
  readDB  (pgxpool → replica:5433)   ← GetByCode, GetAll

Primary ──streaming replication──► Replica
```

**Routing lives in the repository**, not in middleware. Only `URLRepository` changes — callers are unaffected.

```go
type URLRepository struct {
    writeDB *pgxpool.Pool  // env: DB_PRIMARY_URL
    readDB  *pgxpool.Pool  // env: DB_REPLICA_URL
}

func (r *URLRepository) GetByCode(ctx, code) → readDB
func (r *URLRepository) Create(ctx, url)     → writeDB
func (r *URLRepository) Delete(ctx, code)    → writeDB
```

Replica fallback: if `readDB.Ping` fails, `GetByCode` falls back to `writeDB` (circuit breaker guards `readDB`, same pattern as Redis CB).

### Infrastructure (Phase A)

```yaml
postgres-primary:
  image: postgres:16
  environment:
    POSTGRES_REPLICATION_MODE: master
    POSTGRES_REPLICATION_USER: replicator

postgres-replica:
  image: postgres:16
  environment:
    POSTGRES_REPLICATION_MODE: slave
    POSTGRES_MASTER_HOST: postgres-primary
```

Bitnami PostgreSQL image handles replication config automatically.

### Metrics added (Phase A)

```
db_query_total{pool="read|write"}      # confirm reads hit replica
db_pool_connections{pool="read|write"} # pool utilisation
```

### Load test story (Phase A)

```
k6 (300 VUs, 80% redirects / 20% creates)
→ db_query_total{pool="read"} ≈ 80% of all queries
→ primary stays nearly idle on writes
→ replica CPU/connections absorb redirect load
```

### Chaos test story (Phase A)

```
Scenario A: kill replica
→ readDB CB opens → GetByCode falls back to writeDB → 200 continues
→ health check shows db replica degraded
→ restart replica → CB half-opens → readDB recovers

Scenario B: kill primary
→ Create/Delete return 503 (write path has no fallback — correct)
→ GetByCode still serves reads from replica during primary outage
→ health check reports primary down
```

---

### Problem (Phase B)

With multiple gateway instances (or at high VU count in load test), each instance holds its own pgxpool. Total PostgreSQL connections = `instances × pool_size`. PostgreSQL degrades beyond ~100–200 concurrent connections.

### Pattern Phase B: PgBouncer in transaction mode

PgBouncer proxies all gateway connections and multiplexes them onto a small fixed backend pool. PostgreSQL sees a constant connection count regardless of how many gateway instances are running.

```
Gateway instance 1 (writePool) ──┐
Gateway instance 2 (writePool) ──┼──► PgBouncer-write (5 real PG conns) ──► primary
Gateway instance N (writePool) ──┘

Gateway instance 1 (readPool)  ──┐
Gateway instance 2 (readPool)  ──┼──► PgBouncer-read  (10 real PG conns) ──► replica
Gateway instance N (readPool)  ──┘
```

**Go code change:** zero. Only env vars change (`DB_PRIMARY_URL` and `DB_REPLICA_URL` now point at PgBouncer instead of Postgres directly).

**Known constraint:** PgBouncer transaction mode does not support prepared statements. `pgxpool` with `default_query_exec_mode=simple_protocol` handles this — one env var.

### Load test story (Phase B)

```
Without PgBouncer: scale gateway to 5 instances, k6 500 VUs
→ PostgreSQL hits max_connections → connection refused errors in load test

Add PgBouncer: same load
→ PostgreSQL sees 15 connections regardless of gateway count
→ Zero connection errors, stable throughput
```

---

## Layer 3: RabbitMQ — Competing Consumers + Quorum Queue

### Problem

A single analytics-worker is a throughput bottleneck. During load spikes the queue depth grows unboundedly. Additionally, the current classic durable queue offers no replication — if the RabbitMQ node crashes, unacked messages on disk may be lost.

### Pattern: Competing consumers + quorum queue

Scale `analytics-worker` horizontally. Each worker instance is an independent consumer on `analytics.clicks`. RabbitMQ distributes messages round-robin across all consumers. Replace the classic durable queue with a **quorum queue** (`x-queue-type: quorum`) for replicated message storage.

```
analytics.clicks (quorum queue)
        │
        ├──► analytics-worker-1  (batch 100, prefetch 200)
        ├──► analytics-worker-2  (batch 100, prefetch 200)
        └──► analytics-worker-3  (batch 100, prefetch 200)
```

**Go code change:** one line in worker's queue declaration — add `x-queue-type: quorum` to the `QueueDeclare` args. Everything else (batch logic, ack, DLQ routing) is unchanged.

**Infra change:** `docker compose up --scale analytics-worker=N` — no other config needed.

### Prefetch reasoning

`prefetch = 2 × batch_size` means each worker always has enough messages queued locally to fill its next batch without waiting for a RabbitMQ round-trip. At 3 workers × prefetch 200, up to 600 messages are in-flight simultaneously.

### Metrics story

RabbitMQ management plugin exposes queue depth via Prometheus. This makes the scaling benefit directly visible:

```
rabbitmq_queue_messages{queue="analytics.clicks"}   # depth over time
```

### Load test story

```
k6 burst: 5000 clicks/min for 2 minutes

1 worker:  queue depth climbs steadily → worker cannot keep up
2 workers: queue depth stabilises
3 workers: queue drains faster than it fills
```

### Chaos test story

```
Scenario: kill worker-2 of 3 mid-load
→ queue depth rises slightly (2 workers absorbing what 3 did)
→ no data loss (unacked messages requeued by RabbitMQ)
→ restart worker-2 → queue depth drops back to baseline
→ row count in analytics table = total events published (zero loss)
```

---

## Implementation Order

```
Phase 9A: PostgreSQL streaming replication + read-write split in Go
          → load test confirms replica absorbs reads
          → chaos test: kill replica → fallback; kill primary → 503

Phase 9B: Redis consistent hash ring (3 nodes + virtual nodes in Go)
          → load test confirms 33% key distribution per node
          → chaos test: remove one node → ~33% miss, not 50%+

Phase 9C: RabbitMQ competing consumers + quorum queue
          → load test: queue depth chart across 1/2/3 workers
          → chaos test: kill one worker → no data loss

Phase 9D: PgBouncer (optional follow-up to 9A)
          → load test: connection exhaustion without PgBouncer,
            stable with PgBouncer (zero code change in Go)
```

---

## Summary

| Layer | Pattern | Code change | Infra change | Key metric |
|---|---|---|---|---|
| Redis | Consistent hash ring | `HashRing` + `CacheRouter` in Go | 3 Redis containers | `cache_node_hits_total` distribution |
| PostgreSQL | Read-write split | Dual `pgxpool`, routing in repository | Primary + replica (streaming replication) | `db_query_total{pool}` |
| PostgreSQL (later) | PgBouncer | Zero | PgBouncer proxy | PG `pg_stat_activity` connection count |
| RabbitMQ | Competing consumers + quorum queue | 1-line queue declaration | `--scale analytics-worker=N` | `rabbitmq_queue_messages` depth |
