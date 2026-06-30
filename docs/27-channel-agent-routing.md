# Channel Agent Routing

One bot per channel (Telegram / Feishu / Zalo) can dispatch to N backend agents based on deterministic match rules — without sharing tools across agents.

## Concept

Single shared bot → multiple agents. Goclaw chooses the target `agent_id` per inbound message based on the `channel_agent_routes` table; no match → falls back to `channel_instances.agent_id`.

### Match conditions

| Field | Description |
|---|---|
| `peer_kind` | `direct` (DM) / `group` / `supergroup` — required |
| `media_type` | `null` (any) / `text` / `voice` / `media` |
| `mention_required` | only matches when the bot is @-mentioned in the inbound |

Routes are evaluated by **`priority ASC, created_at ASC`** — first match wins. `priority` is operator-defined; lower number = higher precedence.

### Per-route `tool_allow`

Each route may narrow the MCP tool whitelist for messages routed through it.

- `null` → inherit the agent's full MCP whitelist (no extra restriction).
- `["A", "B"]` → only those tools are exposed via `list_tools` and only those calls are accepted; the agent loop rejects any other tool name BEFORE calling backend.

Backend's MCP-key whitelist remains the outer perimeter — `tool_allow` is defense-in-depth on the channel side.

## Setup example: partner DM + admin group

```
POST /v1/channels/instances/{id}/agent-routes
{
  "name": "Admin group commands",
  "agent_id": "<admin-agent-uuid>",
  "peer_kind": "group",
  "mention_required": true,
  "priority": 50,
  "tool_allow": ["broadcast", "list_groups"]
}

POST /v1/channels/instances/{id}/agent-routes
{
  "name": "Partner DM",
  "agent_id": "<partner-agent-uuid>",
  "peer_kind": "direct",
  "priority": 100
}
```

Inbound DM → matches second route → partner agent (inherits full tool whitelist).
Inbound group + bot mention → matches first route → admin agent, narrowed to `broadcast` + `list_groups` only.
Inbound group without mention → no match → default agent on the channel instance.

## Cache invalidation

The resolver caches per-channel route lists in-process (default TTL 30s). REST mutations (`POST/PATCH/DELETE`) call `Invalidate(channelInstanceID)` automatically so the next inbound sees the new rule within milliseconds. No restart required.

### Intent-based routing (LLM classifier)

Opt-in layer for content-aware routing. When a route has `intent VARCHAR(50)` set, the resolver invokes a configured `IntentClassifier` (typically wrapping a cheap LLM call) per inbound to label the message. The route then matches only when the classifier returns the same label.

Gating logic (cost discipline):
- If NO route on the channel has `intent`, the classifier is **never called** — pure rule eval.
- If `messageText` is empty (media-only inbound), classifier is **skipped**.
- Wrap with `CachedIntentClassifier` (TTL 60s) to dedupe identical messages.

Failure modes:
- Classifier returns error → log WARN, treat intent as `""` (unknown) → routes with non-null intent don't match → null-intent fallback route catches everything.
- Classifier returns label not matching any route → null-intent fallback route catches everything.

Setup:
- Set `intent` on a route via REST PATCH or UI.
- Call `resolver.SetIntentClassifier(yourClassifier)` once at boot (typically wired in `cmd/gateway.go`).
- Use the `StaticIntentClassifier` for testing or as a placeholder.

### Team target (Path 4)

Routes can dispatch to a **team** instead of a single agent — set `target_kind='team'` on the route and put the `agent_teams.id` in the `agent_id` column (reinterpreted as team id when kind=team). The resolver returns `Decision{TargetKind, TargetID}` via `ResolveDecision`; channel handlers populate `bus.InboundMessage.TargetKind` so the downstream consumer can branch on it.

**Wiring status:**
- ✅ Schema + store + resolver + REST CRUD + UI route form (target_kind picker)
- ✅ Channel handlers (Telegram + Feishu + BaseChannel) emit `bus.InboundMessage.TargetKind="team"` on team match
- ✅ Consumer-side dispatch: `cmd/gateway_consumer_normal.go` resolves team_id → `team.LeadAgentID`, injects `tools.WithToolTeamID(ctx, team_id)` so `team_tasks` tool calls run scoped to that team

**Execution model — lead-coordinated:** the team's lead agent receives the inbound and delegates to members via the `team_tasks(action="create", assignee=…)` tool. The existing `PendingTeamDispatch` + post-turn drain pipeline handles member dispatch. No parallel fan-out / separate orchestration primitive is introduced — the lead is the single entry point that decides distribution.

**Fail-open contract** (`cmd/gateway_consumer_team_route.go`):
- Team UUID parse error / team store nil → fall back to default channel agent
- Team load error (DB down) → fall back to default channel agent
- Team has no lead (lead was removed) → fall back to default channel agent

Sticky binding is **skipped for team targets** at the resolver layer — team orchestration manages its own per-task state, and skipping sticky lets operator team-membership edits land at next inbound.

### LLM intent classifier — wiring

Built-in implementations under `internal/channels/routing/`:
- `IntentClassifier` interface
- `LLMIntentClassifier` — provider-backed (any `providers.Provider`); closed-set label normalization (hallucinations collapse to "")
- `CachedIntentClassifier` — sync.Map cache, TTL 60s
- `StaticIntentClassifier` — test/placeholder

Operator wires at boot time in `cmd/gateway.go` (currently optional — when not wired, classifier is nil → cost-zero):

```go
if intentProvider := pickProvider(cfg); intentProvider != nil {
    classifier := routing.NewCachedIntentClassifier(
        routing.NewLLMIntentClassifier(
            intentProvider,
            cfg.IntentClassifier.Model,        // e.g. "claude-haiku-4-5"
            cfg.IntentClassifier.Labels,        // closed set
            cfg.IntentClassifier.PromptTemplate, // optional; {labels} placeholder
        ),
        0, // default 60s TTL
    )
    deps.routeResolver.SetIntentClassifier(classifier)
}
```

Cost guardrails:
- Resolver skips classifier call entirely if NO route on the channel has `intent` set.
- Classifier skipped on empty `messageText` (media-only inbound).
- Cache dedupes identical messages within 60s window.
- Classifier failure → log WARN + treat as `intent=""` → null-intent routes still match.

### Sticky routing (channel routing affinity)

Optional layer on top of rule eval — when the resolver is wired with a `ChannelRoutingAffinityStore` (auto-enabled when migration 082 is applied), each `(channel_instance_id, peer_id)` pair becomes sticky for 1 hour by default. The first inbound from a peer matches a route normally; subsequent inbound from the same peer short-circuits to the same agent with the SAME tool_allow snapshot until TTL expires.

```
Resolve(ch, peer, ...)
   │
   ├─ 1. Affinity lookup: (ch, peer) → bound agent
   │
   │    HIT → return (agent, snapshot_tool_allow, matched)   ← shortcut, no rule eval
   │
   ├─ 2. MISS → rule eval (priority match)
   │
   └─ 3. Match → upsert affinity row with TTL = now + 1h
```

**peerID encoding (chosen per channel):**
- DM (chatID == senderID): `peerID = chatID`
- Group/supergroup: `peerID = chatID + ":" + senderID` so each user in a group gets independent stickiness (admin reply doesn't drag the customer's binding).

**Snapshot semantics:** `tool_allow` is frozen at bind time. If operator edits the source route mid-conversation, the bound peer keeps the old `tool_allow` until expiry — avoids dropping/granting tools to an in-flight conversation. Next conversation (new peer or after expiry) sees the new rule.

**Tenant isolation:** affinity rows carry `tenant_id`; PG store filters every read. Cross-tenant sticky is impossible.

**Failure modes:**
- Affinity store Upsert fails → log WARN, rule decision still returned (fail-open).
- Empty peerID (caller hasn't been updated) → sticky bypassed silently, pure rule eval.
- Sticky DB miss (no row OR expired) → falls through to rule eval automatically.
- Operator wants to break a sticky binding manually → DELETE FROM `channel_routing_affinity` WHERE `peer_id = ?`.

**Cleanup:** `DeleteExpired(now)` prunes past-TTL rows. Currently no cron wired — lazy SQL filter (`expires_at > NOW()` on every read) handles it, but periodic cleanup is encouraged for large peer churn. Wire via Beat / cron job calling `ChannelRoutingAffinity.DeleteExpired`.

**Disable sticky:** call `SetAffinityStore(nil, 0)` after `NewAgentRouteResolver` — resolver becomes pure rule-based. Useful for tenants/channels where every message MUST be re-evaluated (e.g. routing depends on tool_allow that changes frequently).

### Multi-node deployments

When goclaw is built with `-tags redis` AND `GOCLAW_REDIS_DSN` is set, every `Invalidate` ALSO publishes an event to Redis channel `goclaw:route_invalidate` carrying the channel_instance UUID. Every node subscribes to that channel and evicts its own cache entry on receipt — so multi-node clusters converge on route changes within ~10ms of the mutation, not 30s (TTL).

```
Node A REST mutation
   ├─ store.Update commit
   ├─ resolver.Invalidate(ch-123)
   │   ├─ local cache.Delete(ch-123)
   │   └─ redis PUBLISH goclaw:route_invalidate ch-123
   │            │
   │            ▼
   ▼      Node B, Node C subscribers
HTTP 200       ├─ resolver.InvalidateLocal(ch-123)
               └─ cache.Delete(ch-123) — NO re-publish (avoids loop)
```

Failure modes:
- Redis down → `Publish` returns error → handler logs WARN, local cache is still evicted, peers catch up via TTL.
- Subscriber receives malformed payload → logs WARN, continues.
- Build without `-tags redis` OR `GOCLAW_REDIS_DSN` unset → `StartRedisInvalidate` is a silent no-op (single-node mode).

Files: `internal/channels/routing/redis_invalidate.go` (the `redis`-tagged implementation), `internal/channels/routing/redis_invalidate_noop.go` (single-node stub), wired in `cmd/gateway.go` right after `routing.NewAgentRouteResolver(...)`.

## Legacy `VoiceAgentID` auto-migration

If a channel instance config carries `voice_agent_id="<agent-key>"` (Telegram/Feishu/Zalo), a one-shot boot migration creates a route row:

```
peer_kind=direct, media_type=voice, agent_id=<resolved>, priority=50, is_enabled=true
name="Legacy VoiceAgentID migration"
```

Migration is **idempotent** — boots after the first one are no-ops. The legacy `VoiceAgentID` field on the config struct is marked deprecated.

## Kill switch

Set the env var **`GOCLAW_DISABLE_ROUTE_TABLE=true`** to bypass the route table entirely. The resolver short-circuits to "no match" and every inbound falls back to `channel_instances.agent_id`. Read once at process start — restart required to toggle.

Recommended use: disable in prod first deploy, smoke-test on beta with routes created, then unset to enable.

## Rollout sequence

1. **Dev** — migrate (PG migration 081) + boot with `GOCLAW_DISABLE_ROUTE_TABLE=true` → behavior identical to before.
2. **Dev** — flip the env var off, create 2 routes via the new admin UI tab "Agent Routes" → smoke-test DM + group+mention live.
3. **Beta (zuey)** — repeat.
4. **Prod** — per-tenant: just leave the route table empty (zero-risk equivalent of the kill switch). Operators enable multi-agent routing by creating routes from the channel-instance edit screen.

## Surface parity checklist

- **Gateway server:** migration 081 (Postgres + SQLite parity), `internal/store/pg/channel_agent_route_store.go`, resolver in `internal/channels/routing/`.
- **API contract:** REST endpoints under `/v1/channels/instances/{id}/agent-routes` (5 endpoints). Documented in `api-reference.md`.
- **Web UI:** "Agent Routes" tab on channel-instance detail page (`ui/web/src/pages/channels/channel-detail/channel-agent-routes-tab.tsx`).
- **Desktop UI (Lite):** N/A — Lite blocks channels per `edition.go`.

## Failure modes & operator playbook

| Symptom | Likely cause | Fix |
|---|---|---|
| Inbound goes to wrong agent | route higher up the priority list catching it | reorder via PATCH `priority` |
| Bot doesn't reply to group at all | `mention_required=true` with bot not mentioned + no fallback route | add a non-mention group route OR `@`-mention bot |
| Routing changes don't take effect | network partition between API and worker | restart worker — local cache will refetch |
| Need emergency disable | any reason routing misbehaves | set `GOCLAW_DISABLE_ROUTE_TABLE=true` + restart |

## Related

- Migration: `migrations/000081_*.sql`
- Store: `internal/store/channel_agent_route_store.go`
- Resolver: `internal/channels/routing/agent_route_resolver.go`
- HTTP: `internal/http/channel_agent_routes.go`
- UI: `ui/web/src/pages/channels/channel-detail/channel-agent-routes-tab.tsx`
