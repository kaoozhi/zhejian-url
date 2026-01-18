.PHONY: all build run test clean docker-up docker-down lint fmt help

# Default target
all: help

# =============================================================================
# Development
# =============================================================================

## Start all infrastructure services
infra-up:
	docker-compose up -d postgres redis rabbitmq

## Stop all infrastructure services
infra-down:
	docker-compose down

## Start full stack (infrastructure + observability)
stack-up:
	docker-compose up -d

## Stop full stack
stack-down:
	docker-compose down -v

# =============================================================================
# Go Gateway
# =============================================================================

## Build the Go gateway
gateway-build:
	cd services/gateway && go build -o bin/gateway cmd/server/main.go

## Run the Go gateway
gateway-run:
	cd services/gateway && go run cmd/server/main.go

## Run Go tests
gateway-test:
	cd services/gateway && go test -v ./...

## Format Go code
gateway-fmt:
	cd services/gateway && go fmt ./...

## Lint Go code
gateway-lint:
	cd services/gateway && golangci-lint run

# =============================================================================
# Rust Rate Limiter
# =============================================================================

## Build the Rust rate limiter
limiter-build:
	cd services/rate-limiter && cargo build --release

## Run the Rust rate limiter
limiter-run:
	cd services/rate-limiter && cargo run

## Run Rust tests
limiter-test:
	cd services/rate-limiter && cargo test

## Format Rust code
limiter-fmt:
	cd services/rate-limiter && cargo fmt

## Lint Rust code
limiter-lint:
	cd services/rate-limiter && cargo clippy

# =============================================================================
# Docker
# =============================================================================

## Build all Docker images
docker-build:
	docker-compose build

## Build gateway Docker image
docker-build-gateway:
	docker build -t zhejian-gateway ./services/gateway

## Build rate limiter Docker image
docker-build-limiter:
	docker build -t zhejian-rate-limiter ./services/rate-limiter

# =============================================================================
# Database
# =============================================================================

## Setup database (run migrations)
db-setup:
	./scripts/migrate.sh

## Connect to database
db-connect:
	PGPASSWORD=zhejian_secret psql -h localhost -p 5434 -U zhejian -d urlshortener

# =============================================================================
# Testing
# =============================================================================

## Run load tests
load-test:
	cd testing/load && k6 run load-test.js

## Run chaos tests
chaos-test:
	cd testing/chaos && ./run-chaos.sh

## Run integration tests
integration-test:
	cd testing/integration && go test -v ./...

# =============================================================================
# Utilities
# =============================================================================

## Clean build artifacts
clean:
	cd services/gateway && rm -rf bin/
	cd services/rate-limiter && cargo clean

## Show logs for all services
logs:
	docker-compose logs -f

## Show logs for gateway
logs-gateway:
	docker-compose logs -f gateway

## Show logs for rate limiter
logs-limiter:
	docker-compose logs -f rate-limiter

## Open Grafana dashboard
open-grafana:
	open http://localhost:3001

## Open RabbitMQ management
open-rabbitmq:
	open http://localhost:15672

## Open Jaeger UI
open-jaeger:
	open http://localhost:16686

# =============================================================================
# Help
# =============================================================================

## Show this help message
help:
	@echo "Zhejian URL Shortener - Available Commands"
	@echo ""
	@echo "Development:"
	@echo "  make infra-up        Start infrastructure services"
	@echo "  make infra-down      Stop infrastructure services"
	@echo "  make stack-up        Start full stack"
	@echo "  make stack-down      Stop full stack"
	@echo ""
	@echo "Go Gateway:"
	@echo "  make gateway-build   Build the gateway"
	@echo "  make gateway-run     Run the gateway"
	@echo "  make gateway-test    Run tests"
	@echo ""
	@echo "Rust Rate Limiter:"
	@echo "  make limiter-build   Build the rate limiter"
	@echo "  make limiter-run     Run the rate limiter"
	@echo "  make limiter-test    Run tests"
	@echo ""
	@echo "Database:"
	@echo "  make db-migrate      Run migrations"
	@echo "  make db-reset        Reset database"
	@echo "  make db-connect      Connect to psql"
	@echo ""
	@echo "Testing:"
	@echo "  make load-test       Run load tests"
	@echo "  make chaos-test      Run chaos tests"
