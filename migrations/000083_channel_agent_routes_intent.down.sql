DROP INDEX IF EXISTS idx_channel_agent_routes_channel_intent;
ALTER TABLE channel_agent_routes DROP COLUMN IF EXISTS intent;
