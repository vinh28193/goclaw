package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

func (h *ChannelAgentRoutesHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.resolveChannel(w, r)
	if !ok {
		return
	}
	locale := store.LocaleFromContext(r.Context())

	var body agentRouteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidJSON))
		return
	}

	if body.AgentID == nil || *body.AgentID == uuid.Nil {
		writeError(w, http.StatusBadRequest, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "agent_id"))
		return
	}
	if body.PeerKind == nil || !validPeerKinds[*body.PeerKind] {
		writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "peer_kind must be direct|group|supergroup"))
		return
	}
	if body.MediaType != nil && !validMediaTypes[*body.MediaType] {
		writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "media_type must be text|voice|media or null"))
		return
	}
	if body.TargetKind != nil && !validTargetKinds[*body.TargetKind] {
		writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "target_kind must be agent or team"))
		return
	}
	if body.PeerID != nil && len(*body.PeerID) > 128 {
		writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "peer_id must be at most 128 characters"))
		return
	}

	// Default target_kind = "agent" for legacy clients that don't send it.
	targetKind := store.RouteTargetAgent
	if body.TargetKind != nil {
		targetKind = *body.TargetKind
	}

	// validateAgent runs ONLY when target_kind=agent. For team, agent_id refers
	// to agent_teams.id — different validation path (not enforced server-side
	// in v1; team membership integrity belongs to the team-orchestration layer).
	if targetKind == store.RouteTargetAgent {
		if err := h.validateAgent(r, *body.AgentID, inst.TenantID); err != nil {
			writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, err.Error()))
			return
		}
	}

	row := &store.ChannelAgentRouteData{
		TenantID:          inst.TenantID,
		ChannelInstanceID: inst.ID,
		AgentID:           *body.AgentID,
		Name:              ptrStringOr(body.Name, ""),
		PeerKind:          *body.PeerKind,
		MediaType:         body.MediaType,
		MentionRequired:   ptrBoolOr(body.MentionRequired, false),
		Priority:          ptrIntOr(body.Priority, 100),
		IsEnabled:         ptrBoolOr(body.IsEnabled, true),
		ToolAllow:         normalizeToolAllow(body.ToolAllow),
		Intent:            body.Intent,
		PeerID:            normalizePeerID(body.PeerID),
		TargetKind:        targetKind,
	}

	if err := h.routes.Create(r.Context(), row); err != nil {
		slog.Error("channel_agent_routes.create", "error", err, "channel_instance_id", inst.ID)
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, i18n.T(locale, i18n.MsgFailedToCreate, "agent route", "internal error"))
		return
	}

	h.invalidateCache(inst.ID)
	h.bustAffinity(r.Context(), inst.ID, row.PeerID)
	writeJSON(w, http.StatusCreated, toResponse(row))
}

func (h *ChannelAgentRoutesHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.resolveChannel(w, r)
	if !ok {
		return
	}
	locale := store.LocaleFromContext(r.Context())

	rid, err := uuid.Parse(r.PathValue("rid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidID, "route"))
		return
	}

	existing, err := h.routes.Get(r.Context(), rid)
	if err != nil || existing == nil || existing.ChannelInstanceID != inst.ID {
		writeError(w, http.StatusNotFound, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "route", rid.String()))
		return
	}
	// Snapshot the pre-update peer_id now — some store implementations return
	// a row that gets mutated in place by Update, so reading existing.PeerID
	// after the update call would silently observe the NEW value.
	oldPeerID := existing.PeerID

	var body agentRouteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidJSON))
		return
	}

	updates := map[string]any{}
	if body.Name != nil {
		updates["name"] = *body.Name
	}
	if body.AgentID != nil && *body.AgentID != uuid.Nil {
		if err := h.validateAgent(r, *body.AgentID, inst.TenantID); err != nil {
			writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, err.Error()))
			return
		}
		updates["agent_id"] = *body.AgentID
	}
	if body.PeerKind != nil {
		if !validPeerKinds[*body.PeerKind] {
			writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "peer_kind must be direct|group|supergroup"))
			return
		}
		updates["peer_kind"] = *body.PeerKind
	}
	if mediaTypePresent(body) {
		if body.MediaType != nil && !validMediaTypes[*body.MediaType] {
			writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "media_type must be text|voice|media or null"))
			return
		}
		updates["media_type"] = body.MediaType
	}
	if body.MentionRequired != nil {
		updates["mention_required"] = *body.MentionRequired
	}
	if body.Priority != nil {
		updates["priority"] = *body.Priority
	}
	if body.IsEnabled != nil {
		updates["is_enabled"] = *body.IsEnabled
	}
	if body.ToolAllow != nil {
		updates["tool_allow"] = normalizeToolAllow(body.ToolAllow)
	}
	if body.Intent != nil {
		// Empty string from operator means "clear intent" — store as NULL.
		if *body.Intent == "" {
			updates["intent"] = nil
		} else {
			updates["intent"] = *body.Intent
		}
	}
	if body.TargetKind != nil {
		if !validTargetKinds[*body.TargetKind] {
			writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "target_kind must be agent or team"))
			return
		}
		updates["target_kind"] = *body.TargetKind
	}
	if body.PeerID != nil {
		if len(*body.PeerID) > 128 {
			writeError(w, http.StatusUnprocessableEntity, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidRequest, "peer_id must be at most 128 characters"))
			return
		}
		// Empty string from operator means "clear peer pinning" — store as NULL.
		if strings.TrimSpace(*body.PeerID) == "" {
			updates["peer_id"] = nil
		} else {
			updates["peer_id"] = strings.TrimSpace(*body.PeerID)
		}
	}

	if len(updates) == 0 {
		writeError(w, http.StatusBadRequest, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidUpdates))
		return
	}

	if err := h.routes.Update(r.Context(), rid, updates); err != nil {
		slog.Error("channel_agent_routes.update", "error", err, "route_id", rid)
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, i18n.T(locale, i18n.MsgFailedToUpdate, "agent route", "internal error"))
		return
	}

	h.invalidateCache(inst.ID)
	h.bustAffinity(r.Context(), inst.ID, oldPeerID)
	h.bustAffinity(r.Context(), inst.ID, normalizePeerID(body.PeerID))
	refreshed, err := h.routes.Get(r.Context(), rid)
	if err != nil || refreshed == nil {
		// Update succeeded but read-back failed — return what we know.
		writeJSON(w, http.StatusOK, map[string]any{"id": rid.String(), "status": "updated"})
		return
	}
	writeJSON(w, http.StatusOK, toResponse(refreshed))
}

func (h *ChannelAgentRoutesHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.resolveChannel(w, r)
	if !ok {
		return
	}
	locale := store.LocaleFromContext(r.Context())

	rid, err := uuid.Parse(r.PathValue("rid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidID, "route"))
		return
	}

	existing, err := h.routes.Get(r.Context(), rid)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "route", rid.String()))
		return
	}
	if existing.ChannelInstanceID != inst.ID {
		writeError(w, http.StatusNotFound, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "route", rid.String()))
		return
	}

	if err := h.routes.Delete(r.Context(), rid); err != nil {
		slog.Error("channel_agent_routes.delete", "error", err, "route_id", rid)
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, i18n.T(locale, i18n.MsgFailedToDelete, "agent route", "internal error"))
		return
	}

	h.invalidateCache(inst.ID)
	h.bustAffinity(r.Context(), inst.ID, existing.PeerID)
	w.WriteHeader(http.StatusNoContent)
}

// invalidateCache is a no-op when no resolver was wired (tests, edge configs).
func (h *ChannelAgentRoutesHandler) invalidateCache(channelInstanceID uuid.UUID) {
	if h.invalidator == nil {
		return
	}
	h.invalidator.Invalidate(channelInstanceID)
}

// bustAffinity best-effort deletes the sticky binding for a peer so a freshly
// mutated peer-pinned route is honored immediately (resolver checks affinity
// BEFORE rules). Errors are logged, never surfaced — affinity is a cache.
func (h *ChannelAgentRoutesHandler) bustAffinity(ctx context.Context, channelInstanceID uuid.UUID, peerID *string) {
	if h.affinity == nil || peerID == nil || *peerID == "" {
		return
	}
	if err := h.affinity.Delete(ctx, channelInstanceID, *peerID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("channel_agent_routes: affinity bust failed",
			"channel_instance_id", channelInstanceID, "peer_id", *peerID, "err", err)
	}
}

// normalizePeerID trims whitespace and collapses an empty/whitespace-only
// value to nil so the store persists NULL rather than an empty string for
// "no peer pinning".
func normalizePeerID(p *string) *string {
	if p == nil {
		return nil
	}
	s := strings.TrimSpace(*p)
	if s == "" {
		return nil
	}
	return &s
}

// mediaTypePresent reports whether the JSON body carried media_type — needed
// to distinguish "set to null" (clear matcher) from "field absent".
//
// json.Decoder leaves *string=nil for both cases by default; we therefore
// re-decode the raw payload via json.RawMessage at the top of PATCH if we
// need to distinguish. For now PATCH treats nil pointer as "do not touch" and
// callers wishing to clear send body.MediaType = nil with another field
// present — best-effort signal until the next protocol bump.
//
// TODO(v2): switch to json.RawMessage delta to support explicit null clear.
func mediaTypePresent(body agentRouteRequest) bool {
	return body.MediaType != nil
}

func ptrStringOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func ptrBoolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func ptrIntOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}
