# RabbitMQ 3-Node Cluster Overlay — Design Spec

**Date:** 2026-04-01
**Branch:** dev/ph12
**Scope:** Infrastructure only — no application code changes, no scenario 6 update.

---

## Goal

Add a `docker-compose.rabbitmq-cluster.yml` overlay that replaces the single-node RabbitMQ with a 3-node Raft cluster. The base `docker compose up` stays unchanged (single node, fast dev). The overlay is opt-in for quorum fault-tolerance testing.

---

## Prerequisites: Base Rename

The base `docker-compose.yml` names the broker service `rabbitmq`. With a cluster overlay adding `rabbitmq-1/2/3`, the base service would conflict on port 5672 and produce a stranded container. The fix is a mechanical rename before the overlay is added.

### Files touched

| File | Change |
|---|---|
| `docker-compose.yml` | `rabbitmq:` → `rabbitmq-1:`, `rabbitmq_data` → `rabbitmq_1_data`, gateway and analytics-worker `depends_on.rabbitmq` → `rabbitmq-1`, analytics-worker `AMQP_URL` env var `@rabbitmq:` → `@rabbitmq-1:` |
| `docker-compose.chaos.yml` | `AMQP_URL=...@rabbitmq:5672/` → `@rabbitmq-1:5672/` |
| `scripts/chaos-test.sh` | `$COMPOSE stop/start/kill rabbitmq` → `rabbitmq-1` (4 occurrences in scenarios 5 and 6) |

Prometheus scrapes via the host port (`host.docker.internal:15692`) — unaffected.

---

## Cluster Overlay

### Clustering mechanism

`rabbit_peer_discovery_classic_config` — static node list in a mounted `rabbitmq.conf`. Chosen over DNS peer discovery for explicitness and reliable behaviour in Docker Compose without special network configuration.

### Node naming

RabbitMQ derives its node name as `rabbit@<container_hostname>`. Each container is given `hostname: rabbitmq-N` so the names are deterministic:

- `rabbit@rabbitmq-1`
- `rabbit@rabbitmq-2`
- `rabbit@rabbitmq-3`

### Erlang cookie

Set via `RABBITMQ_ERLANG_COOKIE` env var (same value on all three nodes). Required for inter-node authentication. No file mounting needed.

### Config file

**`config/rabbitmq/rabbitmq.conf`** — mounted read-only into all three nodes:

```ini
cluster_formation.peer_discovery_backend = rabbit_peer_discovery_classic_config
cluster_formation.classic_config.nodes.1 = rabbit@rabbitmq-1
cluster_formation.classic_config.nodes.2 = rabbit@rabbitmq-2
cluster_formation.classic_config.nodes.3 = rabbit@rabbitmq-3
```

### Port exposure

| Node | Ports exposed | Purpose |
|---|---|---|
| `rabbitmq-1` | 5672, 15672, 15692 | Same as today — AMQP, management UI, Prometheus |
| `rabbitmq-2` | none | Cluster member only |
| `rabbitmq-3` | none | Cluster member only |

### New file: `docker-compose.rabbitmq-cluster.yml`

The overlay:
1. **Overrides `rabbitmq-1`** — adds `hostname`, `RABBITMQ_ERLANG_COOKIE`, and the config mount.
2. **Adds `rabbitmq-2` and `rabbitmq-3`** — same image and command as `rabbitmq-1`, internal-only, each with its own named volume.
3. **Overrides `gateway`** — adds `depends_on` for `rabbitmq-2` and `rabbitmq-3` (Compose merges with the base's `rabbitmq-1` dependency). No `AMQP_URL` change — gateway still connects to `rabbitmq-1:5672`; quorum queue replication is transparent.
4. **Adds three volumes** — `rabbitmq_1_data` (override), `rabbitmq_2_data`, `rabbitmq_3_data`.

### Quorum queue behaviour with 3 nodes

The existing `x-queue-type: quorum` declaration in `consumer.go` is unchanged. RabbitMQ's default replication factor is `min(cluster_size, 3)`, so the queue is replicated across all three nodes automatically. A write is acknowledged only after a majority (2/3) confirms it — this is the Raft guarantee the current single-node test cannot exercise.

---

## Usage

```bash
# Normal dev — single node, unchanged
docker compose up -d

# 3-node cluster
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml up -d

# Chaos + cluster
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml -f docker-compose.chaos.yml up -d

# Tear down (must use -v to allow queue type to be re-declared fresh)
docker compose -f docker-compose.yml -f docker-compose.rabbitmq-cluster.yml down -v
```

---

## Out of Scope

- Scenario 6 chaos test update (kill one node, assert queue still available on remaining two) — deferred to a follow-up.
- Toxiproxy proxies for `rabbitmq-2` and `rabbitmq-3` — not needed until scenario 6 is updated.
- Production compose (`docker-compose.prod.yml`) — not affected.
