// Package routing — voice_agent_id_migrator one-shot migration helper.
//
// Legacy channel instance config carries an optional `voice_agent_id` string
// (Telegram + Feishu). Phase 04 of the multi-agent routing plan removes the
// handler-level voice override and consolidates voice routing into
// channel_agent_routes (media_type=voice). This migrator scans every channel
// instance, reads voice_agent_id from its config JSON, resolves it to an
// agents.id (via agent_key lookup scoped to the instance's tenant), and
// inserts a route row — idempotent across reboots.
package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// AgentKeyResolver narrows the AgentStore surface used by the migrator. Only
// GetByKey is consumed; using a small interface keeps the migrator easy to
// test in isolation.
type AgentKeyResolver interface {
	GetByKey(ctx context.Context, agentKey string) (*store.AgentData, error)
}

// MigrateVoiceAgentIDs inserts a `media_type=voice` route row for every channel
// instance whose config carries a non-empty voice_agent_id. Idempotent: rows
// already present (same channel_instance_id + media_type=voice + agent_id) are
// skipped. Errors on individual instances are logged but do NOT abort the
// migration — the caller can re-run safely.
//
// Returns (instancesScanned, routesCreated, error). Error is non-nil only when
// the channel_instances listing itself fails.
func MigrateVoiceAgentIDs(
	ctx context.Context,
	instances store.ChannelInstanceStore,
	routes store.ChannelAgentRouteStore,
	agents AgentKeyResolver,
) (int, int, error) {
	if instances == nil || routes == nil || agents == nil {
		return 0, 0, errors.New("migrate voice agent ids: nil store dependency")
	}
	// ListAllInstances (not ListAllEnabled): even disabled instances should be
	// migrated so re-enabling doesn't silently bring back legacy voice routing.
	list, err := instances.ListAllInstances(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("list channel instances: %w", err)
	}
	scanned := 0
	created := 0
	for _, inst := range list {
		scanned++
		voiceKey := extractVoiceAgentKey(inst.Config)
		if voiceKey == "" {
			continue
		}
		// Scope agent lookup to the instance's tenant — voice_agent_id is an
		// agent_key string, NOT a UUID, so collisions across tenants are possible.
		tctx := store.WithTenantID(ctx, inst.TenantID)
		agent, err := agents.GetByKey(tctx, voiceKey)
		if err != nil || agent == nil {
			slog.Warn("voice_agent_id_migration: agent not found, skipping",
				"channel_instance", inst.Name, "tenant_id", inst.TenantID,
				"voice_agent_id", voiceKey, "err", err)
			continue
		}
		// Idempotency: skip if a matching voice route already exists.
		existing, err := routes.ListByChannelInstance(tctx, inst.ID)
		if err != nil {
			slog.Warn("voice_agent_id_migration: list existing routes failed, skipping",
				"channel_instance", inst.Name, "err", err)
			continue
		}
		if hasVoiceRouteFor(existing, agent.ID) {
			continue
		}
		voiceKindStr := MediaKindVoice
		route := &store.ChannelAgentRouteData{
			TenantID:          inst.TenantID,
			ChannelInstanceID: inst.ID,
			AgentID:           agent.ID,
			Name:              "Legacy VoiceAgentID migration",
			PeerKind:          "direct",
			MediaType:         &voiceKindStr,
			MentionRequired:   false,
			Priority:          50,
			IsEnabled:         true,
		}
		if err := routes.Create(tctx, route); err != nil {
			slog.Warn("voice_agent_id_migration: create route failed",
				"channel_instance", inst.Name, "voice_agent_id", voiceKey, "err", err)
			continue
		}
		created++
	}
	if scanned > 0 {
		slog.Info("voice_agent_id_migration",
			"instances_scanned", scanned, "routes_created", created)
	}
	return scanned, created, nil
}

// extractVoiceAgentKey reads the legacy voice_agent_id string from a channel
// instance's Config JSON. Returns "" when the key is absent, blank, or the
// config is malformed (callers tolerate malformed config — log+skip).
func extractVoiceAgentKey(cfg json.RawMessage) string {
	if len(cfg) == 0 {
		return ""
	}
	var probe struct {
		VoiceAgentID string `json:"voice_agent_id"`
	}
	if err := json.Unmarshal(cfg, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.VoiceAgentID)
}

// hasVoiceRouteFor returns true when the supplied route list already contains
// an enabled (or disabled — we don't re-add) voice route pointing to the same
// agent. Matching is exact on (media_type=voice, agent_id) regardless of
// peer_kind / mention_required so a user-customized voice route also blocks
// the migrator from inserting a duplicate.
func hasVoiceRouteFor(existing []store.ChannelAgentRouteData, agentID uuid.UUID) bool {
	for _, r := range existing {
		if r.MediaType == nil || *r.MediaType != MediaKindVoice {
			continue
		}
		if r.AgentID == agentID {
			return true
		}
	}
	return false
}
