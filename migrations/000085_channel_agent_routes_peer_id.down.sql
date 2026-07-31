DROP INDEX IF EXISTS idx_channel_agent_routes_channel_peer;
ALTER TABLE channel_agent_routes DROP COLUMN IF EXISTS peer_id;
