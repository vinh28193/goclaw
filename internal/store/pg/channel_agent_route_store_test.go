package pg

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func seedChannelInstance(t *testing.T, db *sql.DB, tenantID, agentID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	now := time.Now()
	if _, err := db.Exec(
		`INSERT INTO channel_instances (id, name, display_name, channel_type, agent_id,
		 config, enabled, created_by, created_at, updated_at, tenant_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT DO NOTHING`,
		id, "telegram/test-"+id.String()[:8], "Test", "telegram", agentID,
		[]byte("{}"), true, "test", now, now, tenantID,
	); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM channel_instances WHERE id=$1", id) })
	return id
}

func TestPGChannelAgentRouteStore_CreateGetRoundtrip(t *testing.T) {
	db := hooksTestDB(t)
	tenantID, agentID := seedTenantAndAgent(t, db)
	channelID := seedChannelInstance(t, db, tenantID, agentID)
	s := NewPGChannelAgentRouteStore(db)
	ctx := store.WithTenantID(context.Background(), tenantID)

	voice := "voice"
	allow := []string{"generate_shortlink"}
	r := &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           agentID,
		Name:              "voice-route",
		PeerKind:          "supergroup",
		MediaType:         &voice,
		MentionRequired:   true,
		Priority:          10,
		IsEnabled:         true,
		ToolAllow:         &allow,
	}
	if err := s.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.TenantID != tenantID {
		t.Fatalf("TenantID derivation failed: %v want %v", r.TenantID, tenantID)
	}

	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PeerKind != "supergroup" || !got.MentionRequired || got.MediaType == nil || *got.MediaType != "voice" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got.ToolAllow == nil || len(*got.ToolAllow) != 1 || (*got.ToolAllow)[0] != "generate_shortlink" {
		t.Fatalf("tool_allow lost: %v", got.ToolAllow)
	}
}

func TestPGChannelAgentRouteStore_ListPriorityCreatedAtTiebreak(t *testing.T) {
	db := hooksTestDB(t)
	tenantID, agentID := seedTenantAndAgent(t, db)
	channelID := seedChannelInstance(t, db, tenantID, agentID)
	s := NewPGChannelAgentRouteStore(db)
	ctx := store.WithTenantID(context.Background(), tenantID)

	r1 := &store.ChannelAgentRouteData{ChannelInstanceID: channelID, AgentID: agentID, Name: "high", PeerKind: "direct", Priority: 10, IsEnabled: true}
	r2 := &store.ChannelAgentRouteData{ChannelInstanceID: channelID, AgentID: agentID, Name: "low-first", PeerKind: "direct", Priority: 100, IsEnabled: true}
	r3 := &store.ChannelAgentRouteData{ChannelInstanceID: channelID, AgentID: agentID, Name: "low-second", PeerKind: "direct", Priority: 100, IsEnabled: true}
	if err := s.Create(ctx, r1); err != nil {
		t.Fatalf("Create r1: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := s.Create(ctx, r2); err != nil {
		t.Fatalf("Create r2: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := s.Create(ctx, r3); err != nil {
		t.Fatalf("Create r3: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM channel_agent_routes WHERE tenant_id=$1", tenantID) })

	list, err := s.ListByChannelInstance(ctx, channelID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 || list[0].Name != "high" || list[1].Name != "low-first" || list[2].Name != "low-second" {
		t.Fatalf("order broken: %+v", list)
	}
}

func TestPGChannelAgentRouteStore_TenantIsolation(t *testing.T) {
	db := hooksTestDB(t)
	tenantA, agentA := seedTenantAndAgent(t, db)
	tenantB, _ := seedTenantAndAgent(t, db)
	channelA := seedChannelInstance(t, db, tenantA, agentA)
	s := NewPGChannelAgentRouteStore(db)

	ctxA := store.WithTenantID(context.Background(), tenantA)
	r := &store.ChannelAgentRouteData{
		ChannelInstanceID: channelA,
		AgentID:           agentA,
		Name:              "A-route",
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}
	if err := s.Create(ctxA, r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM channel_agent_routes WHERE tenant_id=$1", tenantA) })

	ctxB := store.WithTenantID(context.Background(), tenantB)
	listB, err := s.ListByChannelInstance(ctxB, channelA)
	if err != nil {
		t.Fatalf("List as B: %v", err)
	}
	if len(listB) != 0 {
		t.Fatalf("cross-tenant leak: %d routes visible", len(listB))
	}
}

func TestPGChannelAgentRouteStore_TenantMismatchRejected(t *testing.T) {
	db := hooksTestDB(t)
	tenantID, agentID := seedTenantAndAgent(t, db)
	channelID := seedChannelInstance(t, db, tenantID, agentID)
	s := NewPGChannelAgentRouteStore(db)

	wrong := uuid.Must(uuid.NewV7())
	r := &store.ChannelAgentRouteData{
		TenantID:          wrong,
		ChannelInstanceID: channelID,
		AgentID:           agentID,
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}
	if err := s.Create(store.WithCrossTenant(context.Background()), r); err == nil {
		t.Fatal("expected tenant mismatch error")
	}
}

func TestPGChannelAgentRouteStore_DeleteCascadeWithChannel(t *testing.T) {
	db := hooksTestDB(t)
	tenantID, agentID := seedTenantAndAgent(t, db)
	channelID := seedChannelInstance(t, db, tenantID, agentID)
	s := NewPGChannelAgentRouteStore(db)
	ctx := store.WithTenantID(context.Background(), tenantID)

	r := &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           agentID,
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}
	if err := s.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := db.Exec("DELETE FROM channel_instances WHERE id=$1", channelID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}

	if _, err := s.Get(ctx, r.ID); err == nil {
		t.Fatal("expected route cascade-deleted with channel")
	}
}
