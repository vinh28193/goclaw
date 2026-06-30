-- channel_routing_affinity: sticky per-(channel, peer) agent binding so a
-- conversation continues with the same agent until the TTL window expires.
-- Sits on top of channel_agent_routes — resolver checks affinity FIRST, then
-- falls through to rule eval, then upserts affinity with the chosen agent.
--
-- Snapshot semantics: tool_allow is captured at bind time. If operator edits
-- the source route mid-conversation, the bound peer keeps the snapshot until
-- TTL — avoids mid-conversation tool drift. Next conversation re-evaluates.

CREATE TABLE channel_routing_affinity (
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    channel_instance_id UUID NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
    peer_id             VARCHAR(255) NOT NULL,
    agent_id            UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    tool_allow          JSONB NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, channel_instance_id, peer_id)
);

-- Hot read path: lookup by (channel, peer) filtering out expired entries.
CREATE INDEX idx_channel_routing_affinity_lookup
    ON channel_routing_affinity(channel_instance_id, peer_id, expires_at);

-- Cleanup helper: scan expired entries for periodic prune job.
CREATE INDEX idx_channel_routing_affinity_expired
    ON channel_routing_affinity(expires_at);
