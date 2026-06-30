-- Path 1: LLM intent classifier — opt-in column.
-- When set on a route, the resolver only matches if classifier (configured at
-- channel_instances.config.intent_classifier_prompt) returns the same label
-- for the inbound message. NULL = rule-only matching (legacy behavior).
-- Length 50 is generous for short labels like "billing", "support",
-- "tech_question" without bloating index size.

ALTER TABLE channel_agent_routes
ADD COLUMN intent VARCHAR(50) NULL;

-- Lookup: when a channel has classifier enabled, resolver filters routes by
-- intent. Composite index on (channel_instance_id, intent) speeds the per-channel
-- intent match without a full table scan.
CREATE INDEX idx_channel_agent_routes_channel_intent
    ON channel_agent_routes(channel_instance_id, intent)
    WHERE intent IS NOT NULL;
