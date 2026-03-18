package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"sync"

	"github.com/redis/go-redis/v9"
)

type ClientProvider interface {
	ClientFor(key string) *redis.Client
	Close()
	Ping(ctx context.Context) error
}

// HashRing maps cache keys to Redis clients using consistent hashing with
// virtual nodes. Adding or removing one node re-routes only ~1/N of all keys;
// no data is migrated — affected keys cold-miss and self-populate within one TTL.
type HashRing struct {
	mu       sync.RWMutex
	vnodes   []uint32          // sorted virtual node hashes
	nodeMap  map[uint32]string // vnode hash → node name
	clients  map[string]*redis.Client
	replicas int
}

// NewHashRing builds a consistent hash ring from the given clients.
// replicas is the number of virtual nodes per real node; 150 gives ~2-3% distribution error.
func NewHashRing(clients map[string]*redis.Client, replicas int) *HashRing {
	r := &HashRing{
		nodeMap:  make(map[uint32]string),
		clients:  make(map[string]*redis.Client),
		replicas: replicas,
	}

	for name, client := range clients {
		r.clients[name] = client
		r.addVnodes(name)
	}
	slices.Sort(r.vnodes)
	return r
}

func (r *HashRing) Close() {
	for _, client := range r.clients {
		client.Close()
	}
}

func (r *HashRing) Ping(ctx context.Context) error {
	for _, client := range r.clients {
		if err := client.Ping(ctx).Err(); err != nil {
			return err
		}
	}
	return nil
}

// addVnodes places replicas virtual nodes for name onto the ring.
// Each vnode is hashed as "<name>,#<i>" so positions are spread across the ring.
// The caller is responsible for sorting vnodes after all nodes are added.
func (r *HashRing) addVnodes(name string) {
	for i := 0; i < r.replicas; i++ {
		h := hashKey(fmt.Sprintf("%s,#%d", name, i))
		r.vnodes = append(r.vnodes, h)
		r.nodeMap[h] = name
	}
}

// nodeFor is the unlocked core — callers must hold at least r.mu.RLock().
func (r *HashRing) nodeFor(key string) string {
	if len(r.vnodes) == 0 {
		return ""
	}
	h := hashKey(key)
	idx := findIndex(r.vnodes, h)
	if idx == len(r.vnodes) {
		idx = 0
	}
	return r.nodeMap[r.vnodes[idx]]
}

// NodeFor returns the name of the node responsible for key.
func (r *HashRing) NodeFor(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nodeFor(key)
}

// ClientFor returns the Redis client responsible for key, or nil if the ring is empty.
func (r *HashRing) ClientFor(key string) *redis.Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name := r.nodeFor(key)
	if name == "" {
		return nil
	}
	return r.clients[name]
}

// Remove evicts name and its virtual nodes from the ring.
// Keys previously owned by name are redistributed to their next clockwise neighbour.
func (r *HashRing) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	toRemove := make(map[uint32]struct{})
	for i := 0; i < r.replicas; i++ {
		toRemove[hashKey(fmt.Sprintf("%s,#%d", name, i))] = struct{}{}
	}

	// Allocate a fresh slice to avoid overwriting elements still being iterated.
	filtered := make([]uint32, 0, len(r.vnodes)-r.replicas)
	for _, h := range r.vnodes {
		if _, skip := toRemove[h]; !skip {
			filtered = append(filtered, h)
		}
	}

	r.vnodes = filtered
	for h := range toRemove {
		delete(r.nodeMap, h)
	}
	delete(r.clients, name)
}

// Add inserts a new node and its virtual nodes into the ring.
// Only routing is updated — no data is migrated. Keys that now hash to the new node
// will cold-miss on first lookup and self-populate within one TTL via the normal write path.
// Calling Add with an existing name is a no-op.
func (r *HashRing) Add(name string, client *redis.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.clients[name]; exists {
		return
	}
	r.clients[name] = client
	r.addVnodes(name)
	slices.Sort(r.vnodes)
}

// hashKey maps an arbitrary string to a uint32 position on the ring using SHA-256.
// Only the first 4 bytes are used, giving a 32-bit hash space (0 to 2³²-1).
func hashKey(key string) uint32 {
	h := sha256.Sum256([]byte(key))
	return binary.BigEndian.Uint32(h[:4])
}

// findIndex returns the index of the first vnode hash >= h (clockwise successor).
// If h exceeds all vnodes the caller wraps to index 0 to close the ring.
func findIndex(vnodes []uint32, h uint32) int {
	l, r := 0, len(vnodes)
	for l < r {
		m := l + (r-l)/2
		if vnodes[m] < h {
			l = m + 1
		} else if vnodes[m] > h {
			r = m
		} else {
			return m
		}
	}
	return l
}
