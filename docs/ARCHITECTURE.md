# Architecture Deep Dive

## System Design Principles

### 1. Fault Tolerance
- Circuit breakers prevent cascade failures
- Exponential backoff with jitter for retries
- Dead letter queues for failed message processing
- Graceful degradation when dependencies fail

### 2. Scalability
- Stateless API gateway enables horizontal scaling
- Distributed rate limiting across multiple instances
- Read replicas for PostgreSQL (future)
- Redis cluster mode support (future)

### 3. Consistency Model
- PostgreSQL is the source of truth
- Redis provides eventual consistency for reads
- RabbitMQ ensures reliable cache invalidation

## Component Details

### Go API Gateway

**Responsibilities:**
- HTTP request routing and validation
- Authentication/Authorization
- Request rate limiting (via Rust service)
- Circuit breaking for downstream services
- Backpressure management
- Request tracing and metrics

**Key Packages:**
- `gin` - HTTP router
- `sony/gobreaker` - Circuit breaker
- `golang.org/x/time/rate` - Local rate limiting fallback
- `opentelemetry-go` - Distributed tracing

### Rust Rate Limiter

**Responsibilities:**
- High-performance distributed rate limiting
- Token bucket / Sliding window algorithms
- Sub-millisecond response times
- Redis-backed state synchronization

**Key Crates:**
- `axum` or `actix-web` - HTTP framework
- `tonic` - gRPC (optional)
- `redis` - Redis client
- `tracing` - Observability

### Cache Strategy

```
┌─────────────┐     Cache Miss     ┌─────────────┐
│   Request   │ ─────────────────► │  PostgreSQL │
│             │                    │             │
│   (Redis)   │ ◄───────────────── │  (Write)    │
└─────────────┘     Populate       └─────────────┘
       │                                  │
       │                                  │
       ▼                                  ▼
┌─────────────────────────────────────────────────┐
│                   RabbitMQ                       │
│  - Cache invalidation events                     │
│  - Async cache warming                           │
│  - Failed update retries                         │
└─────────────────────────────────────────────────┘
```

### Message Flow

1. **URL Creation:**
   - Write to PostgreSQL
   - Publish cache update event to RabbitMQ
   - Worker consumes and updates Redis

2. **URL Redirect:**
   - Check Redis cache first
   - On miss, query PostgreSQL
   - Async populate cache via RabbitMQ

3. **Cache Invalidation:**
   - TTL-based expiration
   - Event-driven invalidation via RabbitMQ
   - Manual invalidation API

## Failure Scenarios

| Scenario | Behavior |
|----------|----------|
| Redis down | Fall back to PostgreSQL |
| PostgreSQL down | Return cached data if available, otherwise 503 |
| RabbitMQ down | Synchronous cache updates, queue recovery |
| Rate limiter down | Local rate limiting fallback |

## Metrics & Observability

### Key Metrics
- Request latency (p50, p95, p99)
- Cache hit/miss ratio
- Rate limit rejections
- Circuit breaker state changes
- Queue depth and processing time

### Tracing
- End-to-end request tracing with OpenTelemetry
- Span correlation across services
- Error tracking and debugging
