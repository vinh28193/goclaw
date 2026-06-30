package store

import (
	"context"

	"github.com/google/uuid"
)

// ChannelAgentRouteData represents a routing rule that selects which agent
// handles an inbound message on a given channel instance.
//
// Routing match order (in resolver): is_enabled = true AND peer_kind matches
// AND (media_type IS NULL OR media_type matches) AND (mention_required = false OR mention present),
// then pick lowest priority, tie-break by created_at ASC.
//
// ToolAllow narrows the MCP tool whitelist for messages routed through this
// rule. nil = inherit the agent's full MCP whitelist; non-nil = restrict to
// the listed tool names (intersection with the agent's existing allowlist).
type ChannelAgentRouteData struct {
	BaseModel
	TenantID          uuid.UUID `json:"tenant_id" db:"tenant_id"`
	ChannelInstanceID uuid.UUID `json:"channel_instance_id" db:"channel_instance_id"`
	AgentID           uuid.UUID `json:"agent_id" db:"agent_id"`
	Name              string    `json:"name" db:"name"`
	PeerKind          string    `json:"peer_kind" db:"peer_kind"` // direct|group|supergroup
	MediaType         *string   `json:"media_type,omitempty" db:"media_type"`
	MentionRequired   bool      `json:"mention_required" db:"mention_required"`
	Priority          int       `json:"priority" db:"priority"`
	IsEnabled         bool      `json:"is_enabled" db:"is_enabled"`
	ToolAllow         *[]string `json:"tool_allow,omitempty" db:"tool_allow"`
	// Intent (Path 1): if set, route only matches when the channel-instance's
	// classifier labels the inbound message with this string. NULL = legacy
	// rule-only match (no classifier consultation).
	Intent *string `json:"intent,omitempty" db:"intent"`
	// TargetKind (Path 4): "agent" (default) or "team". When "team", AgentID
	// field is reinterpreted as the agent_teams.id to dispatch via
	// internal/orchestration. Legacy rows default to "agent".
	TargetKind string `json:"target_kind" db:"target_kind"`
}

// Route target kind constants.
const (
	RouteTargetAgent = "agent"
	RouteTargetTeam  = "team"
)

// ChannelAgentRouteStore manages routing rules between channel instances and agents.
type ChannelAgentRouteStore interface {
	// Create derives tenant_id from the parent channel_instance and persists the route.
	// If r.TenantID is set and disagrees with the channel's tenant, returns an error.
	Create(ctx context.Context, r *ChannelAgentRouteData) error
	Get(ctx context.Context, id uuid.UUID) (*ChannelAgentRouteData, error)
	Update(ctx context.Context, id uuid.UUID, updates map[string]any) error
	Delete(ctx context.Context, id uuid.UUID) error
	// ListByChannelInstance returns routes for a channel ordered by priority ASC, created_at ASC.
	ListByChannelInstance(ctx context.Context, channelInstanceID uuid.UUID) ([]ChannelAgentRouteData, error)
	// ListByTenant returns all routes visible under the current tenant scope.
	ListByTenant(ctx context.Context) ([]ChannelAgentRouteData, error)
}
