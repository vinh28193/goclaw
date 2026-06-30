//go:build integration

package integration

import (
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/channels/routing"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
)

// Phase-07 spec scenario (c): same channel_agent_routes table, same resolver,
// serves Telegram + Feishu + Zalo OA channel instances. The decision logic is
// channel-agnostic — these tests prove that:
//
//   1. A tenant can have one channel_instance per channel_type, each with its
//      own route set; resolver picks the correct agent per channel_instance.
//   2. Cache is isolated per channel_instance — invalidating Telegram does NOT
//      flush Feishu / Zalo cached entries.
//   3. The migrator processes all three channel types uniformly.
//
// Synthetic Telegram/Feishu/Zalo inbound generation lives in the channel
// packages' own unit tests; this file targets the data + resolver contract
// the channel layer relies on.

func TestChannelAgentRoutes_Integration_MultiChannel_PerChannelRouting(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgent := seedTenantAgent(t, db)

	// 1 channel_instance per supported channel_type.
	tgChannel := seedChannelInstanceForType(t, db, tenantID, defaultAgent, "telegram")
	fsChannel := seedChannelInstanceForType(t, db, tenantID, defaultAgent, "feishu")
	zaloChannel := seedChannelInstanceForType(t, db, tenantID, defaultAgent, "zalo_oa")

	// Distinct route agents per channel so we can prove resolver returns the
	// channel-specific row (NOT a cross-channel leak).
	tgAgent := seedExtraAgent(t, db, tenantID, "tg")
	fsAgent := seedExtraAgent(t, db, tenantID, "fs")
	zaloAgent := seedExtraAgent(t, db, tenantID, "zalo")

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	ctx := tenantCtx(tenantID)

	for ch, agent := range map[uuid.UUID]uuid.UUID{
		tgChannel:   tgAgent,
		fsChannel:   fsAgent,
		zaloChannel: zaloAgent,
	} {
		if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
			ChannelInstanceID: ch,
			AgentID:           agent,
			PeerKind:          "direct",
			Priority:          100,
			IsEnabled:         true,
		}); err != nil {
			t.Fatalf("create route for channel %s: %v", ch, err)
		}
	}

	resolver := routing.NewAgentRouteResolver(routeStore, 0)

	// Telegram channel resolves to tgAgent.
	if a, _, m, _ := resolver.Resolve(ctx, tgChannel, "", "", "direct", routing.MediaKindText, false); !m || a != tgAgent {
		t.Fatalf("telegram → tgAgent expected; got agent=%v matched=%v", a, m)
	}
	// Feishu channel resolves to fsAgent.
	if a, _, m, _ := resolver.Resolve(ctx, fsChannel, "", "", "direct", routing.MediaKindText, false); !m || a != fsAgent {
		t.Fatalf("feishu → fsAgent expected; got agent=%v matched=%v", a, m)
	}
	// Zalo OA channel resolves to zaloAgent.
	if a, _, m, _ := resolver.Resolve(ctx, zaloChannel, "", "", "direct", routing.MediaKindText, false); !m || a != zaloAgent {
		t.Fatalf("zalo → zaloAgent expected; got agent=%v matched=%v", a, m)
	}
}

// Cache must be partitioned per channel_instance — invalidating one channel
// must NOT evict the other channels' entries. Critical for production where
// many channels share a single resolver instance.
func TestChannelAgentRoutes_Integration_MultiChannel_CacheIsolation(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgent := seedTenantAgent(t, db)
	tgChannel := seedChannelInstanceForType(t, db, tenantID, defaultAgent, "telegram")
	fsChannel := seedChannelInstanceForType(t, db, tenantID, defaultAgent, "feishu")

	tgAgent := seedExtraAgent(t, db, tenantID, "tgcache")
	fsAgent := seedExtraAgent(t, db, tenantID, "fscache")

	routeStore := pg.NewPGChannelAgentRouteStore(db)
	ctx := tenantCtx(tenantID)

	if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
		ChannelInstanceID: tgChannel, AgentID: tgAgent, PeerKind: "direct", Priority: 100, IsEnabled: true,
	}); err != nil {
		t.Fatalf("create tg route: %v", err)
	}
	if err := routeStore.Create(ctx, &store.ChannelAgentRouteData{
		ChannelInstanceID: fsChannel, AgentID: fsAgent, PeerKind: "direct", Priority: 100, IsEnabled: true,
	}); err != nil {
		t.Fatalf("create fs route: %v", err)
	}

	resolver := routing.NewAgentRouteResolver(routeStore, 0)

	// Warm both cache entries.
	if a, _, _, _ := resolver.Resolve(ctx, tgChannel, "", "", "direct", routing.MediaKindText, false); a != tgAgent {
		t.Fatalf("tg warm: agent=%v want %v", a, tgAgent)
	}
	if a, _, _, _ := resolver.Resolve(ctx, fsChannel, "", "", "direct", routing.MediaKindText, false); a != fsAgent {
		t.Fatalf("fs warm: agent=%v want %v", a, fsAgent)
	}

	// Mutate ONLY the Telegram route at the store, then Invalidate ONLY tg's cache.
	newTgAgent := seedExtraAgent(t, db, tenantID, "tgnew")
	rows, _ := routeStore.ListByChannelInstance(ctx, tgChannel)
	if err := routeStore.Update(ctx, rows[0].ID, map[string]any{"agent_id": newTgAgent}); err != nil {
		t.Fatalf("update tg route: %v", err)
	}
	resolver.Invalidate(tgChannel)

	// Telegram now resolves to newTgAgent — cache was flushed for this channel.
	if a, _, _, _ := resolver.Resolve(ctx, tgChannel, "", "", "direct", routing.MediaKindText, false); a != newTgAgent {
		t.Fatalf("tg post-invalidate: agent=%v want %v", a, newTgAgent)
	}
	// Feishu is UNTOUCHED — still resolves to original fsAgent (cache not evicted).
	if a, _, _, _ := resolver.Resolve(ctx, fsChannel, "", "", "direct", routing.MediaKindText, false); a != fsAgent {
		t.Fatalf("fs after tg invalidate must NOT be evicted: agent=%v want %v", a, fsAgent)
	}
}

// Migrator handles channel_instances of any channel_type uniformly — voice_agent_id
// is a channel-config field, not channel-type-specific logic.
func TestChannelAgentRoutes_Integration_MultiChannel_VoiceMigrationAcrossTypes(t *testing.T) {
	db := testDB(t)
	tenantID, defaultAgent := seedTenantAgent(t, db)

	// One voice agent shared across all three channel types.
	voiceAgent := seedExtraAgent(t, db, tenantID, "mcvoice")
	voiceKey := voiceAgentKeyOf("mcvoice", voiceAgent)

	tgChannel := inlineSeedChannelInstanceVoice(t, tenantID, defaultAgent, "telegram", voiceKey)
	fsChannel := inlineSeedChannelInstanceVoice(t, tenantID, defaultAgent, "feishu", voiceKey)
	zaloChannel := inlineSeedChannelInstanceVoice(t, tenantID, defaultAgent, "zalo_oa", voiceKey)

	instStore := pg.NewPGChannelInstanceStore(db, testEncryptionKey)
	routeStore := pg.NewPGChannelAgentRouteStore(db)
	agentStore := pg.NewPGAgentStore(db)

	if _, created, err := routing.MigrateVoiceAgentIDs(tenantCtx(tenantID), instStore, routeStore, agentStore); err != nil {
		t.Fatalf("migrate: %v", err)
	} else if created < 3 {
		// Other tests in this run may have seeded their own channel_instances → created can be >3.
		t.Fatalf("expected ≥3 migrated routes for the 3 channels in this test; got %d", created)
	}

	// Each channel got its own route row.
	for name, ch := range map[string]uuid.UUID{"telegram": tgChannel, "feishu": fsChannel, "zalo_oa": zaloChannel} {
		rows, err := routeStore.ListByChannelInstance(tenantCtx(tenantID), ch)
		if err != nil {
			t.Fatalf("%s list: %v", name, err)
		}
		found := false
		for _, r := range rows {
			if r.AgentID == voiceAgent && r.MediaType != nil && *r.MediaType == routing.MediaKindVoice {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s channel should have voice route to voiceAgent after migration; rows=%+v", name, rows)
		}
	}
}

// seedChannelInstanceForType inserts a channel_instance of an explicit
// channel_type (telegram | feishu | zalo_oa). Cleanup includes routes.
// The `db` param is unused — we resolve via testDB(t) for consistency with
// the shared connection pool, but the signature mirrors sibling helpers.
func seedChannelInstanceForType(t *testing.T, _ interface{}, tenantID, defaultAgent uuid.UUID, channelType string) uuid.UUID {
	t.Helper()
	tdb := testDB(t)
	id := uuid.Must(uuid.NewV7())
	if _, err := tdb.Exec(
		`INSERT INTO channel_instances (id, name, display_name, channel_type, agent_id,
		 config, enabled, created_by, created_at, updated_at, tenant_id)
		 VALUES ($1,$2,$3,$4,$5,'{}'::jsonb, true,'test',NOW(),NOW(),$6)`,
		id, channelType+"/multi-"+id.String()[:8], "Multi "+channelType, channelType, defaultAgent, tenantID,
	); err != nil {
		t.Fatalf("seed %s channel_instance: %v", channelType, err)
	}
	t.Cleanup(func() {
		tdb.Exec("DELETE FROM channel_agent_routes WHERE channel_instance_id = $1", id)
		tdb.Exec("DELETE FROM channel_instances WHERE id = $1", id)
	})
	return id
}
