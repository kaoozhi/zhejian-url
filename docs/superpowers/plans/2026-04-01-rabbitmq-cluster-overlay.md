# RabbitMQ 3-Node Cluster Overlay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the single-node `rabbitmq` service with a named `rabbitmq-1` service and add an opt-in `docker-compose.rabbitmq-cluster.yml` overlay that forms a 3-node Raft cluster.

**Architecture:** Base compose is updated with a mechanical rename (`rabbitmq` → `rabbitmq-1`). A new overlay adds `rabbitmq-2` and `rabbitmq-3` using `rabbit_peer_discovery_classic_config` (static node list). All nodes share a mounted `rabbitmq.conf` and the same `RABBITMQ_ERLANG_COOKIE`. Clients connect to `rabbitmq-1:5672` in both single-node and cluster topologies.

**Tech Stack:** Docker Compose v2, RabbitMQ 4 (`rabbitmq:4-management-alpine`), Erlang peer discovery (classic config).

---

## File Map

| Action | Path | What changes |
|---|---|---|
| Modify | `docker-compose.yml` | Rename service + volume, update `depends_on` and `AMQP_URL` for analytics-worker |
| Modify | `docker-compose.chaos.yml` | Update hardcoded `AMQP_URL` in gateway override |
| Modify | `scripts/chaos-test.sh` | 4 occurrences of `rabbitmq` in stop/start/kill commands → `rabbitmq-1` |
| Create | `config/rabbitmq/rabbitmq.conf` | Cluster formation config (static node list) |
| Create | `docker-compose.rabbitmq-cluster.yml` | Cluster overlay: overrides `rabbitmq-1`, adds `rabbitmq-2/3`, gateway depends_on extension |

---

## Task 1: Rename the base RabbitMQ service

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Rename the service, volume reference, and volume declaration**

In `docker-compose.yml`, make these four changes:

```yaml
# Line ~140: service name
  rabbitmq-1:                          # was: rabbitmq:
    image: rabbitmq:4-management-alpine
    command: sh -c "rabbitmq-plugins enable rabbitmq_prometheus && exec rabbitmq-server"
    ports:
      - "5672:5672"
      - "15672:15672"
      - "15692:15692"
    volumes:
      - rabbitmq_1_data:/var/lib/rabbitmq  # was: rabbitmq_data
    healthcheck:
      test: ["CMD", "rabbitmq-diagnostics", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5
```

```yaml
# gateway depends_on block: change rabbitmq key to rabbitmq-1
      rabbitmq-1:                      # was: rabbitmq:
        condition: service_healthy
```

```yaml
# analytics-worker section: change depends_on key and AMQP_URL
  analytics-worker:
    env_file: .env
    environment:
      AMQP_URL: amqp://guest:guest@rabbitmq-1:5672/   # was: @rabbitmq:5672/
    depends_on:
      rabbitmq-1:                      # was: rabbitmq:
        condition: service_healthy
      postgres:
        condition: service_healthy
    restart: on-failure
```

```yaml
# volumes section at bottom: rename the volume key
  rabbitmq_1_data:                     # was: rabbitmq_data:
```

- [ ] **Step 2: Verify the base compose parses cleanly**

```bash
docker compose config --quiet
```

Expected: no output, exit code 0. Any "service X not found" or YAML parse error means a reference was missed.

- [ ] **Step 3: Commit**

```bash
git add docker-compose.yml
git commit -m "refactor: rename rabbitmq service to rabbitmq-1 in base compose"
```

---

## Task 2: Update the chaos overlay and chaos-test.sh

**Files:**
- Modify: `docker-compose.chaos.yml`
- Modify: `scripts/chaos-test.sh`

- [ ] **Step 1: Update the hardcoded AMQP_URL in docker-compose.chaos.yml**

In `docker-compose.chaos.yml`, in the `gateway:` → `environment:` block:

```yaml
      - AMQP_URL=amqp://guest:guest@rabbitmq-1:5672/   # was: @rabbitmq:5672/
```

- [ ] **Step 2: Update the four rabbitmq references in chaos-test.sh**

In `scripts/chaos-test.sh`, replace each occurrence of the bare `rabbitmq` service name in `docker compose` commands (not the `RABBITMQ_API` URL variable — that uses a host port, not a service name):

```bash
# Scenario 5 (~line 346): stop
$COMPOSE stop rabbitmq-1        # was: $COMPOSE stop rabbitmq

# Scenario 5 (~line 382): start after restart
$COMPOSE start rabbitmq-1       # was: $COMPOSE start rabbitmq

# Scenario 6 (~line 477): SIGKILL
$COMPOSE kill -s SIGKILL rabbitmq-1   # was: $COMPOSE kill -s SIGKILL rabbitmq

# Scenario 6 (~line 481): start after kill
$COMPOSE start rabbitmq-1       # was: $COMPOSE start rabbitmq
```

- [ ] **Step 3: Verify the chaos overlay parses cleanly**

```bash
docker compose -f docker-compose.yml -f docker-compose.chaos.yml config --quiet
```

Expected: exit code 0.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.chaos.yml scripts/chaos-test.sh
git commit -m "refactor: update chaos overlay and chaos-test.sh for rabbitmq-1 rename"
```

---

## Task 3: Create the RabbitMQ cluster config file

**Files:**
- Create: `config/rabbitmq/rabbitmq.conf`

- [ ] **Step 1: Create the directory and config file**

```bash
mkdir -p config/rabbitmq
```

Create `config/rabbitmq/rabbitmq.conf` with this exact content:

```ini
cluster_formation.peer_discovery_backend = rabbit_peer_discovery_classic_config
cluster_formation.classic_config.nodes.1 = rabbit@rabbitmq-1
cluster_formation.classic_config.nodes.2 = rabbit@rabbitmq-2
cluster_formation.classic_config.nodes.3 = rabbit@rabbitmq-3
```

This file is mounted read-only into all three nodes at `/etc/rabbitmq/conf.d/cluster.conf`. RabbitMQ 3.7+ merges all files in `conf.d/` with the main `rabbitmq.conf`, so there is no conflict with the image's default config.

- [ ] **Step 2: Commit**

```bash
git add config/rabbitmq/rabbitmq.conf
git commit -m "feat: add RabbitMQ cluster formation config"
```

---

## Task 4: Create the cluster overlay

**Files:**
- Create: `docker-compose.rabbitmq-cluster.yml`

- [ ] **Step 1: Create docker-compose.rabbitmq-cluster.yml**

```yaml
# RabbitMQ 3-node cluster overlay
#
# Usage:
#   docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml up -d
#
# Cluster formation: rabbit_peer_discovery_classic_config (static node list in conf.d/cluster.conf)
# Node names:  rabbit@rabbitmq-1  rabbit@rabbitmq-2  rabbit@rabbitmq-3
# Port exposure: rabbitmq-1 only (5672, 15672, 15692) — same as single-node dev
# Erlang cookie: shared via RABBITMQ_ERLANG_COOKIE env var
#
# To tear down (required before re-declaring queue with a different type):
#   docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml down -v

services:
  # Override rabbitmq-1 to add hostname, cookie, and cluster config mount.
  # All other settings (image, ports, healthcheck) are inherited from the base.
  rabbitmq-1:
    hostname: rabbitmq-1
    environment:
      RABBITMQ_ERLANG_COOKIE: ZHEJIAN_CLUSTER_COOKIE
    volumes:
      - ./config/rabbitmq/rabbitmq.conf:/etc/rabbitmq/conf.d/cluster.conf:ro
      - rabbitmq_1_data:/var/lib/rabbitmq

  rabbitmq-2:
    image: rabbitmq:4-management-alpine
    hostname: rabbitmq-2
    environment:
      RABBITMQ_ERLANG_COOKIE: ZHEJIAN_CLUSTER_COOKIE
    volumes:
      - ./config/rabbitmq/rabbitmq.conf:/etc/rabbitmq/conf.d/cluster.conf:ro
      - rabbitmq_2_data:/var/lib/rabbitmq
    healthcheck:
      test: ["CMD", "rabbitmq-diagnostics", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  rabbitmq-3:
    image: rabbitmq:4-management-alpine
    hostname: rabbitmq-3
    environment:
      RABBITMQ_ERLANG_COOKIE: ZHEJIAN_CLUSTER_COOKIE
    volumes:
      - ./config/rabbitmq/rabbitmq.conf:/etc/rabbitmq/conf.d/cluster.conf:ro
      - rabbitmq_3_data:/var/lib/rabbitmq
    healthcheck:
      test: ["CMD", "rabbitmq-diagnostics", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  # Extend the gateway's depends_on to wait for all cluster members.
  # Compose merges this with the base depends_on (which already waits for rabbitmq-1).
  gateway:
    depends_on:
      rabbitmq-2:
        condition: service_healthy
      rabbitmq-3:
        condition: service_healthy

volumes:
  rabbitmq_1_data:
  rabbitmq_2_data:
  rabbitmq_3_data:
```

- [ ] **Step 2: Verify the cluster overlay parses cleanly**

```bash
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml config --quiet
```

Expected: exit code 0.

- [ ] **Step 3: Commit**

```bash
git add docker-compose.rabbitmq-cluster.yml
git commit -m "feat: add RabbitMQ 3-node cluster overlay"
```

---

## Task 5: Smoke-test the cluster

This task has no code changes. It verifies the cluster forms correctly and the quorum queue is replicated across all three nodes.

- [ ] **Step 1: Tear down any existing stack (volumes must be fresh)**

```bash
docker compose down -v
```

- [ ] **Step 2: Bring up the cluster stack**

```bash
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml up -d --build
```

Wait for the gateway healthcheck to pass (up to ~90s on first build):

```bash
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml ps
```

Expected: all services show `healthy` or `running`. The gateway will be `healthy` only after all three RabbitMQ nodes pass their healthchecks.

- [ ] **Step 3: Verify cluster membership**

```bash
curl -s http://guest:guest@localhost:15672/api/nodes | jq '[.[] | .name]'
```

Expected output (order may vary):
```json
[
  "rabbit@rabbitmq-1",
  "rabbit@rabbitmq-2",
  "rabbit@rabbitmq-3"
]
```

If only one node appears, the Erlang cookie or hostname is misconfigured — check `docker compose logs rabbitmq-2` for `Authentication failed` errors.

- [ ] **Step 4: Trigger queue declaration and verify quorum replication**

The analytics-worker declares the queue on startup. Check that it's a quorum queue replicated to all three nodes:

```bash
curl -s http://guest:guest@localhost:15672/api/queues/%2F/analytics.clicks \
  | jq '{type: .type, members: .members, leader: .leader}'
```

Expected:
```json
{
  "type": "quorum",
  "members": ["rabbit@rabbitmq-1", "rabbit@rabbitmq-2", "rabbit@rabbitmq-3"],
  "leader": "rabbit@rabbitmq-1"
}
```

`members` must contain all three nodes. `leader` will be whichever node won the initial Raft election (usually `rabbitmq-1`).

- [ ] **Step 5: Verify a message survives killing one node**

```bash
# Create a short URL to generate a click event
CODE=$(curl -sf -X POST -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/cluster-test"}' \
  http://localhost:8080/api/v1/shorten | jq -r .short_code)

# Stop the analytics-worker so messages queue up
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml stop analytics-worker

# Publish 10 events
for i in $(seq 1 10); do curl -s -o /dev/null "http://localhost:8080/$CODE"; done
sleep 2

# Record pre-kill depth
curl -s http://guest:guest@localhost:15672/api/queues/%2F/analytics.clicks \
  | jq '.messages_ready'
# Expected: 10

# Kill one non-leader node (minority failure — quorum intact with 2/3)
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml \
  kill -s SIGKILL rabbitmq-2
sleep 2

# Queue must still be readable (2/3 quorum holds)
curl -s http://guest:guest@localhost:15672/api/queues/%2F/analytics.clicks \
  | jq '.messages_ready'
# Expected: 10 (no loss, queue still available)

# Restore
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml start rabbitmq-2
```

- [ ] **Step 6: Tear down**

```bash
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml down -v
```

- [ ] **Step 7: Verify the base single-node stack still works**

```bash
docker compose up -d --build
# wait ~30s, then:
curl -sf http://localhost:8080/health | jq .amqp_connected
# Expected: true
docker compose down -v
```
