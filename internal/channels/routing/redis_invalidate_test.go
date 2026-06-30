//go:build redis

package routing

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// Live Redis pub/sub round-trip: 2 AgentRouteResolver instances share the
// same Redis broker. Invalidate on resolver A must evict the cache entry
// on resolver B within a tight deadline. Mirrors a 2-node prod deploy.
//
// Skipped unless GOCLAW_REDIS_TEST_DSN is set. Default expected DSN:
// `redis://localhost:6379/0` against a docker-run redis:7-alpine.
func TestRedisInvalidate_TwoNodeRoundtrip(t *testing.T) {
	dsn := os.Getenv("GOCLAW_REDIS_TEST_DSN")
	if dsn == "" {
		dsn = "redis://localhost:6379/0"
	}

	opts, err := redis.ParseURL(dsn)
	if err != nil {
		t.Skipf("invalid GOCLAW_REDIS_TEST_DSN: %v", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()

	pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		t.Skipf("Redis not reachable at %s: %v", dsn, err)
	}
	// Use a SEPARATE client for the second node so each has its own connection
	// pool — mimics 2 processes against shared Redis.
	clientB := redis.NewClient(opts)
	defer clientB.Close()

	chID := uuid.Must(uuid.NewV7())
	agentID := uuid.Must(uuid.NewV7())

	// Each "node" has its own resolver + store. Same underlying route data
	// so that after both nodes warm cache they get a consistent picture.
	makeNode := func(client *redis.Client) (*AgentRouteResolver, func(), *fakeRouteStore) {
		fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
			chID: {newRoute(agentID, "direct", nil, false, 100, true, nil)},
		}}
		r := NewAgentRouteResolver(fs, time.Hour)
		stop, err := StartRedisInvalidate(client, r)
		if err != nil {
			t.Fatalf("StartRedisInvalidate: %v", err)
		}
		return r, stop, fs
	}

	nodeA, stopA, _ := makeNode(client)
	defer stopA()
	nodeB, stopB, fsB := makeNode(clientB)
	defer stopB()

	// Give the subscriber goroutines a moment to register with Redis.
	time.Sleep(100 * time.Millisecond)

	// Warm cache on BOTH nodes.
	nodeA.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	nodeB.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if fsB.calls != 1 {
		t.Fatalf("nodeB warm: want 1 store call; got %d", fsB.calls)
	}

	// Now: Invalidate on nodeA — nodeB must receive the event and evict.
	nodeA.Invalidate(chID)

	// Poll for nodeB to re-query the store (cache evicted), max 2s.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		nodeB.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
		if fsB.calls >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if fsB.calls < 2 {
		t.Fatalf("nodeB cache should have been evicted by peer Invalidate; got %d store calls (want ≥2)", fsB.calls)
	}
}

// Publisher-only sanity: StartRedisInvalidate with nil client returns a noop
// stop without erroring (matches single-node-mode contract).
func TestStartRedisInvalidate_NilClient(t *testing.T) {
	r := NewAgentRouteResolver(&fakeRouteStore{}, 0)
	stop, err := StartRedisInvalidate(nil, r)
	if err != nil {
		t.Fatalf("nil client should be no-op, not error; got %v", err)
	}
	if stop == nil {
		t.Fatal("stop func must not be nil")
	}
	stop() // safe to call
}

// Wrong-typed client is operator error → return error, not panic.
func TestStartRedisInvalidate_WrongType(t *testing.T) {
	r := NewAgentRouteResolver(&fakeRouteStore{}, 0)
	_, err := StartRedisInvalidate("not a redis client", r)
	if err == nil {
		t.Fatal("wrong-type client should error; got nil")
	}
}
