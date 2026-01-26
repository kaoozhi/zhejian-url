[![Go](https://github.com/kaoozhi/zhejian-url/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/kaoozhi/zhejian-url/actions/workflows/go.yml)
[![Docker Compose](https://github.com/kaoozhi/zhejian-url/actions/workflows/production.yml/badge.svg)](https://github.com/kaoozhi/zhejian-url/actions/workflows/production.yml)
[![codecov](https://codecov.io/gh/kaoozhi/zhejian-url/branch/main/graph/badge.svg?token=XPQ70FTF7M)](https://codecov.io/gh/kaoozhi/zhejian-url)

# Zhejian URL Shortener

A production-grade URL shortener built to showcase high-concurrency backend engineering and distributed systems design.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Load Balancer                                   │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Go API Gateway                                       │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │   Router    │  │ Backpressure│  │   Circuit   │  │   Request   │        │
│  │             │  │   Control   │  │   Breaker   │  │   Tracing   │        │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘        │
└─────────────────────────────────────────────────────────────────────────────┘
         │                    │                    │
         ▼                    ▼                    ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│  Rust Rate      │  │    Redis        │  │   PostgreSQL    │
│  Limiter        │  │    Cache        │  │   (Primary)     │
│  (gRPC/HTTP)    │  │                 │  │                 │
└─────────────────┘  └─────────────────┘  └─────────────────┘
                              │                    │
                              └────────┬───────────┘
                                       ▼
                     ┌─────────────────────────────────┐
                     │          RabbitMQ               │
                     │  ┌─────────┐  ┌─────────────┐   │
                     │  │ Cache   │  │ Dead Letter │   │
                     │  │ Updates │  │   Queue     │   │
                     │  └─────────┘  └─────────────┘   │
                     └─────────────────────────────────┘
                                       │
                     ┌─────────────────┴─────────────────┐
                     ▼                                   ▼
         ┌─────────────────┐                 ┌─────────────────┐
         │   Prometheus    │                 │   OpenTelemetry │
         │   + Grafana     │                 │   Collector     │
         └─────────────────┘                 └─────────────────┘
```

## Project Structure

```
zhejian-url/
├── services/
│   ├── gateway/              # Go API Gateway
│   ├── rate-limiter/         # Rust Rate Limiter
│   └── cache-worker/         # Go/Rust Cache Update Worker
├── frontend/
│   ├── dashboard/            # Real-time Dashboard (React/Next.js)
│   └── chaos-panel/          # Chaos Engineering Panel
├── infrastructure/
│   ├── docker/               # Docker configurations
│   ├── kubernetes/           # K8s manifests (optional)
│   └── terraform/            # IaC (optional)
├── observability/
│   ├── prometheus/           # Prometheus configs
│   ├── grafana/              # Grafana dashboards
│   └── otel/                 # OpenTelemetry configs
├── testing/
│   ├── load/                 # Load testing (k6, vegeta)
│   ├── chaos/                # Chaos testing scripts
│   └── integration/          # Integration tests
├── scripts/                  # Build and utility scripts
├── docs/                     # Documentation
└── docker-compose.yml        # Local development setup
```

## Technology Stack

| Component | Technology | Purpose |
|-----------|------------|---------|
| API Gateway | Go (Fiber/Gin) | Request routing, backpressure, circuit breaking |
| Rate Limiter | Rust (Actix/Axum) | Low-latency distributed rate limiting |
| Primary DB | PostgreSQL | Authoritative URL storage |
| Cache | Redis | Read-optimized caching |
| Message Broker | RabbitMQ | Async cache updates, retries |
| Metrics | Prometheus | System metrics collection |
| Dashboards | Grafana | Visualization |
| Tracing | OpenTelemetry + Jaeger | Distributed tracing |
| Load Testing | k6/Vegeta | Synthetic traffic generation |
| Chaos Testing | Custom + Toxiproxy | Fault injection |

## Getting Started

See [docs/GETTING_STARTED.md](docs/GETTING_STARTED.md) for detailed instructions.

## Build Order (Recommended)

1. **Phase 1: Core Foundation** - PostgreSQL schema + Go Gateway basics
2. **Phase 2: Caching Layer** - Redis integration + Cache-aside pattern
3. **Phase 3: Rate Limiter** - Rust service + gRPC integration
4. **Phase 4: Async Processing** - RabbitMQ + Cache workers
5. **Phase 5: Resilience** - Circuit breakers, retries, backpressure
6. **Phase 6: Observability** - Prometheus, Grafana, OpenTelemetry
7. **Phase 7: Testing** - Load testing, chaos engineering
8. **Phase 8: Frontend** - Dashboard + Chaos panel

## License

MIT
