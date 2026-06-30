package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ChannelRoutingAffinityData is the sticky binding between a (channel, peer)
// pair and the agent that should keep handling that peer's messages until
// ExpiresAt elapses. ToolAllow snapshots the route's tool_allow at bind time
// — operator edits to the source route AFTER the binding don't leak into a
// live conversation (re-evaluated at next conversation).
type ChannelRoutingAffinityData struct {
	TenantID          uuid.UUID `json:"tenant_id" db:"tenant_id"`
	ChannelInstanceID uuid.UUID `json:"channel_instance_id" db:"channel_instance_id"`
	PeerID            string    `json:"peer_id" db:"peer_id"`
	AgentID           uuid.UUID `json:"agent_id" db:"agent_id"`
	ToolAllow         *[]string `json:"tool_allow,omitempty" db:"tool_allow"`
	ExpiresAt         time.Time `json:"expires_at" db:"expires_at"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}

// ChannelRoutingAffinityStore manages sticky bindings between peers and agents.
// All reads filter out expired entries at the SQL layer (expires_at > NOW()).
type ChannelRoutingAffinityStore interface {
	// Get returns the active binding for the (channel, peer) pair, or
	// (nil, sql.ErrNoRows) when no row exists OR the row exists but has
	// already expired.
	Get(ctx context.Context, channelInstanceID uuid.UUID, peerID string) (*ChannelRoutingAffinityData, error)
	// Upsert writes the binding with a fresh expires_at = now + ttl. If a row
	// already exists for the (tenant, channel, peer) it is overwritten —
	// agent_id and tool_allow are refreshed from the new binding (operator
	// route edits land at next conversation, not within an active one).
	Upsert(ctx context.Context, row *ChannelRoutingAffinityData) error
	// Delete removes a single binding (used when a tenant flushes sticky for
	// a specific peer, e.g. user requested handoff to human).
	Delete(ctx context.Context, channelInstanceID uuid.UUID, peerID string) error
	// DeletePeerForChannel evicts every binding for a channel — used when
	// operator wants to clear all stickiness after a route reorg.
	DeletePeerForChannel(ctx context.Context, channelInstanceID uuid.UUID) (int, error)
	// DeleteExpired prunes rows past their expires_at. Returns count.
	// Called by the periodic cleanup cron; lazy expiry is also enforced at
	// read time, so lapses are non-fatal.
	DeleteExpired(ctx context.Context, now time.Time) (int, error)
}
