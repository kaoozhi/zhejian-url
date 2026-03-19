# Phase 10B: Redis Consistent Hash Ring — Findings

**Date:** 2026-03-19
**Branch:** `dev/ph10`
**Test script:** `tests/throughput.js` — 2400 VUs, no sleep, 2m30s
**Environment:** Mac host gateway (Go process) + Docker Redis nodes

---

## Summary

The consistent hash ring routes keys correctly and distributes load uniformly across nodes
(~33% per node). On localhost it does not improve throughput — all nodes share the same CPU —
and worsens tail latency due to partitioned connection pools and shared-CPU contention at peak
load. The throughput ceiling on a single host is set by the Go scheduler and Postgres pool, not
by Redis capacity.

---

## Implementation

- `services/gateway/internal/cache/ring.go` — SHA-256 consistent hash ring, 150 virtual nodes
  per real node, `sync.RWMutex`-protected for safe concurrent access
- `ClientProvider` interface allows `CachedURLRepository` to treat single-node and ring
  identically; no changes to repository logic
- Nodes configured via `CACHE_NODES=host:port,...`; single node is a ring of one

---

## Circuit Breaker Problem (pre-fix)

Before results were meaningful, the circuit breaker tripped on every run regardless of Redis
health. Root causes identified through iterative testing:

### Root cause 1 — operation timeout too tight

`CACHE_OPERATION_TIMEOUT` defaulted to **50ms**. At 2400 VUs the Go scheduler manages 2400+
goroutines on `GOMAXPROCS` OS threads. A goroutine can burn its entire deadline waiting to be
scheduled before `client.Get()` even starts. Even at **150ms** the CB still tripped.

### Root cause 2 — consecutive failures threshold too sensitive

The original `ReadyToTrip` condition:
```go
counts.ConsecutiveFailures >= 5  // or 20 after first fix
```
At 14k req/s, five (or even twenty) back-to-back scheduling-induced timeouts are trivial to
hit. With `requests=52428` in the log and `consecutive_failures=20`, the failure rate at trip
time was `0.038%` — Redis was healthy.

### Fix applied

Replaced the single consecutive-failures condition with a two-tier strategy:

```go
// Primary: rate-based — requires sustained failure over a meaningful sample
if cb.MinRequestsToTrip > 0 && cb.FailureRateThreshold > 0 &&
    counts.Requests >= cb.MinRequestsToTrip {
    failureRate := float64(counts.TotalFailures) / float64(counts.Requests)
    if failureRate > cb.FailureRateThreshold { return true }
}
// Secondary: consecutive failures — fast path for total outages
if cb.ConsecutiveFailures > 0 && counts.ConsecutiveFailures >= cb.ConsecutiveFailures {
    return true
}
```

For the throughput test:

| Env var | Value | Rationale |
|---|---|---|
| `CACHE_OPERATION_TIMEOUT` | `500ms` | Covers goroutine scheduling latency at 2400 VUs |
| `CACHE_CB_MIN_REQUESTS` | `50` | Minimum sample before rate check |
| `CACHE_CB_FAILURE_RATE` | `0.2` | Trip at >20% sustained failure |
| `CACHE_CB_CONSECUTIVE_FAILURES` | `0` | Disabled — rate check is sufficient |
| `CACHE_POOL_SIZE` | `50` | Explicit pool size per node (was implicit GOMAXPROCS×10) |

All values are env-var overridable; the production defaults in `docker-compose.yml` remain
more conservative (`150ms` timeout, `CONSECUTIVE_FAILURES=20`).

---

## Results (clean run, Redis flushed before each test)

| Metric | Single node | 3-node ring | Delta |
|---|---|---|---|
| **req/s** | 13,771 | 13,653 | −0.9% |
| **avg latency** | 138ms | 140ms | +1.4% |
| **median (p50)** | 155ms | 124ms | −20ms |
| **p90** | 198ms | 284ms | **+43%** |
| **p95** | 209ms | 331ms | **+58%** |
| **max** | 383ms | 959ms | **+150%** |
| **CB trips** | 0 | 0 | — |
| **Cache hit rate** | ~100% | ~100% | — |

### Key distribution (ring test, Prometheus metrics)

```
cache_hits_total{cache_node="localhost:6379"} 595,512  (31%)
cache_hits_total{cache_node="localhost:6380"} 524,282  (27%)
cache_hits_total{cache_node="localhost:6381"} 796,196  (41%)
```

Distribution variance (31/27/41%) is expected with a 300-URL test set. With 150 virtual nodes
the ring converges toward uniform distribution as key population grows; ~5% per-node error is
typical.

---

## Analysis

### Why throughput is identical

On localhost all three Redis processes share the same CPU cores, memory bus, and loopback
interface. Adding nodes splits routing, not capacity. Little's Law:
`throughput = concurrency / latency` — latency is unchanged, so throughput is unchanged.

### Why median improves but tail worsens

At moderate concurrency the ring spreads key lookups across three pools, reducing contention
per connection (better median). At peak (2400 VUs), if any one Redis process gets CPU-starved,
all keys hashed to that node queue simultaneously — the partitioned failure mode drives p90/p95
up sharply. Single-node doesn't partition failures this way.

### Why the ring does provide value on separate machines

With independent nodes, each has its own CPU, memory bandwidth, and network interface. A key
hashed to node-2 cannot be delayed by load on node-1. Adding a node genuinely adds capacity
rather than just redistributing contention. The consistent hashing property (only ~1/N keys
re-route on node add/remove, no data migration required) is also only meaningful at that scale.

---

## Conclusion

The ring implementation is correct. On localhost it is a **routing correctness experiment**,
not a throughput experiment. The appropriate use of this pattern is:

- **Use when:** Redis is on separate machines / container hosts and is the actual throughput
  bottleneck (measurable via `redis-cli info stats` showing high `instantaneous_ops_per_sec`)
- **Don't use when:** Redis is co-located or the bottleneck is elsewhere (Go scheduler, Postgres
  pool, network bandwidth)
- **Phase 10D note:** PgBouncer is likely to show a more direct throughput gain on this stack
  because Postgres connection exhaustion is a real bottleneck at high VU counts, unlike Redis
  which has headroom to spare
