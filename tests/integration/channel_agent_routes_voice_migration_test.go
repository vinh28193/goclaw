//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/channels/routing"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
)

// Phase-07 spec scenario (e): boot-time `voice_agent_id` migration on live PG.
// Seeds a channel_instance with `voice_agent_id` set in its config JSON, then
// runs the migrator and asserts:
//   - exactly 1 route inserted with media_type=voice + the resolved agent.
//   - a second run is a no-op (idempotency).
//   - a config whose voice_agent_id refers to an unknown agent_key is skipped
//     with a WARN log (no crash, no row).

// inlineSeedChannelInstanceVoice inserts a channel_instance with config={"voice_agent_id":...}.
// Returns the channel UUID and registers a cleanup.
func inlineSeedChannelInstanceVoice(t *testing.T, tenantID, defaultAgent uuid.UUID, channelType, voiceAgentKey string) uuid.UUID {
	t.Helper()
	db := testDB(t)
	id := uuid.Must(uuid.NewV7())
	cfg, err := json.Marshal(map[string]any{"voice_agent_id": voiceAgentKey})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	now := time.Now()
	_, err = db.Exec(
		`INSERT INTO channel_instances (id, name, display_name, channel_type, agent_id,
		 config, enabled, created_by, created_at, updated_at, tenant_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, channelType+"/voice-"+id.String()[:8], "Voice Test", channelType, defaultAgent,
		cfg, true, "test", now, now, tenantID,
	)
	if err != nil {
		t.Fatalf("seed channel_instance with voice config: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM channel_agent_routes WHERE channel_instance_id = $1", id)
		db.Exec("DELETE FROM channel_instances WHERE id = $1", id)
	})
	return id
}

// voiceAgentKeyOf returns the agent_key string assigned by seedExtraAgent (and
// the underlying INSERT). Mirrors the slug pattern there so callers can derive
// the agent_key from the returned UUID + slug suffix without a separate lookup.
func voiceAgentKeyOf(slugSuffix string, id uuid.UUID) string {
	return "agent-" + slugSuffix + "-" + id.String()[:8]
}

func TestChannelAgentRoutes_Integration_VoiceMigration_CreatesRoute(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgentID := seedTenantAgent(t, db)

	// Reuse the proven seedExtraAgent helper (channel_agent_routes_test.go).
	voiceAgentID := seedExtraAgent(t, db, tenantID, "voice")
	voiceKey := voiceAgentKeyOf("voice", voiceAgentID)

	channelID := inlineSeedChannelInstanceVoice(t, tenantID, defaultAgentID, "telegram", voiceKey)

	instStore := pg.NewPGChannelInstanceStore(db, testEncryptionKey)
	routeStore := pg.NewPGChannelAgentRouteStore(db)
	agentStore := pg.NewPGAgentStore(db)

	ctx := context.Background() // migrator scopes per-instance internally
	scanned, created, err := routing.MigrateVoiceAgentIDs(ctx, instStore, routeStore, agentStore)
	if err != nil {
		t.Fatalf("MigrateVoiceAgentIDs: %v", err)
	}
	if scanned < 1 || created < 1 {
		t.Fatalf("expected scanned≥1, created≥1; got scanned=%d created=%d", scanned, created)
	}

	rows, err := routeStore.ListByChannelInstance(tenantCtx(tenantID), channelID)
	if err != nil {
		t.Fatalf("list routes after migration: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 migrated route; got %d", len(rows))
	}
	r := rows[0]
	if r.AgentID != voiceAgentID {
		t.Fatalf("migrated route agent_id = %v; want %v", r.AgentID, voiceAgentID)
	}
	if r.MediaType == nil || *r.MediaType != routing.MediaKindVoice {
		t.Fatalf("migrated route media_type = %v; want voice", r.MediaType)
	}
	if r.PeerKind != "direct" {
		t.Fatalf("migrated route peer_kind = %q; want direct", r.PeerKind)
	}
	if !r.IsEnabled {
		t.Fatal("migrated route must be enabled")
	}
	if r.Priority != 50 {
		t.Fatalf("migrated route priority = %d; want 50", r.Priority)
	}
}

func TestChannelAgentRoutes_Integration_VoiceMigration_Idempotent(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgentID := seedTenantAgent(t, db)

	voiceAgentID := seedExtraAgent(t, db, tenantID, "voiceidempot")
	voiceKey := voiceAgentKeyOf("voiceidempot", voiceAgentID)

	channelID := inlineSeedChannelInstanceVoice(t, tenantID, defaultAgentID, "feishu", voiceKey)

	instStore := pg.NewPGChannelInstanceStore(db, testEncryptionKey)
	routeStore := pg.NewPGChannelAgentRouteStore(db)
	agentStore := pg.NewPGAgentStore(db)
	ctx := context.Background()

	// 1st run: 1 created.
	if _, c, err := routing.MigrateVoiceAgentIDs(ctx, instStore, routeStore, agentStore); err != nil {
		t.Fatalf("1st run: %v", err)
	} else if c < 1 {
		t.Fatalf("1st run should create ≥1 route; got %d", c)
	}

	rows1, _ := routeStore.ListByChannelInstance(tenantCtx(tenantID), channelID)
	if len(rows1) != 1 {
		t.Fatalf("after 1st run want 1 row; got %d", len(rows1))
	}

	// 2nd run: must NOT add another row.
	if _, _, err := routing.MigrateVoiceAgentIDs(ctx, instStore, routeStore, agentStore); err != nil {
		t.Fatalf("2nd run: %v", err)
	}
	rows2, _ := routeStore.ListByChannelInstance(tenantCtx(tenantID), channelID)
	if len(rows2) != 1 {
		t.Fatalf("after 2nd run still want 1 row (idempotent); got %d", len(rows2))
	}
}

func TestChannelAgentRoutes_Integration_VoiceMigration_UnknownAgentSkipsWithoutCrash(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgentID := seedTenantAgent(t, db)

	// voice_agent_id references an agent_key that doesn't exist for this tenant.
	channelID := inlineSeedChannelInstanceVoice(t, tenantID, defaultAgentID, "zalo_oa", "no-such-agent-key-xyz")

	instStore := pg.NewPGChannelInstanceStore(db, testEncryptionKey)
	routeStore := pg.NewPGChannelAgentRouteStore(db)
	agentStore := pg.NewPGAgentStore(db)

	scanned, created, err := routing.MigrateVoiceAgentIDs(context.Background(), instStore, routeStore, agentStore)
	if err != nil {
		t.Fatalf("MigrateVoiceAgentIDs should NOT error on unknown agent_key (logs WARN, continues); got err=%v", err)
	}
	if scanned < 1 {
		t.Fatalf("scanned should be ≥1; got %d", scanned)
	}
	_ = created // value depends on whether other tests in the same shared-DB run added rows; only the per-channel assertion below matters

	rows, _ := routeStore.ListByChannelInstance(tenantCtx(tenantID), channelID)
	if len(rows) != 0 {
		t.Fatalf("unknown voice_agent_id should NOT create a route; got %d rows", len(rows))
	}
}
