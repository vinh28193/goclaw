-- Path 4: agent-team target.
-- Routes can now point to either a single agent OR an agent team (existing
-- internal/orchestration/ + agent_teams table). Backward compat: existing rows
-- get target_kind='agent' via the DEFAULT. agent_id column reinterpreted as
-- the target id when target_kind='team' (saves an ALTER for renaming, keeps
-- the FK loose — channel handler validates target existence per kind).

ALTER TABLE channel_agent_routes
ADD COLUMN target_kind VARCHAR(20) NOT NULL DEFAULT 'agent'
CHECK (target_kind IN ('agent','team'));

-- Drop the FK constraint on agent_id because for team rows the id refers to
-- agent_teams.id, NOT agents.id. Validation happens at the application layer
-- via the route handler (Create/Update reject mismatched target_kind+id).
ALTER TABLE channel_agent_routes
DROP CONSTRAINT IF EXISTS channel_agent_routes_agent_id_fkey;

-- Index for reverse lookup: which routes target a specific agent OR team.
CREATE INDEX IF NOT EXISTS idx_channel_agent_routes_target
    ON channel_agent_routes(target_kind, agent_id);
