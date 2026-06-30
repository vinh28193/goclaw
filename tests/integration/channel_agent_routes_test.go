//go:build integration

package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/channels/routing"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
)

// channel_agent_routes integration tests — drive the store + resolver end-to-end
// against live Postgres (pgvector:pg18). Asserts scenarios (a), (b), (d) from
// phase-07 spec without requiring synthetic Telegram/Feishu/Zalo inbound — the
// resolver IS the routing decision point, so exercising it directly with
// store-backed rows proves the contract the channel handlers rely on.

func seedChannelInstanceForRoutes(t *testing.T, db *sql.DB, tenantID, agentID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	now := time.Now()
	_, err := db.Exec(
		`INSERT INTO channel_instances (id, name, display_name, channel_type, agent_id,
		 config, enabled, created_by, created_at, updated_at, tenant_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, "telegram/routes-"+id.String()[:8], "Routes Test", "telegram", agentID,
		[]byte("{}"), true, "test", now, now, tenantID,
	)
	if err != nil {
		t.Fatalf("seed channel_instance: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM channel_agent_routes WHERE channel_instance_id = $1", id)
		db.Exec("DELETE FROM channel_instances WHERE id = $1", id)
	})
	return id
}

// seedExtraAgent inserts a second agent in the same tenant. Sets display_name
// explicitly so PGAgentStore.scanAgentRow can read it back without a NULL-scan
// failure (the schema makes display_name nullable, but scanAgentRow scans into
// a plain string — NULL → error → "agent not found" surfaces downstream).
func seedExtraAgent(t *testing.T, db *sql.DB, tenantID uuid.UUID, slugSuffix string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	agentKey := "agent-" + slugSuffix + "-" + id.String()[:8]
	_, err := db.Exec(
		`INSERT INTO agents (id, tenant_id, agent_key, display_name, agent_type, status, provider, model, owner_id)
		 VALUES ($1,$2,$3,$4,'predefined','active','test','test-model','test-owner')`,
		id, tenantID, agentKey, agentKey)
	if err != nil {
		t.Fatalf("seed extra agent: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM agents WHERE id = $1", id)
	})
	return id
}

// Scenario (a): tool_allow=NULL → resolver surfaces nil → downstream MCP gate
// keeps the agent's full whitelist open.
func TestChannelAgentRoutes_Integration_ScenarioA_NullToolAllowPassThrough(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgentID := seedTenantAgent(t, db)
	partnerAgent := seedExtraAgent(t, db, tenantID, "partner")
	channelID := seedChannelInstanceForRoutes(t, db, tenantID, defaultAgentID)

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	ctx := tenantCtx(tenantID)

	r := &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           partnerAgent,
		Name:              "DM → partner (inherit tools)",
		PeerKind:          "direct",
		MentionRequired:   false,
		Priority:          100,
		IsEnabled:         true,
		ToolAllow:         nil, // <-- inherit
	}
	if err := routeStore.Create(ctx, r); err != nil {
		t.Fatalf("create route: %v", err)
	}

	resolver := routing.NewAgentRouteResolver(routeStore, 0)
	agent, allow, matched, err := resolver.Resolve(ctx, channelID, "", "", "direct", routing.MediaKindText, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !matched || agent != partnerAgent {
		t.Fatalf("expected match to partner; got agent=%v matched=%v", agent, matched)
	}
	if allow != nil {
		t.Fatalf("nil route tool_allow must surface as nil (inherit); got %v", allow)
	}
}

// Scenario (b): tool_allow=["A","B"] narrows. Resolver hands the slice to the
// downstream gate (covered by unit test allowGate); here we only verify the
// slice round-trips correctly through PG (JSONB encoding + decode).
func TestChannelAgentRoutes_Integration_ScenarioB_ToolAllowNarrowing(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgentID := seedTenantAgent(t, db)
	channelID := seedChannelInstanceForRoutes(t, db, tenantID, defaultAgentID)

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	ctx := tenantCtx(tenantID)

	allow := []string{"generate_shortlink", "get_commission_for_url"}
	if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           defaultAgentID,
		Name:              "DM narrowed",
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
		ToolAllow:         &allow,
	}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	resolver := routing.NewAgentRouteResolver(routeStore, 0)
	_, got, matched, err := resolver.Resolve(ctx, channelID, "", "", "direct", routing.MediaKindText, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !matched {
		t.Fatal("expected match")
	}
	if len(got) != 2 || got[0] != "generate_shortlink" || got[1] != "get_commission_for_url" {
		t.Fatalf("tool_allow round-trip broke through PG JSONB: got %v", got)
	}
}

// Scenario (d): media_type=voice route catches voice messages while a sibling
// route with media_type=NULL catches everything else. Tie-break by priority.
func TestChannelAgentRoutes_Integration_ScenarioD_MediaTypeVoice(t *testing.T) {
	db := testDB(t)
	tenantID, textAgent := seedTenantAgent(t, db)
	voiceAgent := seedExtraAgent(t, db, tenantID, "voice")
	channelID := seedChannelInstanceForRoutes(t, db, tenantID, textAgent)

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	ctx := tenantCtx(tenantID)

	voiceMT := "voice"
	if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           voiceAgent,
		Name:              "DM voice → voice agent",
		PeerKind:          "direct",
		MediaType:         &voiceMT,
		Priority:          50, // higher precedence than text catch-all
		IsEnabled:         true,
	}); err != nil {
		t.Fatalf("create voice route: %v", err)
	}
	if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           textAgent,
		Name:              "DM any → text agent",
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}); err != nil {
		t.Fatalf("create text route: %v", err)
	}

	resolver := routing.NewAgentRouteResolver(routeStore, 0)

	// Voice inbound → voice agent.
	if a, _, m, _ := resolver.Resolve(ctx, channelID, "", "", "direct", routing.MediaKindVoice, false); !m || a != voiceAgent {
		t.Fatalf("voice should route to voiceAgent: agent=%v matched=%v", a, m)
	}
	// Text inbound → falls through to catch-all.
	if a, _, m, _ := resolver.Resolve(ctx, channelID, "", "", "direct", routing.MediaKindText, false); !m || a != textAgent {
		t.Fatalf("text should fall through to text agent: agent=%v matched=%v", a, m)
	}
}

// Cache invalidation E2E: store CRUD → resolver Invalidate → next Resolve
// re-reads from PG within the same process. Proves the wire-up that REST
// handlers depend on for "your route change is live immediately".
func TestChannelAgentRoutes_Integration_CacheInvalidationFlowsThrough(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgentID := seedTenantAgent(t, db)
	otherAgent := seedExtraAgent(t, db, tenantID, "swap")
	channelID := seedChannelInstanceForRoutes(t, db, tenantID, defaultAgentID)

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	resolver := routing.NewAgentRouteResolver(routeStore, time.Hour) // generous TTL so we MUST Invalidate
	ctx := tenantCtx(tenantID)

	// Step 1: route to defaultAgent.
	first := &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           defaultAgentID,
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}
	if err := routeStore.Create(ctx, first); err != nil {
		t.Fatalf("create first route: %v", err)
	}
	if a, _, m, _ := resolver.Resolve(ctx, channelID, "", "", "direct", routing.MediaKindText, false); !m || a != defaultAgentID {
		t.Fatalf("initial resolve: agent=%v matched=%v", a, m)
	}

	// Step 2: disable the first route at the store, then bump cache.
	if err := routeStore.Update(ctx, first.ID, map[string]any{"is_enabled": false}); err != nil {
		t.Fatalf("disable first route: %v", err)
	}
	// Without Invalidate the resolver would still hit cache and return defaultAgent.
	resolver.Invalidate(channelID)

	if _, _, m, _ := resolver.Resolve(ctx, channelID, "", "", "direct", routing.MediaKindText, false); m {
		t.Fatal("disabled route must no longer match after Invalidate")
	}

	// Step 3: insert a route to a different agent; cache must reflect after Invalidate.
	if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           otherAgent,
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}); err != nil {
		t.Fatalf("create second route: %v", err)
	}
	resolver.Invalidate(channelID)
	if a, _, m, _ := resolver.Resolve(ctx, channelID, "", "", "direct", routing.MediaKindText, false); !m || a != otherAgent {
		t.Fatalf("after Invalidate expected otherAgent; got agent=%v matched=%v", a, m)
	}
}

// Unmatched fallback: no route covers the input → resolver returns matched=false
// so the channel handler falls back to channel_instances.agent_id.
func TestChannelAgentRoutes_Integration_NoMatchFallsBackToDefault(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgent := seedTenantAgent(t, db)
	channelID := seedChannelInstanceForRoutes(t, db, tenantID, defaultAgent)

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	ctx := tenantCtx(tenantID)

	// Only a group+mention route exists. DM should not match.
	if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           defaultAgent,
		PeerKind:          "group",
		MentionRequired:   true,
		Priority:          100,
		IsEnabled:         true,
	}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	resolver := routing.NewAgentRouteResolver(routeStore, 0)
	a, allow, matched, err := resolver.Resolve(ctx, channelID, "", "", "direct", routing.MediaKindText, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if matched || a != uuid.Nil || allow != nil {
		t.Fatalf("DM with only group route → must be unmatched; got agent=%v matched=%v allow=%v", a, matched, allow)
	}
}

// Sanity guard so the import doesn't drift if the test ever stops using ctx.
var _ = context.Background
