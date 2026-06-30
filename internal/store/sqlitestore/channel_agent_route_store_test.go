//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestSQLiteChannelAgentRouteStore_CreateGetRoundtrip(t *testing.T) {
	s, ctx, _, channelID, agentID := newTestRouteStore(t)

	voice := "voice"
	allow := []string{"generate_shortlink", "get_commission_for_url"}
	r := &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           agentID,
		Name:              "partner-voice",
		PeerKind:          "group",
		MediaType:         &voice,
		MentionRequired:   true,
		Priority:          50,
		IsEnabled:         true,
		ToolAllow:         &allow,
	}
	if err := s.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.ID == uuid.Nil || r.TenantID != store.MasterTenantID {
		t.Fatalf("expected ID + tenant derived, got id=%v tenant=%v", r.ID, r.TenantID)
	}

	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AgentID != agentID || got.PeerKind != "group" || !got.MentionRequired ||
		!got.IsEnabled || got.Priority != 50 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got.MediaType == nil || *got.MediaType != "voice" {
		t.Fatalf("media_type lost: %v", got.MediaType)
	}
	if got.ToolAllow == nil || len(*got.ToolAllow) != 2 {
		t.Fatalf("tool_allow lost: %v", got.ToolAllow)
	}
}

func TestSQLiteChannelAgentRouteStore_NilToolAllowAndMediaType(t *testing.T) {
	s, ctx, _, channelID, agentID := newTestRouteStore(t)

	r := &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           agentID,
		Name:              "default",
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}
	if err := s.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MediaType != nil {
		t.Fatalf("media_type should be nil, got %v", *got.MediaType)
	}
	if got.ToolAllow != nil {
		t.Fatalf("tool_allow should be nil (inherit), got %v", *got.ToolAllow)
	}
}

func TestSQLiteChannelAgentRouteStore_ListOrderingPriorityThenCreatedAt(t *testing.T) {
	s, ctx, _, channelID, agentID := newTestRouteStore(t)

	// Insert three routes: priorities 10, 100, 100 (latter two with explicit created_at).
	r1 := &store.ChannelAgentRouteData{ChannelInstanceID: channelID, AgentID: agentID, Name: "high", PeerKind: "direct", Priority: 10, IsEnabled: true}
	r2 := &store.ChannelAgentRouteData{ChannelInstanceID: channelID, AgentID: agentID, Name: "low-first", PeerKind: "direct", Priority: 100, IsEnabled: true}
	r3 := &store.ChannelAgentRouteData{ChannelInstanceID: channelID, AgentID: agentID, Name: "low-second", PeerKind: "direct", Priority: 100, IsEnabled: true}

	if err := s.Create(ctx, r1); err != nil {
		t.Fatalf("Create r1: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := s.Create(ctx, r2); err != nil {
		t.Fatalf("Create r2: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := s.Create(ctx, r3); err != nil {
		t.Fatalf("Create r3: %v", err)
	}

	list, err := s.ListByChannelInstance(ctx, channelID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(list))
	}
	if list[0].Name != "high" {
		t.Fatalf("priority order broken: %v", list[0].Name)
	}
	if list[1].Name != "low-first" || list[2].Name != "low-second" {
		t.Fatalf("created_at tiebreak broken: %v / %v", list[1].Name, list[2].Name)
	}
}

func TestSQLiteChannelAgentRouteStore_TenantIsolation(t *testing.T) {
	s, ctxA, db, channelID, agentID := newTestRouteStore(t)

	r := &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           agentID,
		Name:              "tenant-A-route",
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}
	if err := s.Create(ctxA, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Other tenant context (different UUID) — should see nothing.
	otherTID := uuid.MustParse("01950000-0000-7000-8000-000000000999")
	ctxB := store.WithTenantID(context.Background(), otherTID)
	if _, err := s.Get(ctxB, r.ID); err == nil {
		t.Fatal("cross-tenant Get should fail")
	}
	listB, err := s.ListByChannelInstance(ctxB, channelID)
	if err != nil {
		t.Fatalf("List as other tenant: %v", err)
	}
	if len(listB) != 0 {
		t.Fatalf("expected zero routes for other tenant, got %d", len(listB))
	}
	_ = db
}

func TestSQLiteChannelAgentRouteStore_TenantMismatchRejected(t *testing.T) {
	s, _, _, channelID, agentID := newTestRouteStore(t)

	wrongTenant := uuid.MustParse("01950000-0000-7000-8000-000000000abc")
	r := &store.ChannelAgentRouteData{
		TenantID:          wrongTenant, // caller-supplied tenant disagreeing with channel's
		ChannelInstanceID: channelID,
		AgentID:           agentID,
		PeerKind:          "direct",
		Priority:          100,
		IsEnabled:         true,
	}
	ctxMaster := store.WithCrossTenant(context.Background())
	if err := s.Create(ctxMaster, r); err == nil {
		t.Fatal("expected tenant_id mismatch error")
	}
}

func TestSQLiteChannelAgentRouteStore_InvalidPeerKind(t *testing.T) {
	s, ctx, _, channelID, agentID := newTestRouteStore(t)
	r := &store.ChannelAgentRouteData{
		ChannelInstanceID: channelID,
		AgentID:           agentID,
		PeerKind:          "channel",
		Priority:          100,
		IsEnabled:         true,
	}
	if err := s.Create(ctx, r); err == nil {
		t.Fatal("expected invalid peer_kind error")
	}
}

func TestSQLiteChannelAgentRouteStore_UpdateAndDelete(t *testing.T) {
	s, ctx, _, channelID, agentID := newTestRouteStore(t)

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

	newAllow := []string{"only_this"}
	if err := s.Update(ctx, r.ID, map[string]any{
		"priority":   42,
		"is_enabled": false,
		"tool_allow": &newAllow,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Priority != 42 || got.IsEnabled {
		t.Fatalf("update mismatch: %+v", got)
	}
	if got.ToolAllow == nil || len(*got.ToolAllow) != 1 || (*got.ToolAllow)[0] != "only_this" {
		t.Fatalf("tool_allow update mismatch: %v", got.ToolAllow)
	}

	if err := s.Delete(ctx, r.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, r.ID); err == nil {
		t.Fatal("Get after Delete should fail")
	}
}

// --- test helpers ---

func newTestRouteStore(t *testing.T) (*SQLiteChannelAgentRouteStore, context.Context, *sql.DB, uuid.UUID, uuid.UUID) {
	t.Helper()

	db, err := OpenDB(filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	tenantID := store.MasterTenantID
	channelID := uuid.Must(uuid.NewV7())
	agentID := uuid.Must(uuid.NewV7())
	now := time.Now()

	if _, err := db.Exec(
		`INSERT INTO agents (id, agent_key, owner_id, provider, model, tenant_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		agentID, "test-agent", "test-owner", "openrouter", "test/model", tenantID, now, now,
	); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO channel_instances (id, name, display_name, channel_type, agent_id,
		 credentials, config, enabled, created_by, created_at, updated_at, tenant_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		channelID, "telegram/test", "Test", "telegram", agentID,
		nil, "{}", 1, "test", now, now, tenantID,
	); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	ctx := store.WithTenantID(context.Background(), tenantID)
	return NewSQLiteChannelAgentRouteStore(db), ctx, db, channelID, agentID
}
