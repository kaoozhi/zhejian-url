# Getting Started Guide

## Prerequisites

- Go 1.21+
- Rust 1.75+
- Docker & Docker Compose
- Node.js 20+ (for frontend)

## Quick Start

```bash
# Start infrastructure services
docker-compose up -d postgres redis rabbitmq

# Start the gateway (development mode)
cd services/gateway && go run cmd/server/main.go

# Start the rate limiter (development mode)
cd services/rate-limiter && cargo run
```

## Development Workflow

1. Start infrastructure: `docker-compose up -d`
2. Run services individually for development
3. Use `make dev` for hot-reload development

## Environment Variables

See `.env.example` for required environment variables.
