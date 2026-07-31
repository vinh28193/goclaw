-- peer_id: optional exact-match against the resolver's peer identifier
-- (DM: chat_id; group: composite "chat_id:sender_id" — see telegram/handlers.go).
-- NULL = match any peer (legacy behavior). Used by affiliate-backend link_chat
-- to pin one Telegram DM to one per-tenant agent.
ALTER TABLE channel_agent_routes ADD COLUMN peer_id VARCHAR(128) NULL;

CREATE INDEX idx_channel_agent_routes_channel_peer
    ON channel_agent_routes(channel_instance_id, peer_id)
    WHERE peer_id IS NOT NULL;
