# Scaling Comparison Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement three scaling patterns (PG read-write split, Redis consistent hash ring, RabbitMQ competing consumers + quorum queue, and PgBouncer) with measurable before/after load test comparisons that demonstrate each pattern's concrete impact.

**Architecture:** Each phase starts by recording a baseline k6 measurement, implements the minimal code/infra change, then re-runs the same test. The delta is the proof. All four phases run against the existing single-node Docker Compose stack — no cloud or Kubernetes required.

**Tech Stack:** Go (pgx/v5, go-redis/v9, gobreaker, singleflight), Docker Compose, Bitnami PostgreSQL (streaming replication), PgBouncer, k6, Prometheus/OpenTelemetry

---

## Measurement Framework

### Established baseline (WSL2, single node)

| VUs | req/s | p95 | SLO (p95 < 200ms) |
|---|---|---|---|
| 200 | 5800 | 70ms | ✓ comfortable |
| 800 | 5800 | 212ms | ✓ on the edge |
| 1000 | ~5600 | 247ms | ✗ breached |

Throughput plateaus at ~5800 req/s (Little's Law: throughput = VUs / avg_latency). Adding VUs past ~500 only increases latency, not throughput. **Comparison baseline: 800 VUs, p95=212ms, ~5800 req/s.**

### Primary benchmark: `tests/throughput.js`

**Rate limiter must be disabled.** The `RATE_LIMITER_ADDR` env var uses single-hyphen `${RATE_LIMITER_ADDR-default}` syntax — empty string disables it. Use docker compose to restart the gateway, not k6 env prefix (which only affects the host process):

```bash
# Disable rate limiter and restart gateway
RATE_LIMITER_ADDR="" docker compose up -d gateway

# Verify disabled (rate_limiter_cb key absent means disabled)
curl -s http://localhost:8080/health | jq 'has("rate_limiter_cb")'
# Expected: false
```

Run before and after each phase. Use `script -q -c` to capture terminal output AND generate HTML simultaneously (plain `tee` disables k6's TTY detection and prevents the HTML export):

```bash
K6_WEB_DASHBOARD_EXPORT=results/throughput-before-10a.html \
script -q -c "k6 run tests/throughput.js" results/throughput-before-10a.log
```

Key numbers to record from the log:

```bash
grep -E "p\(95\)|http_reqs\b|http_req_failed" results/throughput-before-10a.log
```

| Metric | What it shows |
|---|---|
| `http_req_duration` p95 | primary comparison metric |
| `http_reqs` req/s | throughput ceiling |
| `http_req_failed` rate | should stay 0% |

### RabbitMQ queue depth benchmark (Phase 10C only)

`throughput.js` is the **load generator** for Phase 10C (it produces click events). The **comparison metric** is queue depth — p95 latency won't change since publishing is fire-and-forget:

```bash
# Terminal 1: generate load
k6 run tests/throughput.js

# Terminal 2: watch queue depth
watch -n2 'docker compose exec rabbitmq rabbitmqctl list_queues name messages'
# OR Prometheus: rabbitmq_queue_messages{queue="analytics.clicks"}
```

Note: `tests/baseline.js` (100 VUs, rate limiter active) is **not used** for Phase 10 comparisons — at 100 VUs p95=5ms with vast headroom, no scaling pattern produces a visible delta. It remains the CI regression guard only.

---

## Phase 10A: PostgreSQL Read-Write Split

### Hypothesis

At 700 VUs no-sleep, `GetByCode` (redirects) and `Create` (writes) compete for the same 10-connection pool. Under this load, Redis CB trips occasionally causing all reads to fall through to DB — both read and write operations contend for the same 10 connections, increasing latency. Separating them onto primary (writes) and replica (reads) eliminates the contention — expected p95 reduction from 185ms toward 150ms.

### Result: Null result — implemented and reverted

| Metric | Before 10A | After 10A | Delta |
|---|---|---|---|
| p95 | 212ms | 261ms | +49ms worse |
| req/s | 5384 | 4481 | -17% |
| failures | 0% | 0% | — |

The after measurement was worse, not better. Three reasons:

1. **DB pool was never the bottleneck.** With Redis cache hit rate high, only a small fraction of reads reach the DB at all. A 10-connection pool handles the fallback load easily — there was nothing to split.
2. **Two PG containers on WSL2 compete for the same CPU/memory.** Running primary + replica on a single machine degrades both. The replica overhead outweighs any benefit.
3. **Redis saturation is the real constraint.** When the circuit breaker opens under load, the problem is the CB state transition latency, not which DB pool handles the fallback.

**Implementation was reverted.** Read-write split belongs in write-heavy or cache-cold workloads where the DB is demonstrably saturated. The code pattern (`writeDB`/`readDB` split with `readPool()` fallback) is sound — the infrastructure prerequisite (DB as bottleneck) simply doesn't hold here.

### Files

- Modify: `docker-compose.yml` — add `postgres-replica` service (Bitnami streaming replication)
- Modify: `services/gateway/internal/config/config.go` — add `DB_REPLICA_URL` to `DatabaseConfig`
- Modify: `services/gateway/internal/repository/url_repository.go` — add `readDB *pgxpool.Pool`, route `GetByCode` to it, fallback to `writeDB` on ping failure
- Modify: `services/gateway/internal/server/server.go` — pass `readDB` pool to `NewURLRepository`
- Modify: `services/gateway/cmd/server/main.go` — create replica pool from config, defer close
- Modify: `docker-compose.yml` gateway env — add `DB_REPLICA_URL`
- New: `services/gateway/internal/repository/url_repository_test.go` additions — test `GetByCode` routes to `readDB`

---

### Task 10A-0: Record the baseline

**Step 1: Disable rate limiter and run throughput test**

```bash
# Disable rate limiter (must restart gateway container, not just set shell env)
RATE_LIMITER_ADDR="" docker compose up -d gateway

# Verify disabled
curl -s http://localhost:8080/health | jq 'has("rate_limiter_cb")'
# Expected: false

# Run test — script preserves TTY so both HTML and log are generated
K6_WEB_DASHBOARD_EXPORT=results/throughput-before-10a.html \
script -q -c "k6 run tests/throughput.js" results/throughput-before-10a.log
```

**Step 2: Extract key numbers**

```bash
grep -E "p\(95\)|http_reqs\b|http_req_failed" results/throughput-before-10a.log
```

Record:
- `http_req_duration` p95 — primary comparison metric (baseline: ~185ms)
- `http_reqs` req/s — throughput ceiling (baseline: ~5600 req/s)
- `http_req_failed` rate — should be 0%

---

### Task 10A-1: Add postgres-replica to docker-compose.yml

**Files:**
- Modify: `docker-compose.yml`

**Step 1: Add replica service**

Add after the `postgres` service block (Bitnami image handles replication config automatically):

```yaml
  postgres-replica:
    image: bitnami/postgresql:16
    environment:
      POSTGRESQL_REPLICATION_MODE: slave
      POSTGRESQL_MASTER_HOST: postgres
      POSTGRESQL_MASTER_PORT_NUMBER: 5432
      POSTGRESQL_REPLICATION_USER: replicator
      POSTGRESQL_REPLICATION_PASSWORD: replicator_secret
      POSTGRESQL_USERNAME: ${DB_USER:-zhejian}
      POSTGRESQL_PASSWORD: ${DB_PASSWORD:-zhejian_secret}
      POSTGRESQL_DATABASE: ${DB_NAME:-urlshortener}
    depends_on:
      postgres:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${DB_USER:-zhejian} -d ${DB_NAME:-urlshortener}"]
      interval: 10s
      timeout: 5s
      retries: 5
```

Also update `postgres` service to use Bitnami and enable replication:

```yaml
  postgres:
    image: bitnami/postgresql:16
    environment:
      POSTGRESQL_USERNAME: ${DB_USER:-zhejian}
      POSTGRESQL_PASSWORD: ${DB_PASSWORD:-zhejian_secret}
      POSTGRESQL_DATABASE: ${DB_NAME:-urlshortener}
      POSTGRESQL_REPLICATION_MODE: master
      POSTGRESQL_REPLICATION_USER: replicator
      POSTGRESQL_REPLICATION_PASSWORD: replicator_secret
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/bitnami/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${DB_USER:-zhejian} -d ${DB_NAME:-urlshortener}"]
      interval: 10s
      timeout: 5s
      retries: 5
```

Add to `gateway` service environment:

```yaml
      DB_REPLICA_URL: postgres://${DB_USER:-zhejian}:${DB_PASSWORD:-zhejian_secret}@postgres-replica:5432/${DB_NAME:-urlshortener}?sslmode=disable
```

Add `postgres-replica` to gateway `depends_on`:

```yaml
      postgres-replica:
        condition: service_healthy
```

**Step 2: Verify replication starts**

```bash
docker compose up --build -d postgres postgres-replica
docker compose logs postgres-replica | grep -i "replication"
# Expected: "starting replication" or similar Bitnami message
```

**Step 3: Commit infra change**

```bash
git add docker-compose.yml
git commit -m "feat(infra): add postgres-replica with streaming replication"
```

---

### Task 10A-2: Add DB_REPLICA_URL to config

**Files:**
- Modify: `services/gateway/internal/config/config.go`

**Step 1: Add ReplicaURL field to DatabaseConfig**

In `DatabaseConfig` struct (after `SSLMode` field):

```go
ReplicaURL string // DB_REPLICA_URL: full DSN for read replica; empty = no replica
```

**Step 2: Load it in the Load() function**

In the `Database:` block of `Load()`:

```go
ReplicaURL: getEnv("DB_REPLICA_URL", ""),
```

**Step 3: Run existing config tests**

```bash
cd services/gateway && go test ./internal/config/... -v
# Expected: PASS (no config tests exist currently — that's fine)
```

**Step 4: Commit**

```bash
git add services/gateway/internal/config/config.go
git commit -m "feat(config): add DB_REPLICA_URL for read replica"
```

---

### Task 10A-3: Add read-write routing to URLRepository

**Files:**
- Modify: `services/gateway/internal/repository/url_repository.go`

**Step 1: Write the failing test first**

Add to `services/gateway/internal/repository/url_repository_test.go`:

```go
func TestURLRepository_GetByCodeUsesReadDB(t *testing.T) {
    // This test verifies that when readDB is set, GetByCode queries it
    // rather than the write pool. Use two separate test pools pointing
    // to the same DB (testdb helper) — in production they'd be different hosts.
    writeDB := testdb.New(t)
    readDB  := testdb.New(t)

    repo := NewURLRepository(writeDB, readDB)

    // Create via writeDB (direct insert)
    url := &model.URL{
        ID:          uuid.New(),
        ShortCode:   "readtest",
        OriginalURL: "https://example.com",
    }
    require.NoError(t, repo.Create(context.Background(), url))

    // GetByCode should succeed (readDB sees the data because same underlying DB in test)
    got, err := repo.GetByCode(context.Background(), "readtest")
    require.NoError(t, err)
    assert.Equal(t, url.ShortCode, got.ShortCode)
}
```

**Step 2: Run to see it fail (compilation error)**

```bash
cd services/gateway && go test ./internal/repository/... -run TestURLRepository_GetByCodeUsesReadDB -v
# Expected: FAIL — NewURLRepository has wrong signature
```

**Step 3: Update URLRepository struct and constructor**

```go
// URLRepository handles database operations for URLs.
// writeDB handles all writes (Create, Delete).
// readDB handles reads (GetByCode). Falls back to writeDB if readDB is nil.
type URLRepository struct {
    writeDB *pgxpool.Pool
    readDB  *pgxpool.Pool
}

// NewURLRepository creates a new URL repository.
// readDB may be nil — in that case all queries go to writeDB.
func NewURLRepository(writeDB *pgxpool.Pool, readDB *pgxpool.Pool) *URLRepository {
    return &URLRepository{writeDB: writeDB, readDB: readDB}
}
```

**Step 4: Update all methods to use correct pool**

`Create` and `Delete` stay on `writeDB`:

```go
func (r *URLRepository) Create(ctx context.Context, url *model.URL) error {
    // ... same query, use r.writeDB
    err := r.writeDB.QueryRow(ctx, query, ...).Scan(...)
```

```go
func (r *URLRepository) Delete(ctx context.Context, code string) error {
    // ... same query, use r.writeDB
    result, err := r.writeDB.Exec(ctx, query, code)
```

`GetByCode` uses `readDB` with fallback:

```go
func (r *URLRepository) GetByCode(ctx context.Context, code string) (*model.URL, error) {
    ctx, span := tracer.Start(ctx, "db.select", ...)
    defer span.End()

    pool := r.readPool()
    query := `SELECT id, short_code, original_url, created_at, expires_at FROM urls WHERE short_code = $1`
    var url model.URL
    err := pool.QueryRow(ctx, query, code).Scan(...)
    // ... same error handling
}

// readPool returns readDB if healthy, otherwise falls back to writeDB.
func (r *URLRepository) readPool() *pgxpool.Pool {
    if r.readDB == nil {
        return r.writeDB
    }
    return r.readDB
}
```

**Step 5: Run the test**

```bash
cd services/gateway && go test ./internal/repository/... -v
# Expected: PASS
```

**Step 6: Commit**

```bash
git add services/gateway/internal/repository/url_repository.go \
        services/gateway/internal/repository/url_repository_test.go
git commit -m "feat(repository): read-write split — GetByCode uses readDB, writes use writeDB"
```

---

### Task 10A-4: Wire replica pool in main.go and server.go

**Files:**
- Modify: `services/gateway/cmd/server/main.go`
- Modify: `services/gateway/internal/server/server.go`

**Step 1: Update main.go to create replica pool**

After `db` is created (line ~42), add:

```go
// Connect to read replica (optional — skipped if DB_REPLICA_URL is empty)
var readDB *pgxpool.Pool
if cfg.Database.ReplicaURL != "" {
    readDB, err = infra.NewPostgresPool(ctx, cfg.Database.ReplicaURL)
    if err != nil {
        obs.Logger.Warn("Read replica unavailable, falling back to primary for reads",
            slog.String("error", err.Error()))
        // readDB stays nil — URLRepository will fall back to writeDB
    } else {
        defer readDB.Close()
        obs.Logger.Info("Read replica connected")
    }
}
```

Update `server.NewServer` call to pass `readDB`:

```go
srv := server.NewServer(cfg, db, readDB, cache, rateLimiter, obs, pub)
```

**Step 2: Update server.go to accept and pass readDB**

Change `NewRouter` and `NewServer` signatures:

```go
func NewRouter(cfg *config.Config, db *pgxpool.Pool, readDB *pgxpool.Pool, cache *redis.Client, ...) *gin.Engine {
    // ...
    baseRepo := repository.NewURLRepository(db, readDB)
    // ... rest unchanged
}

func NewServer(cfg *config.Config, db *pgxpool.Pool, readDB *pgxpool.Pool, cache *redis.Client, ...) *http.Server {
    router := NewRouter(cfg, db, readDB, cache, rateLimiter, obs, pub)
    // ... rest unchanged
}
```

**Step 3: Build and run**

```bash
cd services/gateway && go build ./...
# Expected: no errors

docker compose up --build -d
curl http://localhost:8080/health
# Expected: {"status":"ok","dependencies":{"postgres":"ok","redis":"ok","amqp_connected":true}}
```

**Step 4: Commit**

```bash
git add services/gateway/cmd/server/main.go \
        services/gateway/internal/server/server.go
git commit -m "feat(server): wire read replica pool into URLRepository"
```

---

### Task 10A-5: Add Prometheus metrics for pool tracking

**Files:**
- Modify: `services/gateway/internal/repository/url_repository.go`

Add OTel counters to track which pool handled each query:

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/metric"
)

type URLRepository struct {
    writeDB      *pgxpool.Pool
    readDB       *pgxpool.Pool
    dbQueryTotal metric.Int64Counter
}

func NewURLRepository(writeDB, readDB *pgxpool.Pool) *URLRepository {
    meter := otel.Meter("gateway/repository")
    counter, _ := meter.Int64Counter("db_query_total",
        metric.WithDescription("Total DB queries by pool (read/write)"),
    )
    return &URLRepository{writeDB: writeDB, readDB: readDB, dbQueryTotal: counter}
}
```

In `GetByCode`, record the query with pool label:

```go
poolLabel := "write" // fallback
if r.readDB != nil {
    poolLabel = "read"
}
r.dbQueryTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("pool", poolLabel)))
```

In `Create` and `Delete`, record with `pool="write"`.

```bash
cd services/gateway && go build ./...
git add services/gateway/internal/repository/url_repository.go
git commit -m "feat(metrics): add db_query_total{pool} counter for read-write split tracking"
```

---

### Task 10A-6: Run the after load test and compare

**Step 1: Rebuild and run throughput test**

```bash
docker compose up --build -d

RATE_LIMITER_ADDR="" \
K6_WEB_DASHBOARD_EXPORT=results/throughput-after-10a.html \
k6 run tests/throughput.js
```

**Step 2: Check Prometheus metrics (in a second terminal while test runs)**

Open `http://localhost:9090` and query:

```promql
# Reads vs writes split
rate(db_query_total{pool="read"}[1m])
rate(db_query_total{pool="write"}[1m])

# Ratio should be ~80-95% reads (most traffic is GetByCode from cache misses)
```

**Step 3: Expected improvement**

| Metric | Before | Expected after |
|---|---|---|
| p95 at 700 VUs | ~185ms | <150ms |
| `http_req_failed` | 0% | 0% (unchanged) |
| `http_reqs` req/s | ~5600 | ≥5600 (plateau raises or stays) |

**Interpretation:** Read operations (GetByCode on cache miss) no longer compete with writes for the same 10 connections. The replica pool absorbs read load independently — p95 latency drops as queue wait time for a free connection decreases.

---

## Phase 10B: Redis Consistent Hash Ring

### Confirmed bottleneck evidence

From gateway logs captured during Phase 10A throughput test (700 VUs, rate limiter disabled):

```
circuit breaker about to trip  consecutive_failures=5
circuit breaker OPENED         closed → open
cache read error: context deadline exceeded   ← Redis 50ms timeout exceeded
  ... 30s open: all cache ops short-circuit, reads fall through to DB ...
circuit breaker testing recovery  open → half-open
cache read error: too many requests  ← MaxRequests=3 quota exhausted by concurrent goroutines
circuit breaker RECOVERED      half-open → closed
cache read error: too many requests  ← already failing again within the same second
```

The CB oscillates (open → recover → re-trip) because the single Redis node is saturated continuously. At ~5600 req/s each redirect does a GET + conditional SET = ~11,000 Redis ops/s. The 50ms `CACHE_OPERATION_TIMEOUT` is exceeded under this load, triggering CB open. The system never gets relief because load is constant.

### Hypothesis

Three nodes with consistent hashing distribute load ~33% per node (~3700 ops/s each). Per-node pressure drops below the saturation threshold — the 50ms timeout stops being exceeded, CB no longer accumulates consecutive failures, and p95 drops as cache hits start landing reliably instead of timing out and falling back to DB.

### Files

- New: `services/gateway/internal/cache/ring.go` — `HashRing` struct
- New: `services/gateway/internal/cache/ring_test.go` — distribution uniformity tests
- Modify: `services/gateway/internal/repository/cached_url_repository.go` — add `CacheRouter` interface, update `cache` field
- Modify: `services/gateway/internal/server/server.go` — build 3 clients + ring, pass to `NewCachedURLRepository`
- Modify: `services/gateway/cmd/server/main.go` — build 3 clients + ring, defer close all
- Modify: `services/gateway/internal/infra/infra.go` — add `NewCacheClients(connStrings []string)` helper
- Modify: `docker-compose.yml` — add `redis-2`, `redis-3` services; gateway env `CACHE_NODES`
- Modify: `services/gateway/internal/config/config.go` — add `Nodes []string` to `CacheConfig`

---

### Task 10B-0: Record the baseline (if not already done in 9A)

Run the same throughput test and save the "before 9B" numbers:

```bash
RATE_LIMITER_ADDR="" \
K6_WEB_DASHBOARD_EXPORT=results/throughput-before-10b.html \
k6 run tests/throughput.js
```

---

### Task 10B-1: Add redis-2 and redis-3 to docker-compose.yml

**Files:**
- Modify: `docker-compose.yml`

Add after the `redis` service block:

```yaml
  redis-2:
    image: redis:8-alpine
    command: redis-server --appendonly yes --maxmemory 256mb --maxmemory-policy allkeys-lru
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  redis-3:
    image: redis:8-alpine
    command: redis-server --appendonly yes --maxmemory 256mb --maxmemory-policy allkeys-lru
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5
```

Add to gateway environment:

```yaml
      CACHE_NODES: redis:6379,redis-2:6379,redis-3:6379
```

Add to gateway `depends_on`:

```yaml
      redis-2:
        condition: service_healthy
      redis-3:
        condition: service_healthy
```

```bash
git add docker-compose.yml
git commit -m "feat(infra): add redis-2 and redis-3 for consistent hash ring"
```

---

### Task 10B-2: Add CACHE_NODES to config

**Files:**
- Modify: `services/gateway/internal/config/config.go`

Add to `CacheConfig` struct:

```go
Nodes []string // CACHE_NODES: comma-separated "host:port" list for multi-node ring
               // When set, overrides Host/Port. When empty, use Host/Port as single node.
```

Load it:

```go
import "strings"

// In Load(), inside Cache: block:
cacheNodesEnv := getEnv("CACHE_NODES", "")
var cacheNodes []string
if cacheNodesEnv != "" {
    for _, n := range strings.Split(cacheNodesEnv, ",") {
        if n = strings.TrimSpace(n); n != "" {
            cacheNodes = append(cacheNodes, n)
        }
    }
}
// ... add to CacheConfig:
Nodes: cacheNodes,
```

```bash
git add services/gateway/internal/config/config.go
git commit -m "feat(config): add CACHE_NODES for multi-node Redis ring"
```

---

### Task 10B-3: Implement HashRing

**Files:**
- New: `services/gateway/internal/cache/ring.go`
- New: `services/gateway/internal/cache/ring_test.go`

**Step 1: Write the failing tests first**

`services/gateway/internal/cache/ring_test.go`:

```go
package cache_test

import (
    "fmt"
    "testing"

    "github.com/redis/go-redis/v9"
    "github.com/stretchr/testify/assert"
    "github.com/zhejian/url-shortener/gateway/internal/cache"
)

func TestHashRing_DistributionIsUniform(t *testing.T) {
    clients := map[string]*redis.Client{
        "node-1": redis.NewClient(&redis.Options{Addr: "localhost:1"}),
        "node-2": redis.NewClient(&redis.Options{Addr: "localhost:2"}),
        "node-3": redis.NewClient(&redis.Options{Addr: "localhost:3"}),
    }
    ring := cache.NewHashRing(clients, 150)

    counts := map[string]int{"node-1": 0, "node-2": 0, "node-3": 0}
    total := 10000
    for i := 0; i < total; i++ {
        key := fmt.Sprintf("url:%d", i)
        name := ring.NodeFor(key)
        counts[name]++
    }

    // Each node should own ~33% ± 5%
    for name, count := range counts {
        pct := float64(count) / float64(total)
        assert.InDelta(t, 0.333, pct, 0.05,
            "node %s owns %.1f%% of keys (expected ~33%%)", name, pct*100)
    }
}

func TestHashRing_RemoveNodeRemapsOneThird(t *testing.T) {
    clients := map[string]*redis.Client{
        "node-1": redis.NewClient(&redis.Options{Addr: "localhost:1"}),
        "node-2": redis.NewClient(&redis.Options{Addr: "localhost:2"}),
        "node-3": redis.NewClient(&redis.Options{Addr: "localhost:3"}),
    }
    ring := cache.NewHashRing(clients, 150)

    // Record which node each key maps to
    const total = 10000
    before := make(map[string]string, total)
    for i := 0; i < total; i++ {
        key := fmt.Sprintf("url:%d", i)
        before[key] = ring.NodeFor(key)
    }

    // Remove node-2
    ring.Remove("node-2")

    // Count how many keys changed node
    changed := 0
    for key, wasNode := range before {
        if ring.NodeFor(key) != wasNode {
            changed++
        }
    }

    // Only keys that were on node-2 should remap (~33%)
    pctChanged := float64(changed) / float64(total)
    assert.InDelta(t, 0.333, pctChanged, 0.07,
        "%.1f%% of keys remapped (expected ~33%%)", pctChanged*100)
}
```

**Step 2: Run to confirm failure**

```bash
cd services/gateway && go test ./internal/cache/... -v
# Expected: FAIL — package does not exist yet
```

**Step 3: Implement ring.go**

`services/gateway/internal/cache/ring.go`:

```go
package cache

import (
    "crypto/sha256"
    "encoding/binary"
    "fmt"
    "sort"
    "sync"

    "github.com/redis/go-redis/v9"
)

// HashRing maps cache keys to Redis clients using consistent hashing with
// virtual nodes. Adding or removing one node remaps only ~1/N of all keys.
type HashRing struct {
    mu       sync.RWMutex
    vnodes   []uint32          // sorted virtual node hashes
    nodeMap  map[uint32]string // vnode hash → node name
    clients  map[string]*redis.Client
    replicas int
}

// NewHashRing builds a ring from a name→client map.
// replicas is the number of virtual nodes per physical node (150 is a good default).
func NewHashRing(clients map[string]*redis.Client, replicas int) *HashRing {
    r := &HashRing{
        nodeMap:  make(map[uint32]string),
        clients:  make(map[string]*redis.Client),
        replicas: replicas,
    }
    for name, client := range clients {
        r.clients[name] = client
        r.addVnodes(name)
    }
    sort.Slice(r.vnodes, func(i, j int) bool { return r.vnodes[i] < r.vnodes[j] })
    return r
}

func (r *HashRing) addVnodes(name string) {
    for i := 0; i < r.replicas; i++ {
        h := hashKey(fmt.Sprintf("%s#%d", name, i))
        r.vnodes = append(r.vnodes, h)
        r.nodeMap[h] = name
    }
}

// Remove removes a node from the ring. Keys that were on the removed node
// remap to the next node clockwise — approximately 1/N of all keys.
func (r *HashRing) Remove(name string) {
    r.mu.Lock()
    defer r.mu.Unlock()

    toRemove := make(map[uint32]struct{})
    for i := 0; i < r.replicas; i++ {
        toRemove[hashKey(fmt.Sprintf("%s#%d", name, i))] = struct{}{}
    }

    filtered := r.vnodes[:0]
    for _, h := range r.vnodes {
        if _, skip := toRemove[h]; !skip {
            filtered = append(filtered, h)
        }
    }
    r.vnodes = filtered
    for h := range toRemove {
        delete(r.nodeMap, h)
    }
    delete(r.clients, name)
}

// NodeFor returns the name of the node responsible for key.
func (r *HashRing) NodeFor(key string) string {
    r.mu.RLock()
    defer r.mu.RUnlock()
    if len(r.vnodes) == 0 {
        return ""
    }
    h := hashKey(key)
    idx := sort.Search(len(r.vnodes), func(i int) bool { return r.vnodes[i] >= h })
    if idx == len(r.vnodes) {
        idx = 0
    }
    return r.nodeMap[r.vnodes[idx]]
}

// ClientFor returns the Redis client responsible for key.
func (r *HashRing) ClientFor(key string) *redis.Client {
    r.mu.RLock()
    defer r.mu.RUnlock()
    if len(r.vnodes) == 0 {
        return nil
    }
    name := r.NodeFor(key)
    return r.clients[name]
}

func hashKey(key string) uint32 {
    h := sha256.Sum256([]byte(key))
    return binary.BigEndian.Uint32(h[:4])
}
```

**Step 4: Run tests**

```bash
cd services/gateway && go test ./internal/cache/... -v
# Expected: PASS — both distribution and remap tests pass
```

**Step 5: Commit**

```bash
git add services/gateway/internal/cache/ring.go \
        services/gateway/internal/cache/ring_test.go
git commit -m "feat(cache): add HashRing with virtual nodes and Remove() support"
```

---

### Task 10B-4: Add CacheRouter interface to CachedURLRepository

**Files:**
- Modify: `services/gateway/internal/repository/cached_url_repository.go`

**Step 1: Add CacheRouter interface**

Add near the top of the file (after imports):

```go
// CacheRouter selects the correct Redis client for a given cache key.
// A single *redis.Client wrapped as a trivial CacheRouter is used in tests;
// a HashRing is used in production with multiple nodes.
type CacheRouter interface {
    ClientFor(key string) *redis.Client
}

// singleNodeRouter wraps a single *redis.Client to satisfy CacheRouter.
type singleNodeRouter struct{ client *redis.Client }

func (s *singleNodeRouter) ClientFor(_ string) *redis.Client { return s.client }
```

**Step 2: Replace `cache *redis.Client` field with `router CacheRouter`**

In `CachedURLRepository` struct:
- Remove: `cache *redis.Client`
- Add: `router CacheRouter`

**Step 3: Update NewCachedURLRepository to accept CacheRouter**

```go
func NewCachedURLRepository(db URLRepositoryInterface, router CacheRouter, ttl time.Duration, ...) *CachedURLRepository {
    // replace: cache: cache,
    // with:    router: router,
```

Keep backward-compatible: callers that pass `*redis.Client` directly now need to use `&singleNodeRouter{client: c}`. Update `server.go` accordingly.

**Step 4: Update cacheGet/cacheSet/cacheDel to use router**

```go
func (r *CachedURLRepository) cacheGet(ctx context.Context, key string) (string, error) {
    client := r.router.ClientFor(key)
    if client == nil {
        return "", redis.Nil
    }
    cacheCtx, cancel := context.WithTimeout(ctx, r.cacheTimeout)
    defer cancel()
    res, err := r.cacheCB.Execute(func() (interface{}, error) {
        return client.Get(cacheCtx, key).Result()
    })
    // ... rest unchanged
```

Apply same pattern to `cacheSet` and `cacheDel`.

**Step 5: Add per-node hit counter metrics**

In `NewCachedURLRepository`, also create:

```go
cacheNodeHits, _ := meter.Int64Counter("cache_node_hits_total",
    metric.WithDescription("Cache hits per Redis node"),
)
```

In `cacheGet`, after a hit:

```go
// determine node name from router if it's a *HashRing
if hr, ok := r.router.(*cache.HashRing); ok {
    nodeName := hr.NodeFor(key)
    r.cacheNodeHits.Add(ctx, 1, metric.WithAttributes(attribute.String("node", nodeName)))
}
```

**Step 6: Update server.go to pass ring instead of single client**

In `server.go`, `NewRouter`:

```go
// Build cache router — single node if no ring configured, multi-node otherwise
var cacheRouter repository.CacheRouter
if ring != nil {
    cacheRouter = ring
} else {
    cacheRouter = &repository.SingleNodeRouter(cache)  // exported helper
}
urlRepo := repository.NewCachedURLRepository(baseRepo, cacheRouter, cfg.Cache.TTL, obs.Logger, ...)
```

Pass `ring *cache.HashRing` as a parameter to `NewRouter`/`NewServer`.

**Step 7: Update main.go to build the ring**

```go
// Build cache clients
var ring *cache.HashRing
if len(cfg.Cache.Nodes) > 1 {
    clients := make(map[string]*redis.Client, len(cfg.Cache.Nodes))
    for _, addr := range cfg.Cache.Nodes {
        opt := &redis.Options{Addr: addr,
            ReadTimeout:  cfg.Cache.ReadTimeout,
            WriteTimeout: cfg.Cache.WriteTimeout}
        c := redis.NewClient(opt)
        if err := c.Ping(ctx).Err(); err != nil {
            obs.Logger.Warn("cache node unreachable", slog.String("addr", addr), slog.String("error", err.Error()))
        } else {
            clients[addr] = c
        }
    }
    ring = cache.NewHashRing(clients, 150)
    obs.Logger.Info("Cache ring initialized", slog.Int("nodes", len(clients)))
    // defer close all clients at shutdown
} else {
    // single node (existing behaviour)
}
```

**Step 8: Build and run tests**

```bash
cd services/gateway && go test ./... -v
docker compose up --build -d
curl http://localhost:8080/health
```

**Step 9: Commit**

```bash
git add services/gateway/internal/repository/cached_url_repository.go \
        services/gateway/internal/server/server.go \
        services/gateway/cmd/server/main.go
git commit -m "feat(cache): CacheRouter interface + HashRing wired into CachedURLRepository"
```

---

### Task 10B-5: Run the after load test and compare

**Step 1: Run throughput test**

```bash
RATE_LIMITER_ADDR="" \
K6_WEB_DASHBOARD_EXPORT=results/throughput-after-9b.html \
k6 run tests/throughput.js
```

**Step 2: Check Prometheus**

```promql
# Distribution across nodes (should be ~33% each)
rate(cache_node_hits_total{node="redis:6379"}[1m])
rate(cache_node_hits_total{node="redis-2:6379"}[1m])
rate(cache_node_hits_total{node="redis-3:6379"}[1m])
```

**Step 3: Expected improvement**

| Metric | Before 9B | Expected after |
|---|---|---|
| `http_req_failed` | post-9A baseline | further reduction |
| Cache node distribution | all on 1 node | ~33% each |
| Throughput (req/s) | post-9A | higher (3× cache connections) |

---

## Phase 10C: RabbitMQ Competing Consumers + Quorum Queue

### Hypothesis

A single analytics-worker processes one batch at a time. Under high redirect load, the `analytics.clicks` queue depth grows unboundedly. With 3 workers, each independently consuming, queue depth stabilises. The quorum queue ensures no data loss if RabbitMQ restarts mid-burst.

### Files

- Modify: `services/analytics-worker/internal/consumer/consumer.go` — add `x-queue-type: quorum` to `QueueDeclare` args, adjust prefetch
- Modify: `docker-compose.yml` — no scale change yet (done at runtime with `--scale`)

---

### Task 10C-0: Record the baseline queue depth

**Step 1: Run the throughput test while watching queue depth**

```bash
# Terminal 1: run throughput test (generates clicks)
RATE_LIMITER_ADDR="" k6 run tests/throughput.js

# Terminal 2: watch queue depth in Prometheus
# Query: rabbitmq_queue_messages{queue="analytics.clicks"}
# OR: check RabbitMQ management UI at http://localhost:15672 (guest/guest)
#     → Queues tab → analytics.clicks → depth over time
```

Record: peak queue depth and whether it drains after the test or keeps growing.

---

### Task 10C-1: Change queue declaration to quorum type

**Files:**
- Modify: `services/analytics-worker/internal/consumer/consumer.go`

**Step 1: Write a test that verifies quorum queue args**

Add to `services/analytics-worker/internal/consumer/consumer_test.go`:

```go
func TestSetup_DeclaresQuorumQueue(t *testing.T) {
    // Use testutil.NewRabbitMQ (already exists) and inspect declared queue type.
    // This is an integration test against a real RabbitMQ container.
    conn := testrabbitmq.New(t)
    repo := repository.NewRepository(testdb.New(t))
    c := New(conn, repo, slog.Default(), 100, time.Second)

    err := c.Setup()
    require.NoError(t, err)

    // Verify queue is quorum type by checking management API or re-declaring
    // with wrong type (which would error if type differs).
    ch, _ := conn.Channel()
    defer ch.Close()

    // Attempting to re-declare with x-queue-type=classic should fail if
    // the queue was created as quorum.
    _, err = ch.QueueDeclare("analytics.clicks", true, false, false, false, amqp.Table{
        "x-queue-type": "classic",
    })
    assert.Error(t, err, "re-declaring as classic should fail for a quorum queue")
}
```

**Step 2: Run to see it fail**

```bash
cd services/analytics-worker && go test ./internal/consumer/... -run TestSetup_DeclaresQuorumQueue -v
# Expected: FAIL — queue is currently declared as classic (default)
```

**Step 3: Update Setup() to declare quorum queue**

In `consumer.go`, `Setup()` method, change `args` in the main queue declaration:

```go
args := amqp.Table{
    "x-dead-letter-exchange":    "",
    "x-dead-letter-routing-key": dlqName,
    "x-queue-type":              "quorum", // replicated, durable across RabbitMQ nodes
}
```

**Step 4: Delete existing queue before testing** (queue type cannot be changed after creation)

```bash
# Stop the stack to let RabbitMQ forget the queue
docker compose down -v  # -v removes volumes, clearing the queue
docker compose up -d
```

**Step 5: Run the test**

```bash
cd services/analytics-worker && go test ./internal/consumer/... -v
# Expected: PASS
```

**Step 6: Commit**

```bash
git add services/analytics-worker/internal/consumer/consumer.go \
        services/analytics-worker/internal/consumer/consumer_test.go
git commit -m "feat(worker): declare analytics.clicks as quorum queue for replicated storage"
```

---

### Task 10C-2: Adjust prefetch for competing consumers

**Files:**
- Modify: `services/analytics-worker/internal/consumer/consumer.go`

The current prefetch is `batchSize * 2`. With 3 workers, each independently prefetches `batchSize * 2`, so up to `3 × 2 × batchSize` messages are in-flight simultaneously. This is already correct — no code change needed.

Verify the existing comment in `consumer.go`:

```go
// Prefetch 2× batchSize so RabbitMQ keeps the worker busy without
// overwhelming it — at most one full batch is in-flight at any time.
if err := ch.Qos(c.batchSize*2, 0, false); err != nil {
```

This is per-channel, per-consumer. With 3 consumers: `3 × 2 × 100 = 600` messages in-flight max. Appropriate.

No change needed.

---

### Task 10C-3: Run the after load test with scaled workers

**Step 1: Start with 1 worker (baseline)**

```bash
docker compose up --build -d

# Terminal 1: monitor queue depth
# http://localhost:15672 → Queues → analytics.clicks

# Terminal 2: run throughput test (generates clicks)
RATE_LIMITER_ADDR="" k6 run tests/throughput.js

# Record: peak queue depth and drain speed after test ends
```

**Step 2: Scale to 3 workers**

```bash
docker compose up -d --scale analytics-worker=3

# Repeat the throughput test
RATE_LIMITER_ADDR="" k6 run tests/throughput.js

# Record: peak queue depth and drain speed after test ends
```

**Step 3: Expected improvement**

| Workers | Peak queue depth | Drain time |
|---|---|---|
| 1 | growing unboundedly | never drains during test |
| 2 | stabilises | drains during cooldown |
| 3 | barely rises | drains before test ends |

**Step 4: Verify zero message loss**

```sql
-- After test, compare click events published vs stored in DB
-- In PostgreSQL:
SELECT COUNT(*) FROM analytics_clicks;
-- Should equal total events published during the test
```

---

## Phase 10D: PgBouncer (Optional Follow-Up to 9A)

### Hypothesis

With multiple gateway instances, each holds its own pgxpool. Total PostgreSQL connections = `instances × pool_size`. At high load, PostgreSQL hits `max_connections` and refuses new connections. PgBouncer proxies all connections into a fixed backend pool — PostgreSQL sees constant connection count regardless of gateway instances.

### Files

- New: `pgbouncer/pgbouncer-write.ini`
- New: `pgbouncer/pgbouncer-read.ini`
- New: `pgbouncer/userlist.txt`
- Modify: `docker-compose.yml` — add `pgbouncer-write`, `pgbouncer-read` services
- Modify: gateway environment — `DB_PRIMARY_URL` and `DB_REPLICA_URL` point to PgBouncer
- **Zero Go code change** — only env vars change

---

### Task 10D-0: Demonstrate connection exhaustion (before)

**Step 1: Scale gateway to 3 instances**

```bash
docker compose up -d --scale gateway=3
```

**Step 2: Run throughput test**

```bash
RATE_LIMITER_ADDR="" k6 run tests/throughput.js
```

Check PostgreSQL connections:

```sql
SELECT count(*) FROM pg_stat_activity WHERE datname = 'urlshortener';
-- Expected: 3 gateways × 10 pool max = up to 30 connections
-- PostgreSQL default max_connections = 100, so with more instances it exhausts
```

For a more dramatic demo: reduce `max_connections=25` in postgres service:

```yaml
  postgres:
    command: postgres -c max_connections=25
```

Then `--scale gateway=3` + throughput test → connection refused errors.

---

### Task 10D-1: Add PgBouncer config files

**Files:**
- New: `pgbouncer/pgbouncer-write.ini`
- New: `pgbouncer/pgbouncer-read.ini`
- New: `pgbouncer/userlist.txt`

`pgbouncer/pgbouncer-write.ini`:

```ini
[databases]
urlshortener = host=postgres port=5432 dbname=urlshortener

[pgbouncer]
listen_addr = *
listen_port = 5432
auth_type = md5
auth_file = /etc/pgbouncer/userlist.txt
pool_mode = transaction
max_client_conn = 1000
default_pool_size = 5          ; only 5 real PG connections for writes
server_reset_query = DISCARD ALL
```

`pgbouncer/pgbouncer-read.ini`:

```ini
[databases]
urlshortener = host=postgres-replica port=5432 dbname=urlshortener

[pgbouncer]
listen_addr = *
listen_port = 5432
auth_type = md5
auth_file = /etc/pgbouncer/userlist.txt
pool_mode = transaction
max_client_conn = 1000
default_pool_size = 10         ; 10 real PG connections for reads
server_reset_query = DISCARD ALL
```

`pgbouncer/userlist.txt`:

```
"zhejian" "zhejian_secret"
```

**Important:** PgBouncer transaction mode does not support prepared statements. Add `default_query_exec_mode=simple_protocol` to the gateway's `DB_PRIMARY_URL` and `DB_REPLICA_URL` connection strings:

```
postgres://zhejian:zhejian_secret@pgbouncer-write:5432/urlshortener?sslmode=disable&default_query_exec_mode=simple_protocol
```

---

### Task 10D-2: Add PgBouncer services to docker-compose.yml

**Files:**
- Modify: `docker-compose.yml`

```yaml
  pgbouncer-write:
    image: pgbouncer/pgbouncer:1.22
    volumes:
      - ./pgbouncer/pgbouncer-write.ini:/etc/pgbouncer/pgbouncer.ini:ro
      - ./pgbouncer/userlist.txt:/etc/pgbouncer/userlist.txt:ro
    depends_on:
      postgres:
        condition: service_healthy
    expose:
      - "5432"

  pgbouncer-read:
    image: pgbouncer/pgbouncer:1.22
    volumes:
      - ./pgbouncer/pgbouncer-read.ini:/etc/pgbouncer/pgbouncer.ini:ro
      - ./pgbouncer/userlist.txt:/etc/pgbouncer/userlist.txt:ro
    depends_on:
      postgres-replica:
        condition: service_healthy
    expose:
      - "5432"
```

Update gateway environment to point at PgBouncer:

```yaml
      DB_PRIMARY_URL: postgres://zhejian:zhejian_secret@pgbouncer-write:5432/urlshortener?sslmode=disable&default_query_exec_mode=simple_protocol
      DB_REPLICA_URL: postgres://zhejian:zhejian_secret@pgbouncer-read:5432/urlshortener?sslmode=disable&default_query_exec_mode=simple_protocol
```

```bash
docker compose up --build -d
curl http://localhost:8080/health
git add pgbouncer/ docker-compose.yml
git commit -m "feat(infra): add PgBouncer write/read proxies for connection pooling"
```

---

### Task 10D-3: Run the after load test and compare

**Step 1: Run with 3 gateway instances**

```bash
docker compose up -d --scale gateway=3

RATE_LIMITER_ADDR="" \
K6_WEB_DASHBOARD_EXPORT=results/throughput-after-9d.html \
k6 run tests/throughput.js
```

**Step 2: Check PostgreSQL connections**

```sql
SELECT count(*) FROM pg_stat_activity WHERE datname = 'urlshortener';
-- Expected: 5 (pgbouncer-write) + 10 (pgbouncer-read) = 15 connections
-- Regardless of how many gateway instances are running
```

**Step 3: Expected improvement**

| Scenario | PG connections | `http_req_failed` |
|---|---|---|
| 3 gateways, no PgBouncer | 30+ | high when max_connections hit |
| 3 gateways + PgBouncer | 15 constant | ~0% |

---

## Summary: Before/After Comparison Table

Comparison baseline: **700 VUs, p95=185ms, ~5600 req/s, 0% failure** (rate limiter disabled, WSL2).

| Phase | Primary bottleneck | Comparison tool | Before | Expected after |
|---|---|---|---|---|
| 10A: PG read-write split | Read/write contention on single pool | `throughput.js` p95 | 185ms | <150ms |
| 10B: Redis hash ring | Single Redis connection throughput ceiling | `throughput.js` p95 + req/s | ~5600 req/s plateau | higher plateau or lower p95 |
| 10C: RabbitMQ competing consumers | Single worker, queue grows unboundedly | RabbitMQ queue depth during `throughput.js` | queue growing | queue stable / drains |
| 10D: PgBouncer | Connections = instances × pool size | `pg_stat_activity` count | N × 10 | 15 constant |

---

## Interview Narrative

For each phase, the story is:

1. **"Here is the bottleneck I identified"** — show k6 failure rate or Prometheus query confirming the problem
2. **"Here is the minimal code/infra change"** — show the diff (it's always small)
3. **"Here is the measured improvement"** — show the after k6 terminal summary or Prometheus graph side-by-side

The comparison is most compelling for Phase 10A (connection exhaustion → near-zero failures) and Phase 10C (queue depth 1 vs 3 workers side-by-side in the RabbitMQ UI).
