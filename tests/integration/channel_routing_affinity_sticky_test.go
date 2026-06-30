//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels/routing"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
)

// Live PG end-to-end for sticky routing: same (channel, peer) Resolve calls
// must converge on the same agent across multiple inbound messages, with the
// tool_allow snapshot held even when operator edits the source route.

func TestSticky_Integration_StickyHitAcrossResolveCalls(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgent := seedTenantAgent(t, db)
	otherAgent := seedExtraAgent(t, db, tenantID, "swap")
	channelID := seedChannelInstanceForRoutes(t, db, tenantID, defaultAgent)

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	affStore := pg.NewPGChannelRoutingAffinityStore(db)
	ctx := tenantCtx(tenantID)
	t.Cleanup(func() {
		db.Exec("DELETE FROM channel_routing_affinity WHERE channel_instance_id = $1", channelID)
	})

	// Operator creates initial route → defaultAgent.
	if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           defaultAgent,
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}); err != nil {
		t.Fatalf("create initial route: %v", err)
	}

	r := routing.NewAgentRouteResolver(routeStore, time.Hour)
	r.SetAffinityStore(affStore, time.Hour)

	// 1st inbound from peer-A → matches route → bound to defaultAgent.
	a1, _, m1, _ := r.Resolve(ctx, channelID, "peer-A", "", "direct", routing.MediaKindText, false)
	if !m1 || a1 != defaultAgent {
		t.Fatalf("1st call: agent=%v matched=%v", a1, m1)
	}

	// Operator changes the route to point at otherAgent.
	rows, _ := routeStore.ListByChannelInstance(ctx, channelID)
	if err := routeStore.Update(ctx, rows[0].ID, map[string]any{"agent_id": otherAgent}); err != nil {
		t.Fatalf("update route: %v", err)
	}
	r.Invalidate(channelID) // operator did invalidate too

	// 2nd inbound from same peer → still bound to defaultAgent (sticky).
	a2, _, m2, _ := r.Resolve(ctx, channelID, "peer-A", "", "direct", routing.MediaKindText, false)
	if !m2 || a2 != defaultAgent {
		t.Fatalf("2nd call should hit sticky → defaultAgent; got %v", a2)
	}

	// New peer-B sees the NEW agent (no prior binding).
	aB, _, mB, _ := r.Resolve(ctx, channelID, "peer-B", "", "direct", routing.MediaKindText, false)
	if !mB || aB != otherAgent {
		t.Fatalf("new peer should see updated route → otherAgent; got %v", aB)
	}
}

func TestSticky_Integration_ExpiryFallsBackToRuleEval(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgent := seedTenantAgent(t, db)
	channelID := seedChannelInstanceForRoutes(t, db, tenantID, defaultAgent)

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	affStore := pg.NewPGChannelRoutingAffinityStore(db)
	ctx := tenantCtx(tenantID)
	t.Cleanup(func() {
		db.Exec("DELETE FROM channel_routing_affinity WHERE channel_instance_id = $1", channelID)
	})

	if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           defaultAgent,
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	// Manually insert an ALREADY-EXPIRED affinity row → resolver MUST fall
	// through to rule eval (SQL filter expires_at > NOW() drops it).
	// Use a real seeded agent (FK constraint) — agent identity doesn't matter
	// because the row is expired and should never be returned.
	ghostAgent := seedExtraAgent(t, db, tenantID, "ghost")
	if err := affStore.Upsert(ctx, &store.ChannelRoutingAffinityData{
		TenantID:          tenantID,
		ChannelInstanceID: channelID,
		PeerID:            "peer-expired",
		AgentID:           ghostAgent,
		ExpiresAt:         time.Now().Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("seed expired affinity: %v", err)
	}

	r := routing.NewAgentRouteResolver(routeStore, time.Hour)
	r.SetAffinityStore(affStore, time.Hour)

	// Resolve must skip expired row and fall through → defaultAgent (from rule).
	a, _, m, _ := r.Resolve(ctx, channelID, "peer-expired", "", "direct", routing.MediaKindText, false)
	if !m || a != defaultAgent {
		t.Fatalf("expired affinity must fall through to rule eval; got agent=%v matched=%v", a, m)
	}
}

func TestSticky_Integration_TenantIsolation(t *testing.T) {
	db := testDB(t)
	tenantA, agentA := seedTenantAgent(t, db)
	tenantB, agentB := seedTenantAgent(t, db)
	channelA := seedChannelInstanceForRoutes(t, db, tenantA, agentA)
	channelB := seedChannelInstanceForRoutes(t, db, tenantB, agentB)

	affStore := pg.NewPGChannelRoutingAffinityStore(db)
	ctxA := tenantCtx(tenantA)
	ctxB := tenantCtx(tenantB)
	t.Cleanup(func() {
		db.Exec("DELETE FROM channel_routing_affinity WHERE tenant_id IN ($1, $2)", tenantA, tenantB)
	})

	// Tenant A binds peer-X to its own agent.
	if err := affStore.Upsert(ctxA, &store.ChannelRoutingAffinityData{
		ChannelInstanceID: channelA,
		PeerID:            "peer-X",
		AgentID:           agentA,
		ExpiresAt:         time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("tenantA upsert: %v", err)
	}

	// Tenant B tries to read its own channelB / peer-X → must be miss.
	if _, err := affStore.Get(ctxB, channelB, "peer-X"); err == nil {
		t.Fatal("tenantB should not see anything for its own channel + peer-X")
	}
	// Tenant B tries to read tenant A's channelA / peer-X → must also miss.
	if _, err := affStore.Get(ctxB, channelA, "peer-X"); err == nil {
		t.Fatal("tenantB MUST NOT see tenantA's sticky binding")
	}
	// Tenant A still sees its own binding.
	if row, err := affStore.Get(ctxA, channelA, "peer-X"); err != nil || row.AgentID != agentA {
		t.Fatalf("tenantA self-read failed: err=%v row=%+v", err, row)
	}
}

func TestSticky_Integration_DeleteExpiredPrunesPastTTL(t *testing.T) {
	db := testDB(t)
	tenantID, agentID := seedTenantAgent(t, db)
	channelID := seedChannelInstanceForRoutes(t, db, tenantID, agentID)

	affStore := pg.NewPGChannelRoutingAffinityStore(db)
	ctx := tenantCtx(tenantID)
	t.Cleanup(func() {
		db.Exec("DELETE FROM channel_routing_affinity WHERE channel_instance_id = $1", channelID)
	})

	// 1 expired + 1 future.
	if err := affStore.Upsert(ctx, &store.ChannelRoutingAffinityData{
		ChannelInstanceID: channelID, PeerID: "expired", AgentID: agentID,
		ExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	if err := affStore.Upsert(ctx, &store.ChannelRoutingAffinityData{
		ChannelInstanceID: channelID, PeerID: "fresh", AgentID: agentID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	// Use cross-tenant ctx for the cleanup job — operator pruning is system-wide.
	n, err := affStore.DeleteExpired(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n < 1 {
		t.Fatalf("DeleteExpired should remove ≥1 row; got %d", n)
	}
	// Fresh row still readable.
	if _, err := affStore.Get(ctx, channelID, "fresh"); err != nil {
		t.Fatalf("fresh row should survive prune: %v", err)
	}
	// Expired row gone.
	if _, err := affStore.Get(ctx, channelID, "expired"); err == nil {
		t.Fatal("expired row must be gone after prune")
	}
}
