#!/usr/bin/env bash
# Chaos test: Toxiproxy latency injection → circuit breaker → fail-open → recovery
#
# Usage:
#   ./scripts/chaos-test.sh              # dev stack  (docker-compose.yml)
#   ./scripts/chaos-test.sh prod         # prod stack (docker-compose.prod.yml)
#   ./scripts/chaos-test.sh prod --no-build  # skip --build
#
# Scenarios tested:
#   1. 500ms latency on rate-limiter gRPC → exceeds 100ms timeout →
#      5 consecutive failures → circuit opens → all requests fail-open (200)
#   2. Remove latency → wait 32s (circuit timeout=30s) →
#      circuit recovers → rate limiting enforced again (429)

set -euo pipefail

# Select base compose file from first argument (default: dev)
case "${1:-dev}" in
  prod) BASE_COMPOSE="docker-compose.prod.yml" ;;
  dev)  BASE_COMPOSE="docker-compose.yml"      ;;
  *)    echo "Usage: $0 [dev|prod] [--no-build]"; exit 1 ;;
esac

BUILD_FLAG="--build"
[ "${2:-}" = "--no-build" ] && BUILD_FLAG=""

COMPOSE="docker compose -f $BASE_COMPOSE -f docker-compose.chaos.yml"
TOXIPROXY="docker exec zhejian-toxiproxy /toxiproxy-cli"
GATEWAY="http://localhost:8080"
PASSED=0
FAILED=0

pass() { echo "  ✓ $*"; PASSED=$((PASSED + 1)); }
fail() { echo "  ✗ $*"; FAILED=$((FAILED + 1)); }

cleanup() {
  echo ""
  echo "--- cleanup ---"
  $TOXIPROXY toxic delete -n latency_downstream rate-limiter 2>/dev/null || true
  $COMPOSE down -v 2>/dev/null || true
}
trap cleanup EXIT

# ─── Start stack ───────────────────────────────────────────────────────────────
echo "=== Starting chaos stack ==="
$COMPOSE up -d $BUILD_FLAG

# ─── Wait for gateway ──────────────────────────────────────────────────────────
echo "=== Waiting for gateway ==="
for i in $(seq 1 30); do
  if curl -sf "$GATEWAY/health" > /dev/null; then
    pass "Gateway healthy (attempt $i)"
    break
  fi
  if [ "$i" -eq 30 ]; then
    fail "Gateway not ready after 60s"
    $COMPOSE logs gateway
    exit 1
  fi
  sleep 2
done

# ─── Verify toxiproxy CLI is reachable ────────────────────────────────────────
# The depends_on chain guarantees toxiproxy-init completed before gateway is
# healthy, but we add an explicit poll here for CI cold-start safety.
echo "=== Verifying toxiproxy CLI ==="
for i in $(seq 1 10); do
  docker exec zhejian-toxiproxy /toxiproxy-cli list > /dev/null 2>&1 && break
  if [ "$i" -eq 10 ]; then
    fail "toxiproxy CLI not reachable after 20s"
    exit 1
  fi
  sleep 2
done
pass "toxiproxy CLI reachable"

# ─── Scenario 1: Latency injection → circuit opens → fail-open ────────────────
echo ""
echo "=== Scenario 1: 500ms latency injection ==="

$TOXIPROXY toxic add -t latency -a latency=500 rate-limiter
pass "Injected 500ms latency on rate-limiter proxy"

# Fire 10 rapid requests.
# - Requests 1-5: gRPC times out after 100ms each → circuit breaker counts failure
# - Request 5: ConsecutiveFailures=5 → ReadyToTrip → circuit OPENS
# - Requests 6-10: ErrOpenState → fail-open immediately (no gRPC call)
# All 10 must return 200 (fail-open, never 503 or 429).
all_ok=true
for i in $(seq 1 10); do
  code=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/health")
  if [ "$code" != "200" ]; then
    fail "Request $i returned $code, expected 200 (fail-open)"
    all_ok=false
  fi
done
[ "$all_ok" = "true" ] && pass "All 10 requests returned 200 during fail-open"

# Give logs a moment to flush, then assert the state change was logged.
sleep 1
if $COMPOSE logs gateway 2>/dev/null | grep -q "circuit breaker state change"; then
  pass "Circuit breaker state change logged by gateway"
else
  fail "Circuit breaker state change not found in gateway logs"
fi

# ─── Scenario 2: Recovery ──────────────────────────────────────────────────────
echo ""
echo "=== Scenario 2: Recovery (circuit breaker Timeout=30s) ==="

$TOXIPROXY toxic delete -n latency_downstream rate-limiter
pass "Removed latency toxic"

echo "    Waiting 32s for circuit breaker to recover (half-open → closed)..."
sleep 32

# After recovery the token bucket was never decremented during the chaos window
# (fail-open bypasses Redis). So burst=50 tokens are available. Fire 60 rapid
# requests to exhaust the burst and confirm 429 is back.
got_429=false
for i in $(seq 1 60); do
  code=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/health")
  if [ "$code" = "429" ]; then
    got_429=true
    pass "Rate limiting enforced after recovery (429 at request $i)"
    break
  fi
done
[ "$got_429" = "false" ] && fail "Rate limiting not restored — no 429 in 60 requests after circuit recovery"

# ─── Summary ──────────────────────────────────────────────────────────────────
echo ""
echo "=== Results: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
