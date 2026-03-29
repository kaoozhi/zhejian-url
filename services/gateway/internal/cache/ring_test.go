package cache_test

import (
	"fmt"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/zhejian/url-shortener/gateway/internal/cache"
)

// TestHashRing_DistributionIsUniform verifies that 150 virtual nodes per real node
// spread 10 000 keys within ±5% of the ideal 33.3% share per node.
func TestHashRing_DistributionIsUniform(t *testing.T) {
	clients := map[string]*redis.Client{
		"node-1": redis.NewClient(&redis.Options{Addr: "localhost:1"}),
		"node-2": redis.NewClient(&redis.Options{Addr: "localhost:2"}),
		"node-3": redis.NewClient(&redis.Options{Addr: "localhost:3"}),
	}

	ring := cache.NewHashRing(clients, 150)

	counts := map[string]int{"node-1": 0, "node-2": 0, "node-3": 0}
	total := 10000

	for i := range total {
		key := fmt.Sprintf("url:%d", i)
		node := ring.NodeFor(key)
		counts[node] += 1
	}

	for node, count := range counts {
		pct := float64(count) / float64(total)
		assert.InDelta(t, 0.333, pct, 0.05,
			"node %s owns %.1f%% of keys (expected ~33%%)", node, pct*100)
	}
}

// TestHashRing_RemoveNodeRemapsOneThird is the core consistent-hashing guarantee:
// removing one of N nodes remaps only ~1/N keys; the other (N-1)/N keys are undisturbed.
func TestHashRing_RemoveNodeRemapsOneThird(t *testing.T) {
	clients := map[string]*redis.Client{
		"node-1": redis.NewClient(&redis.Options{Addr: "localhost:1"}),
		"node-2": redis.NewClient(&redis.Options{Addr: "localhost:2"}),
		"node-3": redis.NewClient(&redis.Options{Addr: "localhost:3"}),
	}
	ring := cache.NewHashRing(clients, 150)
	const total = 10000

	// Snapshot routing before removal.
	before := make(map[string]string, total)
	for i := range total {
		key := fmt.Sprintf("url:%d", i)
		before[key] = ring.NodeFor(key)
	}

	ring.Remove("node-2")

	// Count keys whose owner changed after removal.
	remapped := 0
	for key, wasNode := range before {
		if ring.NodeFor(key) != wasNode {
			remapped++
		}
	}

	pctRemapped := float64(remapped) / float64(total)
	assert.InDelta(t, 0.333, pctRemapped, 0.07,
		"%.1f%% of keys remapped (expected ~33%%)", pctRemapped*100)
}

// TestHashRing_SameKeyAlwaysRoutesToSameNode guards against accidental non-determinism
// in the hash function or lookup logic — the same key must always resolve to the same node.
func TestHashRing_SameKeyAlwaysRoutesToSameNode(t *testing.T) {
	clients := map[string]*redis.Client{
		"node-1": redis.NewClient(&redis.Options{Addr: "localhost:1"}),
		"node-2": redis.NewClient(&redis.Options{Addr: "localhost:2"}),
		"node-3": redis.NewClient(&redis.Options{Addr: "localhost:3"}),
	}
	ring := cache.NewHashRing(clients, 150)

	for i := range 100 {
		key := fmt.Sprintf("url:%d", i)
		first := ring.NodeFor(key)
		for range 10 {
			assert.Equal(t, first, ring.NodeFor(key), "key %s routed differently on repeat call", key)
		}
	}
}

// TestHashRing_EmptyRingReturnsZeroValues ensures NodeFor/ClientFor degrade gracefully
// rather than panicking when the ring has no nodes.
func TestHashRing_EmptyRingReturnsZeroValues(t *testing.T) {
	ring := cache.NewHashRing(map[string]*redis.Client{}, 150)
	assert.Equal(t, "", ring.NodeFor("url:abc"))
	assert.Nil(t, ring.ClientFor("url:abc"))
}

// TestHashRing_AddNodeReroutesOneQuarter verifies that adding a 4th node to a 3-node ring
// re-routes only ~25% of keys to the new node; the remaining ~75% are undisturbed.
func TestHashRing_AddNodeReroutesOneQuarter(t *testing.T) {
	clients := map[string]*redis.Client{
		"node-1": redis.NewClient(&redis.Options{Addr: "localhost:1"}),
		"node-2": redis.NewClient(&redis.Options{Addr: "localhost:2"}),
		"node-3": redis.NewClient(&redis.Options{Addr: "localhost:3"}),
	}
	ring := cache.NewHashRing(clients, 150)
	const total = 10000

	// Snapshot routing before adding the new node.
	before := make(map[string]string, total)
	for i := range total {
		key := fmt.Sprintf("url:%d", i)
		before[key] = ring.NodeFor(key)
	}

	ring.Add("node-4", redis.NewClient(&redis.Options{Addr: "localhost:4"}))

	// Keys re-routed to node-4 are the ones that cold-miss after scale-out.
	reroutedToNew := 0
	for key := range before {
		if ring.NodeFor(key) == "node-4" {
			reroutedToNew++
		}
	}

	pct := float64(reroutedToNew) / float64(total)
	assert.InDelta(t, 0.25, pct, 0.05,
		"%.1f%% of keys routed to node-4 (expected ~25%%)", pct*100)
}

// TestHashRing_AddExistingNodeIsNoop verifies that calling Add with a name already
// in the ring does not duplicate virtual nodes or change routing.
func TestHashRing_AddExistingNodeIsNoop(t *testing.T) {
	clients := map[string]*redis.Client{
		"node-1": redis.NewClient(&redis.Options{Addr: "localhost:1"}),
		"node-2": redis.NewClient(&redis.Options{Addr: "localhost:2"}),
	}
	ring := cache.NewHashRing(clients, 150)

	// Record routing before the duplicate Add.
	before := make(map[string]string, 1000)
	for i := range 1000 {
		key := fmt.Sprintf("url:%d", i)
		before[key] = ring.NodeFor(key)
	}

	// Adding node-1 again must be a no-op.
	ring.Add("node-1", redis.NewClient(&redis.Options{Addr: "localhost:99"}))

	for key, want := range before {
		assert.Equal(t, want, ring.NodeFor(key), "routing changed after duplicate Add for key %s", key)
	}
}

// TestHashRing_SingleNodeOwnsAllKeys verifies that a one-node ring routes every key
// to that node, and that removing it leaves an empty ring with zero-value responses.
func TestHashRing_SingleNodeOwnsAllKeys(t *testing.T) {
	clients := map[string]*redis.Client{
		"node-1": redis.NewClient(&redis.Options{Addr: "localhost:1"}),
	}
	ring := cache.NewHashRing(clients, 150)

	for i := range 1000 {
		assert.Equal(t, "node-1", ring.NodeFor(fmt.Sprintf("url:%d", i)))
	}

	// After removal the ring is empty — must not panic or return stale data.
	ring.Remove("node-1")
	assert.Equal(t, "", ring.NodeFor("url:any"))
	assert.Nil(t, ring.ClientFor("url:any"))
}
