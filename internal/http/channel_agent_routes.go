package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// RouteCacheInvalidator narrows AgentRouteResolver to just the Invalidate hook
// so the handler can flip cached route lists without importing the routing
// package (avoids an import cycle when routing later depends on store).
type RouteCacheInvalidator interface {
	Invalidate(channelInstanceID uuid.UUID)
}

// ChannelAgentRoutesHandler exposes CRUD over `channel_agent_routes` for the
// channel-instance edit screen. All writes flip resolver cache.
type ChannelAgentRoutesHandler struct {
	routes      store.ChannelAgentRouteStore
	channels    store.ChannelInstanceStore
	agents      store.AgentStore
	invalidator RouteCacheInvalidator
}

// NewChannelAgentRoutesHandler builds the handler. `invalidator` may be nil —
// the handler then skips cache eviction (resolver TTL still catches up).
//
// The tenants store arg is currently unused at this layer (tenant-scope is
// enforced by resolveChannel's channel.tenant_id match) but kept as a param
// for symmetry with sibling channel-instance handlers and to leave room for
// owner-side tenant validation later.
func NewChannelAgentRoutesHandler(
	routes store.ChannelAgentRouteStore,
	channels store.ChannelInstanceStore,
	agents store.AgentStore,
	_ store.TenantStore,
	invalidator RouteCacheInvalidator,
) *ChannelAgentRoutesHandler {
	return &ChannelAgentRoutesHandler{
		routes:      routes,
		channels:    channels,
		agents:      agents,
		invalidator: invalidator,
	}
}

// RegisterRoutes mounts the 5 CRUD endpoints under
// /v1/channels/instances/{id}/agent-routes. Tenant context flows via auth
// middleware → store.TenantIDFromContext.
func (h *ChannelAgentRoutesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/channels/instances/{id}/agent-routes", h.auth(h.handleList))
	mux.HandleFunc("POST /v1/channels/instances/{id}/agent-routes", h.adminAuth(h.handleCreate))
	mux.HandleFunc("GET /v1/channels/instances/{id}/agent-routes/{rid}", h.auth(h.handleGet))
	mux.HandleFunc("PATCH /v1/channels/instances/{id}/agent-routes/{rid}", h.adminAuth(h.handleUpdate))
	mux.HandleFunc("DELETE /v1/channels/instances/{id}/agent-routes/{rid}", h.adminAuth(h.handleDelete))
}

func (h *ChannelAgentRoutesHandler) auth(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth("", next)
}

// adminAuth gates writes by RoleAdmin scope. Tenant-scope enforcement happens
// inside each write handler via resolveChannel — channel.tenant_id must match
// the caller's ctx tenant. Matches the convention in channel_instances.go.
func (h *ChannelAgentRoutesHandler) adminAuth(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(permissions.RoleAdmin, next)
}

// agentRouteRequest is the wire-shape for POST/PATCH.
// All fields pointer-typed so PATCH can distinguish "absent" from "zero value".
type agentRouteRequest struct {
	Name            *string    `json:"name"`
	AgentID         *uuid.UUID `json:"agent_id"`
	PeerKind        *string    `json:"peer_kind"`
	MediaType       *string    `json:"media_type"` // null or text|voice|media
	MentionRequired *bool      `json:"mention_required"`
	Priority        *int       `json:"priority"`
	IsEnabled       *bool      `json:"is_enabled"`
	ToolAllow       *[]string  `json:"tool_allow"` // nil = inherit
	Intent          *string    `json:"intent"`     // Path 1: classifier label; null = rule-only
	TargetKind      *string    `json:"target_kind"` // Path 4: "agent" (default) | "team"
}

type agentRouteResponse struct {
	ID                uuid.UUID `json:"id"`
	TenantID          uuid.UUID `json:"tenant_id"`
	ChannelInstanceID uuid.UUID `json:"channel_instance_id"`
	AgentID           uuid.UUID `json:"agent_id"`
	Name              string    `json:"name"`
	PeerKind          string    `json:"peer_kind"`
	MediaType         *string   `json:"media_type"`
	MentionRequired   bool      `json:"mention_required"`
	Priority          int       `json:"priority"`
	IsEnabled         bool      `json:"is_enabled"`
	ToolAllow         *[]string `json:"tool_allow"`
	Intent            *string   `json:"intent"`
	TargetKind        string    `json:"target_kind"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func toResponse(d *store.ChannelAgentRouteData) agentRouteResponse {
	kind := d.TargetKind
	if kind == "" {
		kind = store.RouteTargetAgent
	}
	return agentRouteResponse{
		ID:                d.ID,
		TenantID:          d.TenantID,
		ChannelInstanceID: d.ChannelInstanceID,
		AgentID:           d.AgentID,
		Name:              d.Name,
		PeerKind:          d.PeerKind,
		MediaType:         d.MediaType,
		MentionRequired:   d.MentionRequired,
		Priority:          d.Priority,
		IsEnabled:         d.IsEnabled,
		ToolAllow:         d.ToolAllow,
		Intent:            d.Intent,
		TargetKind:        kind,
		CreatedAt:         d.CreatedAt,
		UpdatedAt:         d.UpdatedAt,
	}
}

// validPeerKinds + validMediaTypes encode the small enum domains we accept on
// the wire. Backend stores the literal string; resolver matches by equality.
var (
	validPeerKinds   = map[string]bool{"direct": true, "group": true, "supergroup": true}
	validMediaTypes  = map[string]bool{"text": true, "voice": true, "media": true}
	validTargetKinds = map[string]bool{store.RouteTargetAgent: true, store.RouteTargetTeam: true}
)

// resolveChannel parses the {id} path value AND verifies it belongs to the
// tenant on r.Context(). Returns (channel, ok). On !ok an HTTP error response
// is already written.
func (h *ChannelAgentRoutesHandler) resolveChannel(w http.ResponseWriter, r *http.Request) (*store.ChannelInstanceData, bool) {
	locale := store.LocaleFromContext(r.Context())
	cid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgInvalidID, "channel"))
		return nil, false
	}
	inst, err := h.channels.Get(r.Context(), cid)
	if err != nil {
		writeError(w, http.StatusNotFound, protocol.ErrNotFound, i18n.T(locale, i18n.MsgInstanceNotFound))
		return nil, false
	}
	tid := store.TenantIDFromContext(r.Context())
	// Master scope (owner) sees all; tenant-scoped callers must own the channel.
	if !store.IsMasterScope(r.Context()) && inst.TenantID != tid {
		writeError(w, http.StatusForbidden, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgPermissionDenied, "channel"))
		return nil, false
	}
	return inst, true
}

func (h *ChannelAgentRoutesHandler) handleList(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.resolveChannel(w, r)
	if !ok {
		return
	}
	locale := store.LocaleFromContext(r.Context())
	rows, err := h.routes.ListByChannelInstance(r.Context(), inst.ID)
	if err != nil {
		slog.Error("channel_agent_routes.list", "error", err, "channel_instance_id", inst.ID)
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, i18n.T(locale, i18n.MsgFailedToList, "agent routes"))
		return
	}
	out := make([]agentRouteResponse, 0, len(rows))
	for i := range rows {
		out = append(out, toResponse(&rows[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": out, "total": len(out)})
}

func (h *ChannelAgentRoutesHandler) handleGet(w http.ResponseWriter, r *http.Request) {
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
	row, err := h.routes.Get(r.Context(), rid)
	if err != nil || row == nil {
		writeError(w, http.StatusNotFound, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "route", rid.String()))
		return
	}
	if row.ChannelInstanceID != inst.ID {
		writeError(w, http.StatusNotFound, protocol.ErrNotFound, i18n.T(locale, i18n.MsgNotFound, "route", rid.String()))
		return
	}
	writeJSON(w, http.StatusOK, toResponse(row))
}

// validateAgent confirms the supplied agent_id exists AND belongs to the same
// tenant as the channel instance. Defense-in-depth on top of the SQL filter.
func (h *ChannelAgentRoutesHandler) validateAgent(r *http.Request, agentID uuid.UUID, tenantID uuid.UUID) error {
	if h.agents == nil {
		return nil
	}
	agent, err := h.agents.GetByID(r.Context(), agentID)
	if err != nil || agent == nil {
		return errors.New("agent not found")
	}
	if agent.TenantID != tenantID {
		return errors.New("agent belongs to a different tenant")
	}
	return nil
}

// normalizeToolAllow collapses [] to nil. Callers send [] to mean "no tools";
// our convention treats nil as "inherit" and [] as the same — using both would
// confuse the resolver. Phase 03 spec: nil = inherit.
func normalizeToolAllow(in *[]string) *[]string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(*in))
	for _, s := range *in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return &out
}
