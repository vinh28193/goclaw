//go:build integration

package integration

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/channels/routing"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
)

// P0 invariant: a route in tenant A MUST NOT be readable / matchable from a
// tenant B context, even if tenant B owns a channel with an identical name or
// the channel_instance UUID happened to collide (it can't with v7, but the
// store filter is the truth). This is the exact test phase-07 spec promised
// as `channel_agent_routes_tenant_isolation_test.go`.

func TestChannelAgentRoutes_Integration_TenantIsolation_StoreFilter(t *testing.T) {
	db := testDB(t)

	tenantA, agentA := seedTenantAgent(t, db)
	tenantB, agentB := seedTenantAgent(t, db)
	channelA := seedChannelInstanceForRoutes(t, db, tenantA, agentA)
	channelB := seedChannelInstanceForRoutes(t, db, tenantB, agentB)

	routeStore := pg.NewPGChannelAgentRouteStore(db)

	// Tenant A creates a route.
	ctxA := tenantCtx(tenantA)
	allow := []string{"tenant-A-secret-tool"}
	if err := routeStore.Create(ctxA, &store.ChannelAgentRouteData{
		ChannelInstanceID: channelA,
		AgentID:           agentA,
		Name:              "A-secret",
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
		ToolAllow:         &allow,
	}); err != nil {
		t.Fatalf("tenantA create: %v", err)
	}

	// Tenant B asks for routes on its own channel — must see nothing.
	ctxB := tenantCtx(tenantB)
	if rows, err := routeStore.ListByChannelInstance(ctxB, channelB); err != nil {
		t.Fatalf("tenantB list own channel: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("tenantB own channel should have 0 routes; got %d", len(rows))
	}

	// Tenant B asks for tenant A's channel directly — must also see nothing
	// (store SQL filter scopes by tenant on every read).
	if rows, err := routeStore.ListByChannelInstance(ctxB, channelA); err != nil {
		t.Fatalf("tenantB list tenantA channel: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("tenantB MUST NOT see tenantA routes; got %d rows", len(rows))
	}

	// Sanity: tenant A still sees its own row.
	if rows, err := routeStore.ListByChannelInstance(ctxA, channelA); err != nil {
		t.Fatalf("tenantA self-list: %v", err)
	} else if len(rows) != 1 {
		t.Fatalf("tenantA own channel should have 1 route; got %d", len(rows))
	}
}

// Resolver invokes ListByChannelInstance — confirm the tenant-scoped read
// flows through it. Tenant B's resolver constructed against the same store
// must NOT match Tenant A's route, even when given Tenant A's channel UUID.
func TestChannelAgentRoutes_Integration_TenantIsolation_ResolverScoping(t *testing.T) {
	db := testDB(t)

	tenantA, agentA := seedTenantAgent(t, db)
	tenantB, _ := seedTenantAgent(t, db)
	channelA := seedChannelInstanceForRoutes(t, db, tenantA, agentA)

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	ctxA := tenantCtx(tenantA)
	if err := routeStore.Create(ctxA, &store.ChannelAgentRouteData{
		ChannelInstanceID: channelA,
		AgentID:           agentA,
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	// Sanity — under tenantA ctx the resolver matches.
	resolver := routing.NewAgentRouteResolver(routeStore, 0)
	if a, _, m, _ := resolver.Resolve(ctxA, channelA, "", "", "direct", routing.MediaKindText, false); !m || a != agentA {
		t.Fatalf("under tenantA ctx resolver should match: agent=%v matched=%v", a, m)
	}

	// Under tenantB ctx, even pointing at tenantA's channel UUID, the
	// ListByChannelInstance SQL filter returns 0 rows → resolver unmatched.
	ctxB := tenantCtx(tenantB)
	resolverB := routing.NewAgentRouteResolver(routeStore, 0)
	if _, _, m, _ := resolverB.Resolve(ctxB, channelA, "", "", "direct", routing.MediaKindText, false); m {
		t.Fatal("tenantB ctx must NOT see tenantA's route even with the channel UUID")
	}
}
