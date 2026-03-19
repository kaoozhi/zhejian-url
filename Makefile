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

## Run baseline load test with k6 web dashboard (100 VUs, 9 min)
load-baseline:
	K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/baseline-report.html \
	k6 run tests/baseline.js

## Run spike load test (50 → 300 → 50 VUs, ~5 min)
load-spike:
	K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/spike-report.html \
	k6 run tests/spike.js

## Run endurance load test (100 VUs, 12 min — local only)
load-endurance:
	K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/endurance-report.html \
	k6 run tests/endurance.js

## Run throughput ceiling test — RATE_LIMITER_ADDR="" make load-throughput
load-throughput:
	K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-report.html \
	k6 run tests/throughput.js

## Phase 10B: single-node baseline — restart gateway with one Redis node first:
##   CACHE_NODES=redis-1:6379 docker compose up -d gateway
load-throughput-single:
	RATE_LIMITER_ADDR="" AMQP_URL="" CACHE_NODES=redis-1:6379 docker compose up -d gateway
	@until curl -sf http://localhost:8080/health > /dev/null 2>&1; do sleep 1; done
	mkdir -p results
	@set -e; \
	if script --version > /dev/null 2>&1; then \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-single.html \
		script -q -c "k6 run tests/throughput.js" results/throughput-single.log; \
	else \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-single.html \
		script -q results/throughput-single.log k6 run tests/throughput.js; \
	fi

## Phase 10B: host gateway + single Redis node (docker infra only)
## Starts postgres/migrations/redis-1 in Docker, runs gateway on host, then runs k6.
load-throughput-single-host:
	docker compose up -d postgres migrations redis-1
	docker compose stop gateway || true
	@existing_pid=$$(lsof -tiTCP:8080 -sTCP:LISTEN || true); \
	if [ -n "$$existing_pid" ]; then \
		echo "Stopping existing listener on :8080 (PID $$existing_pid)"; \
		kill $$existing_pid || true; \
		sleep 1; \
	fi
	@echo "Starting host gateway in background terminal (logs: services/gateway/host-gateway.log)"
	@cd services/gateway && \
		DB_HOST=localhost DB_PORT=5432 CACHE_NODES=localhost:6379 RATE_LIMITER_ADDR='' AMQP_URL='' CACHE_OPERATION_TIMEOUT=150ms \
		go run cmd/server/main.go > host-gateway-single.log 2>&1 & \
		echo $$! > host-gateway.pid
	@until curl -sf http://localhost:8080/health > /dev/null 2>&1; do sleep 1; done
	mkdir -p results
	@set -e; \
	if script --version > /dev/null 2>&1; then \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-single-host.html \
		script -q -c "k6 run tests/throughput.js" results/throughput-single-host.log; \
	else \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-single-host.html \
		script -q results/throughput-single-host.log k6 run tests/throughput.js; \
	fi
	@echo "Host gateway running with PID $$(cat services/gateway/host-gateway.pid)."
	@echo "Stop with: kill $$(cat services/gateway/host-gateway.pid)"

## Phase 10B: 3-node ring measurement — restart gateway with full ring first:
##   docker compose up -d gateway
load-throughput-ring:
	RATE_LIMITER_ADDR="" docker compose up -d gateway
	@until curl -sf http://localhost:8080/health > /dev/null 2>&1; do sleep 1; done
	mkdir -p results
	@set -e; \
	if script --version > /dev/null 2>&1; then \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-ring.html \
		script -q -c "k6 run tests/throughput.js" results/throughput-ring.log; \
	else \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-ring.html \
		script -q results/throughput-ring.log k6 run tests/throughput.js; \
	fi

## Phase 10B: host gateway + 3-node Redis ring (docker infra only)
## Uses host-mapped Redis ports: 6379 (redis-1), 6380 (redis-2), 6381 (redis-3).
load-throughput-ring-host:
	docker compose up -d postgres migrations redis-1 redis-2 redis-3
	docker compose stop gateway || true
	@existing_pid=$$(lsof -tiTCP:8080 -sTCP:LISTEN || true); \
	if [ -n "$$existing_pid" ]; then \
		echo "Stopping existing listener on :8080 (PID $$existing_pid)"; \
		kill $$existing_pid || true; \
		sleep 1; \
	fi
	@echo "Starting host gateway in background terminal (logs: services/gateway/host-gateway.log)"
	@cd services/gateway && \
		DB_HOST=localhost DB_PORT=5432 CACHE_NODES=localhost:6379,localhost:6380,localhost:6381 RATE_LIMITER_ADDR='' AMQP_URL='' CACHE_OPERATION_TIMEOUT=150ms \
		go run cmd/server/main.go > host-gateway-ring.log 2>&1 & \
		echo $$! > host-gateway.pid
	@until curl -sf http://localhost:8080/health > /dev/null 2>&1; do sleep 1; done
	mkdir -p results
	@set -e; \
	if script --version > /dev/null 2>&1; then \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-ring-host.html \
		script -q -c "k6 run tests/throughput.js" results/throughput-ring-host.log; \
	else \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-ring-host.html \
		script -q results/throughput-ring-host.log k6 run tests/throughput.js; \
	fi
	@echo "Host gateway running with PID $$(cat services/gateway/host-gateway.pid)."
	@echo "Stop with: kill $$(cat services/gateway/host-gateway.pid)"

## Phase 11: hot-key stress, single CB (before) — 20 URLs, CB should trip
load-hotkey-single-cb:
	RATE_LIMITER_ADDR="" docker compose up -d gateway
	@until curl -sf http://localhost:8080/health > /dev/null 2>&1; do sleep 1; done
	mkdir -p results
	@set -e; \
	if script --version > /dev/null 2>&1; then \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/hotkey-single-cb.html \
		script -q -c "k6 run tests/hotkey.js" results/hotkey-single-cb.log; \
	else \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/hotkey-single-cb.html \
		script -q results/hotkey-single-cb.log k6 run tests/hotkey.js; \
	fi

## Phase 11: hot-key stress, per-node CB (after) — same 20-URL load, only hot node trips
load-hotkey-per-node-cb:
	RATE_LIMITER_ADDR="" docker compose up -d gateway
	@until curl -sf http://localhost:8080/health > /dev/null 2>&1; do sleep 1; done
	mkdir -p results
	@set -e; \
	if script --version > /dev/null 2>&1; then \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/hotkey-per-node-cb.html \
		script -q -c "k6 run tests/hotkey.js" results/hotkey-per-node-cb.log; \
	else \
		K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/hotkey-per-node-cb.html \
		script -q results/hotkey-per-node-cb.log k6 run tests/hotkey.js; \
	fi

## Run analytics load simulation (Zipf distribution, 50 VUs)
load-analytics:
	k6 run tests/analytics-load.js

## Run chaos tests
chaos-test:
	./scripts/chaos-test.sh

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
	@echo "Load Testing:"
	@echo "  make load-baseline   Baseline (100 VUs, 9 min, dashboard)"
	@echo "  make load-spike      Spike (50→300→50 VUs)"
	@echo "  make load-endurance  Endurance (100 VUs, 12 min, local only)"
	@echo "  make load-throughput Throughput ceiling (rate limiter disabled)"
	@echo "  make load-throughput-single-host Host gateway + single Redis throughput"
	@echo "  make load-throughput-ring-host Host gateway + 3-node ring throughput"
	@echo "  make load-analytics  Analytics simulation (Zipf, 50 VUs)"
	@echo ""
	@echo "Chaos Testing:"
	@echo "  make chaos-test      Run all chaos scenarios"
