DROP INDEX IF EXISTS idx_channel_agent_routes_target;
ALTER TABLE channel_agent_routes DROP COLUMN IF EXISTS target_kind;
-- Restore agent_id FK constraint
ALTER TABLE channel_agent_routes
ADD CONSTRAINT channel_agent_routes_agent_id_fkey
FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
