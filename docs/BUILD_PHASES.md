# Build Phases Guide

This document outlines the recommended build order for constructing the URL shortener system.

## Phase 1: Core Foundation (Week 1-2)

### Objective
Get a minimal working URL shortener with PostgreSQL storage.

### Tasks
1. **Database Schema Design**
   - URLs table (short_code, original_url, created_at, expires_at, click_count)
   - Analytics table (for future use)
   - Indexes for fast lookups

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

### Key Learning
- Go HTTP server patterns
- Database connection pooling
- RESTful API design

---

## Phase 2: CI & Test Automation (Week 2-3)

### Objective
Verify code quality and run tests automatically on every push and pull request.

### Tasks
1. **CI workflow**
   - Add a CI pipeline (GitHub Actions / GitLab CI) to run on push/PR.
   - Steps: `go fmt`, `go vet`, `golangci-lint`, `go test ./... -v` (unit + fast integration).
   - Optionally run heavyweight integration jobs (testcontainers) on a separate job or on protected branches.
2. **Test reliability**
   - Run DB migrations in CI for integration tests (use `scripts/migrate.sh` or `golang-migrate`).
   - Cache modules and Docker layers to speed builds.
3. **Automation housekeeping**
   - Add Makefile targets (`make test`, `make lint`, `make ci-integration`).
   - Add test badges to README and protect main branch to require green CI.

### Deliverables
- CI workflow file (e.g. `.github/workflows/ci.yml`)
- Linting and test jobs passing for PRs
- Fast/optional heavy integration job separation

### Notes
- Keep slow, Docker-dependent integration tests optional for PRs; run them on main or nightly builds.

## Phase 3: Build & Deployment (CD) (Week 3-4)

### Objective
Build container images and deploy to staging/production reliably.

### Tasks
1. **Build pipeline**
   - Build container images (`docker build`, `buildx`) and tag with commit SHA.
   - Push images to a registry (GHCR, ECR, GCR) on successful main branch builds.
2. **Migrations & deploy**
   - Run DB migrations as part of deployment (idempotent migration step) or a separate migration job with locking.
   - Deploy to staging (docker-compose, VM, or Kubernetes). Add manifests under `infrastructure/`.
3. **Release strategy & safety**
   - Implement rolling/canary deploys and health checks (readiness/liveness).
   - Add rollback steps and deployment gating (manual approvals for production).
4. **Secrets & observability**
   - Manage secrets securely (GitHub Secrets, Vault, SOPS) and add basic monitoring/health endpoints.

### Deliverables
- `.github/workflows/cd.yml` for build/push/deploy
- `infrastructure/` manifests or Helm charts
- `deploy`/`promote` scripts or Makefile targets

### Notes
- Use immutable image tags and run migrations safely before service rollout.
- Keep deployment steps idempotent and observable.


## Phase 2: Caching Layer (Week 3)

### Objective
Add Redis caching with cache-aside pattern.

### Tasks
1. **Redis Integration**
   - Connection pool setup
   - Cache-aside pattern implementation
   - TTL-based expiration

2. **Cache Strategies**
   - Read-through on cache miss
   - Write-through on URL creation
   - Cache invalidation on deletion

3. **Graceful Degradation**
   - Handle Redis connection failures
   - Fall back to PostgreSQL
   - Log cache failures for monitoring

### Deliverables
- Redis caching working
- Measurable latency improvement
- Cache hit/miss metrics

### Key Learning
- Redis data structures
- Caching patterns
- Handling distributed system failures

---

## Phase 3: Rust Rate Limiter (Week 4-5)

### Objective
Build a low-latency rate limiting service in Rust.

### Tasks
1. **Rate Limiting Algorithms**
   - Token bucket implementation
   - Sliding window counter (alternative)
   - Per-IP and per-API-key limits

2. **Service Interface**
   - HTTP endpoint for rate limit checks
   - gRPC interface (optional, for lower latency)
   - Bulk check support

3. **Gateway Integration**
   - Middleware for rate limit checks
   - Async rate limit updates
   - Fallback to local limiting

4. **Distributed State**
   - Redis-backed token counts
   - Atomic operations for consistency
   - State synchronization between instances

### Deliverables
- Working Rust rate limiter service
- Gateway integration
- Sub-millisecond response times

### Key Learning
- Rust async programming
- Service-to-service communication
- Distributed state management

---

## Phase 4: Async Processing with RabbitMQ (Week 6)

### Objective
Implement asynchronous cache updates and reliable message processing.

### Tasks
1. **RabbitMQ Setup**
   - Exchange and queue configuration
   - Dead letter queue for failures
   - Message durability settings

2. **Cache Update Worker**
   - Consume cache update events
   - Batch processing for efficiency
   - Retry logic with backoff

3. **Event Publishing**
   - Publish on URL creation/update/delete
   - Message schema design
   - Publisher confirms for reliability

4. **Error Handling**
   - Dead letter queue processing
   - Alert on DLQ growth
   - Manual retry capabilities

### Deliverables
- Async cache update pipeline
- Reliable message processing
- DLQ monitoring

### Key Learning
- Message queue patterns
- Eventual consistency
- Reliable message delivery

---

## Phase 5: Resilience Patterns (Week 7)

### Objective
Add circuit breakers, retries, and backpressure handling.

### Tasks
1. **Circuit Breakers**
   - Wrap PostgreSQL calls
   - Wrap Redis calls
   - Wrap Rate Limiter calls
   - Half-open state handling

2. **Retry Logic**
   - Exponential backoff with jitter
   - Configurable retry counts
   - Idempotency considerations

3. **Backpressure**
   - Request queue limits
   - Graceful rejection (429 responses)
   - Load shedding strategies

4. **Health Checks**
   - Liveness probe
   - Readiness probe
   - Dependency health aggregation

### Deliverables
- Resilient service communication
- Graceful degradation under failure
- Health check endpoints

### Key Learning
- Distributed systems resilience
- Failure isolation
- Graceful degradation

---

## Phase 6: Observability (Week 8)

### Objective
Add comprehensive monitoring, metrics, and tracing.

### Tasks
1. **Prometheus Metrics**
   - Request latency histograms
   - Cache hit/miss counters
   - Rate limit rejection counters
   - Circuit breaker state gauges

2. **Grafana Dashboards**
   - System overview dashboard
   - Per-service dashboards
   - Alert definitions

3. **Distributed Tracing**
   - OpenTelemetry integration
   - Trace context propagation
   - Jaeger/Zipkin setup

4. **Structured Logging**
   - JSON log format
   - Correlation IDs
   - Log aggregation (optional: ELK/Loki)

### Deliverables
- Complete observability stack
- Pre-built dashboards
- Alerting rules

### Key Learning
- Observability best practices
- Metrics-driven debugging
- Distributed tracing

---

## Phase 7: Testing & Chaos Engineering (Week 9-10)

### Objective
Validate system behavior under load and failure conditions.

### Tasks
1. **Load Testing**
   - k6 or Vegeta scripts
   - Gradual ramp-up tests
   - Spike testing
   - Endurance testing

2. **Chaos Testing**
   - Network partition simulation
   - Service failure injection
   - Latency injection
   - Resource exhaustion tests

3. **Integration Tests**
   - End-to-end API tests
   - Cross-service integration tests
   - Database migration tests

### Deliverables
- Load test suite
- Chaos test scenarios
- CI/CD integration

### Key Learning
- Performance testing
- Chaos engineering principles
- System reliability validation

---

## Phase 8: Frontend Dashboard (Week 11-12)

### Objective
Build real-time monitoring dashboard and chaos engineering panel.

### Tasks
1. **Real-time Dashboard**
   - System health overview
   - Live metrics visualization
   - URL analytics
   - WebSocket for real-time updates

2. **Chaos Engineering Panel**
   - Inject failures via UI
   - Monitor system response
   - Experiment history

3. **Admin Interface**
   - URL management
   - Rate limit configuration
   - User management (if applicable)

### Deliverables
- React/Next.js dashboard
- WebSocket integration
- Chaos panel

### Key Learning
- Real-time web applications
- Data visualization
- Full-stack integration
