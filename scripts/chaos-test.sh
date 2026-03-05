#!/usr/bin/env bash
# Chaos test: Toxiproxy fault injection across all dependencies
#
# Usage:
#   ./scripts/chaos-test.sh [dev|prod] [--no-build] [--scenario N[,N,...] | --all]
#   ./scripts/chaos-test.sh                     # dev stack, all scenarios
#   ./scripts/chaos-test.sh prod                # prod stack, all scenarios
#   ./scripts/chaos-test.sh --scenario 2,3      # dev, scenarios 2 and 3 only
#   ./scripts/chaos-test.sh prod --no-build --scenario 5
#
# Scenarios:
#   1.  Rate-limiter latency → CB opens → fail-open, then recovery → 429 enforced
#   2.  Redis +200ms latency → exceeds 50ms op timeout → CB opens → DB fallback
#   3.  Redis complete failure (timeout=0) → CB opens → DB fallback
#   4.  Postgres down → /health returns 503 → restart → /health returns 200
#   5.  RabbitMQ outage → publisher + consumer resilience (at-least-once delivery)

set -euo pipefail

# ─── Argument parsing ─────────────────────────────────────────────────────────
STACK="dev"
BUILD_FLAG="--build"
SCENARIOS="all"

while [[ $# -gt 0 ]]; do
  case "$1" in
    dev|prod)     STACK="$1"; shift ;;
    --no-build)   BUILD_FLAG=""; shift ;;
    --scenario)   SCENARIOS="$2"; shift 2 ;;
    --scenario=*) SCENARIOS="${1#--scenario=}"; shift ;;
    --all)        SCENARIOS="all"; shift ;;
    *) echo "Usage: $0 [dev|prod] [--no-build] [--scenario N[,N,...] | --all]"; exit 1 ;;
  esac
done

case "$STACK" in
  prod) BASE_COMPOSE="docker-compose.prod.yml" ;;
  dev)  BASE_COMPOSE="docker-compose.yml" ;;
esac

# ─── Globals ──────────────────────────────────────────────────────────────────
COMPOSE="docker compose -f $BASE_COMPOSE -f docker-compose.chaos.yml"
TOXIPROXY="docker exec zhejian-toxiproxy /toxiproxy-cli"
GATEWAY="http://localhost:8080"
PASSED=0
FAILED=0

pass() { echo "  ✓ $*"; PASSED=$((PASSED + 1)); }
fail() { echo "  ✗ $*"; FAILED=$((FAILED + 1)); }

# Returns 0 if scenario N should run, 1 otherwise.
# Wraps the number in commas to avoid partial matches (e.g. "1" vs "11").
should_run() {
  [ "$SCENARIOS" = "all" ] || echo ",$SCENARIOS," | grep -q ",$1,"
}

cleanup() {
  # Print summary first so it's always visible, even on set -e early exit.
  echo ""
  echo "=== Results: $PASSED passed, $FAILED failed ==="
  echo ""
  echo "--- cleanup ---"
  $TOXIPROXY toxic delete -n latency_downstream rate-limiter 2>/dev/null || true
  $TOXIPROXY toxic delete -n latency_downstream redis 2>/dev/null || true
  $TOXIPROXY toxic delete -n timeout_downstream redis 2>/dev/null || true
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

# ─── Create a test short URL for redirect scenarios ───────────────────────────
echo "=== Creating test short URL ==="
CODE=$(curl -sf -X POST -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/chaos-test"}' \
  "$GATEWAY/api/v1/shorten" | jq -r .short_code)
if [ -z "$CODE" ] || [ "$CODE" = "null" ]; then
  fail "Could not create test short URL"
  exit 1
fi
pass "Created short URL: $CODE"

# ─── Scenario 1: Rate-limiter CB → fail-open, then recovery ──────────────────
scenario_1() {
  echo ""
  echo "=== Scenario 1a: 500ms latency injection ==="

  HEALTH=$(curl -s "$GATEWAY/health")
  if echo "$HEALTH" | grep -q '"rate_limiter_cb":"closed"'; then
    pass "Health check reports rate_limiter_cb=closed"
  else
    fail "Health check did not rate_limiter_cb=closed"
  fi

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
  if [ "$all_ok" = "true" ]; then pass "All 10 requests returned 200 during fail-open"; fi

  HEALTH=$(curl -s "$GATEWAY/health")
  if echo "$HEALTH" | grep -q '"rate_limiter_cb":"open"'; then
    pass "Health check reports rate_limiter_cb=open"
  else
    fail "Health check did not rate_limiter_cb=open"
  fi

  echo ""
  echo "=== Scenario 1b: Recovery (circuit breaker Timeout=30s) ==="

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
  if [ "$got_429" = "false" ]; then fail "Rate limiting not restored — no 429 in 60 requests after circuit recovery"; fi
}

# ─── Scenario 2: Redis slow → CB opens → DB fallback ─────────────────────────
scenario_2() {
  echo ""
  echo "=== Scenario 2: Redis +200ms latency → CB opens → DB fallback ==="

  # If Scenario 1 ran immediately before this, it exhausted the rate-limit token
  # bucket (burst=50, rate=100/min ≈ 1.67 tokens/s). Wait for it to fully refill.
  if should_run 1; then
    echo "    Waiting 30s for rate-limit token bucket to refill after Scenario 1..."
    sleep 30
  fi

  $TOXIPROXY toxic add -t latency -a latency=200 redis
  pass "Injected 200ms latency on Redis proxy (exceeds 50ms operation timeout)"

  # 5 consecutive timeouts open the circuit breaker (ConsecutiveFailures=5).
  # Subsequent requests fall back to DB. All must return 301.
  all_ok=true
  for i in $(seq 1 10); do
    code=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/$CODE")
    if [ "$code" != "301" ]; then
      fail "Scenario 2: request $i returned $code, expected 301 (DB fallback)"
      all_ok=false
    fi
  done
  if [ "$all_ok" = "true" ]; then pass "Scenario 2: all 10 requests returned 301 during Redis degradation"; fi

  sleep 1
  # Use curl -s (not -sf): the health endpoint returns 503 during Redis
  # degradation because redisPinger.Ping() also times out, but the JSON body
  # still carries cache_cb="open" which is what we want to assert.
  HEALTH=$(curl -s "$GATEWAY/health")
  if echo "$HEALTH" | grep -q '"cache_cb":"open"'; then
    pass "Scenario 2: health check reports cache_cb=open"
  else
    fail "Scenario 2: health check did not report cache_cb=open"
  fi

  $TOXIPROXY toxic delete -n latency_downstream redis
  pass "Scenario 2: removed Redis latency toxic"
  echo "    Waiting 32s for circuit breaker to recover..."
  sleep 32
  # After 30 s (CB Timeout), the breaker transitions Open → HalfOpen.
  # It stays HalfOpen until MaxRequests=3 successful cacheCB.Execute() calls
  # arrive.  A single redirect with a cache miss exercises exactly 3 Execute
  # calls (cacheGet × 2 in singleflight + cacheSet), so one probe is enough
  # to close the breaker.  Fire a few to be safe.
  for i in $(seq 1 3); do curl -s -o /dev/null "$GATEWAY/$CODE"; done
  HEALTH=$(curl -s "$GATEWAY/health")
  echo "$HEALTH" | grep -q '"cache_cb":"closed"' \
    && pass "Scenario 2: cache CB recovered (closed)" \
    || fail "Scenario 2: cache CB did not recover"
}

# ─── Scenario 3: Redis down → CB opens → DB fallback ─────────────────────────
scenario_3() {
  echo ""
  echo "=== Scenario 3: Redis complete failure → CB opens → DB fallback ==="

  $TOXIPROXY toxic add -t timeout -a timeout=0 redis
  pass "Injected timeout=0 (connection drop) on Redis proxy"

  all_ok=true
  for i in $(seq 1 10); do
    code=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/$CODE")
    if [ "$code" != "301" ]; then
      fail "Scenario 3: request $i returned $code, expected 301"
      all_ok=false
    fi
  done
  if [ "$all_ok" = "true" ]; then pass "Scenario 3: all 10 requests returned 301 with Redis down"; fi

  $TOXIPROXY toxic delete -n timeout_downstream redis
  pass "Scenario 3: restored Redis"
  echo "    Waiting 32s for circuit breaker to recover..."
  sleep 32
}

# ─── Scenario 4: Postgres down → health 503 → recovery ───────────────────────
scenario_4() {
  echo ""
  echo "=== Scenario 4: Postgres down → /health returns 503 → recovery ==="

  $COMPOSE stop postgres
  pass "Stopped Postgres"

  HEALTH_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/health")
  if [ "$HEALTH_CODE" = "503" ]; then
    pass "Scenario 4: /health returns 503 with Postgres down"
  else
    fail "Scenario 4: /health returned $HEALTH_CODE, expected 503"
  fi

  # Gateway process must still be running (no panic/crash).
  curl -s "$GATEWAY/health" > /dev/null 2>&1 || true
  pass "Scenario 4: gateway process stable (no crash)"

  $COMPOSE start postgres
  pass "Scenario 4: restarted Postgres"
  echo "    Waiting 10s for connection pool to reconnect..."
  sleep 10

  HEALTH_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/health")
  if [ "$HEALTH_CODE" = "200" ]; then
    pass "Scenario 4: /health returns 200 after Postgres recovery"
  else
    fail "Scenario 4: /health returned $HEALTH_CODE after recovery, expected 200"
  fi
}

# ─── Scenario 5: RabbitMQ outage resilience ───────────────────────────────────
scenario_5() {
  echo ""
  echo "=== Scenario 5: RabbitMQ outage resilience (publisher + consumer) ==="

  # Fire a click event before the outage so it lands in the consumer's unacked batch.
  # FLUSH_INTERVAL defaults to 5s; stopping within that window leaves the message
  # unacked → RabbitMQ requeues it automatically when the connection closes.
  curl -s -o /dev/null "$GATEWAY/$CODE"
  sleep 1  # delivery reaches consumer but flush ticker (5s) has not fired yet

  $COMPOSE stop rabbitmq
  pass "Stopped RabbitMQ"
  sleep 3  # allow publisher and consumer to detect the disconnect

  # 5a: Publisher fire-and-forget — redirects must not block
  all_ok=true
  for i in $(seq 1 10); do
    code=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/$CODE")
    if [ "$code" != "301" ]; then
      fail "Scenario 5a: request $i returned $code, expected 301 (fire-and-forget)"
      all_ok=false
    fi
  done
  if [ "$all_ok" = "true" ]; then pass "Scenario 5a: all 10 redirects returned 301 with RabbitMQ down"; fi

  # 5b: Both services detected the disconnect.
  # Publisher heartbeat is 2 s → detection ≤4 s even without an immediate TCP
  # RST (e.g. WSL2).  sleep 3 + the curl loop above already cover that window.
  # --tail avoids host/daemon clock-skew that can affect --since filters.
  if $COMPOSE logs --tail 500 gateway 2>/dev/null | grep -q "AMQP connection lost"; then
    pass "Scenario 5b: gateway publisher detected broker disconnect"
  else
    fail "Scenario 5b: gateway publisher did not log broker disconnect"
  fi
  if $COMPOSE logs --since 2m analytics-worker 2>/dev/null | grep -q "broker connection lost"; then
    pass "Scenario 5b: analytics-worker consumer detected broker disconnect"
  else
    fail "Scenario 5b: analytics-worker consumer did not log broker disconnect"
  fi

  # Restart RabbitMQ and give both sides time to reconnect (exponential backoff, ~10s typical)
  RESTART_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  $COMPOSE start rabbitmq
  echo "    Waiting 15s for publisher and consumer to reconnect..."
  sleep 15

  # 5c: Publisher reconnected
  if $COMPOSE logs --since "$RESTART_TIME" gateway 2>/dev/null | grep -q "AMQP reconnected successfully"; then
    pass "Scenario 5c: gateway publisher reconnected to RabbitMQ"
  else
    fail "Scenario 5c: gateway publisher did not reconnect"
  fi

  # 5d: Consumer reconnected
  if $COMPOSE logs --since "$RESTART_TIME" analytics-worker 2>/dev/null | grep -q "analytics-worker: started"; then
    pass "Scenario 5d: analytics-worker consumer reconnected"
  else
    fail "Scenario 5d: analytics-worker consumer did not reconnect"
  fi

  # 5e: At-least-once delivery — the pre-outage event was unacked when the connection
  # dropped, so RabbitMQ requeued it. No new redirect is fired here; any "flushed batch"
  # after RESTART_TIME must be that requeued message proving the requeue→redelivery cycle.
  echo "    Waiting 8s for analytics-worker to flush the requeued batch..."
  sleep 8
  if $COMPOSE logs --since "$RESTART_TIME" analytics-worker 2>/dev/null | grep -q "flushed batch"; then
    pass "Scenario 5e: analytics-worker re-delivered and flushed unacked message (at-least-once guarantee)"
  else
    fail "Scenario 5e: analytics-worker did not flush requeued message after recovery"
  fi
}

# ─── Run selected scenarios ────────────────────────────────────────────────────
if should_run 1; then scenario_1; fi
if should_run 2; then scenario_2; fi
if should_run 3; then scenario_3; fi
if should_run 4; then scenario_4; fi
if should_run 5; then scenario_5; fi

# ─── Exit with non-zero status if any assertions failed ───────────────────────
# (The summary is printed by the cleanup trap so it always appears.)
[ "$FAILED" -eq 0 ] || exit 1
