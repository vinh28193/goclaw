-- channel_agent_routes: deterministic per-message agent dispatch for shared-bot multi-agent setups.
-- Resolver picks (agent_id, tool_allow) by matching (channel_instance_id, peer_kind, media_type, mention_required)
-- with tie-break priority ASC, created_at ASC. channel_instances.agent_id remains the default fallback.

CREATE TABLE channel_agent_routes (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v7(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    channel_instance_id UUID NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
    agent_id            UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    name                VARCHAR(100) NOT NULL DEFAULT '',
    peer_kind           VARCHAR(20) NOT NULL CHECK (peer_kind IN ('direct','group','supergroup')),
    media_type          VARCHAR(20) NULL CHECK (media_type IS NULL OR media_type IN ('text','voice','media')),
    mention_required    BOOLEAN NOT NULL DEFAULT false,
    priority            INTEGER NOT NULL DEFAULT 100,
    is_enabled          BOOLEAN NOT NULL DEFAULT true,
    tool_allow          JSONB NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Hot read path: resolver scans enabled routes for a channel ordered by priority.
CREATE INDEX idx_channel_agent_routes_channel_enabled_priority
    ON channel_agent_routes(channel_instance_id, is_enabled, priority);
-- Cross-instance tenant listing for REST API (phase 05 ListByTenant).
CREATE INDEX idx_channel_agent_routes_tenant_instance
    ON channel_agent_routes(tenant_id, channel_instance_id);
-- Reverse lookup: which routes target a given agent (audit + cascade impact preview).
CREATE INDEX idx_channel_agent_routes_agent
    ON channel_agent_routes(agent_id);
