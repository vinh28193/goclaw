export interface ChannelInstanceData {
  id: string;
  name: string;
  display_name: string;
  channel_type: string;
  agent_id: string;
  config: Record<string, unknown> | null;
  enabled: boolean;
  is_default: boolean;
  has_credentials: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface ChannelRuntimeStatus {
  enabled: boolean;
  running: boolean;
  state?:
    | "registered"
    | "starting"
    | "healthy"
    | "degraded"
    | "failed"
    | "stopped";
  summary?: string;
  detail?: string;
  failure_kind?: "auth" | "config" | "network" | "unknown";
  retryable?: boolean;
  checked_at?: string;
  failure_count?: number;
  consecutive_failures?: number;
  first_failed_at?: string;
  last_failed_at?: string;
  last_healthy_at?: string;
  remediation?: {
    code: "reauth" | "open_credentials" | "open_advanced" | "check_network";
    headline: string;
    hint?: string;
    target?: "credentials" | "advanced" | "reauth" | "details";
  };
}

export interface ChannelInstanceInput {
  name: string;
  display_name?: string;
  channel_type: string;
  agent_id: string;
  credentials?: Record<string, unknown>;
  config?: Record<string, unknown>;
  enabled?: boolean;
}

export interface ChannelMemoryConfig {
  enabled: boolean;
  review_mode: boolean;
  interval_minutes: number;
  message_cap: number;
  retention_hours: number;
  allowed_types: string[];
  exclude_users?: string[];
  exclude_patterns?: string[];
  min_messages: number;
  group_only: boolean;
}

export interface ChannelMemoryExtractionRun {
  id: string;
  channel_instance_id: string;
  channel_name: string;
  history_key: string;
  trigger: string;
  status: "pending" | "running" | "completed" | "failed";
  message_count: number;
  redaction_count: number;
  item_count: number;
  error_message?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

export interface ChannelMemoryExtractionItem {
  id: string;
  run_id: string;
  item_type: string;
  summary: string;
  topics?: string[];
  entities?: string[];
  confidence: number;
  status: "pending_review" | "approved" | "rejected" | "written" | "deleted";
  approved_at?: string;
  rejected_at?: string;
  deleted_at?: string;
  written_at?: string;
  episodic_id?: string;
  created_at: string;
}

export interface ChannelMemoryStatus {
  config: ChannelMemoryConfig;
  last_run?: ChannelMemoryExtractionRun;
  pending_count: number;
  recent_items: ChannelMemoryExtractionItem[];
}

export interface ChannelContextData {
  scope_type: "channel" | "group" | "user" | "role";
  scope_key: string;
  display_name: string;
  source: string;
  live_members_supported: boolean;
  member_count?: number;
  last_seen_at?: string;
}

export interface ChannelContextMember {
  platform_id: string;
  user_id?: string;
  display_name?: string;
  username?: string;
  source: string;
  last_seen_at?: string;
}

export type AgentRoutePeerKind = "direct" | "group" | "supergroup";
export type AgentRouteMediaType = "text" | "voice" | "media" | null;
export type AgentRouteTargetKind = "agent" | "team";

export interface ChannelAgentRoute {
  id: string;
  tenant_id: string;
  channel_instance_id: string;
  agent_id: string;
  name: string;
  peer_kind: AgentRoutePeerKind;
  media_type: AgentRouteMediaType;
  mention_required: boolean;
  priority: number;
  is_enabled: boolean;
  tool_allow: string[] | null;
  intent: string | null;
  target_kind: AgentRouteTargetKind;
  created_at: string;
  updated_at: string;
}

export interface ChannelAgentRouteInput {
  name?: string;
  agent_id: string;
  peer_kind: AgentRoutePeerKind;
  media_type?: AgentRouteMediaType;
  mention_required?: boolean;
  priority?: number;
  is_enabled?: boolean;
  tool_allow?: string[] | null;
  intent?: string | null;
  target_kind?: AgentRouteTargetKind;
}

export interface ChannelCapability {
  type: "mcp_server" | "secure_cli";
  id: string;
  name: string;
  display_name?: string;
  enabled: boolean;
  source: string;
  tool_allow?: string[];
  tool_deny?: string[];
  credential_source: string;
  has_credential: boolean;
  context_grant_configured: boolean;
  context_credentials_configured: boolean;
}
