// Package routing implements per-channel agent routing.
//
// AgentRouteResolver picks which backend agent should handle an inbound
// message based on channel_agent_routes rows. It is channel-agnostic — each
// channel (Telegram/Feishu/Zalo) reduces its inbound event to four signals:
//
//	(channelInstanceID, peerKind, mediaKind, mentionMatched)
//
// and the resolver returns the matched agent's UUID plus its optional
// tool_allow narrowing. A miss yields (uuid.Nil, nil, false, nil) — caller
// falls back to channel_instances.agent_id with ToolAllow=nil.
//
// Match order is the store's natural ordering (priority ASC, created_at ASC).
// Routes are cached per channel_instance_id with a short TTL; REST mutation
// must call Invalidate to evict the entry.
package routing

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// InvalidatePublisher pushes route-invalidation events to peer goclaw nodes
// so multi-node deployments converge on route changes within milliseconds
// rather than waiting for the per-node TTL. Optional — nil = single-node mode
// (purely in-process Invalidate, no cross-node sync).
//
// Concrete implementation lives in redis_invalidate.go (build tag `redis`);
// when goclaw is built without that tag the wire-up is a no-op.
type InvalidatePublisher interface {
	// Publish broadcasts a channel-instance invalidation event to peers.
	// Implementations should be best-effort — never block, never panic on
	// transient broker failures. Use the returned error only for logging.
	Publish(ctx context.Context, channelInstanceID uuid.UUID) error
}

// DisableRouteTableEnv is the operator kill switch. When set to "true" the
// resolver returns "no match" immediately, so all inbound traffic falls back
// to channel_instances.agent_id without touching the route table. Read once
// at NewAgentRouteResolver() — flipping the env requires a restart, which is
// exactly the contract we want for a kill switch (deterministic across nodes).
const DisableRouteTableEnv = "GOCLAW_DISABLE_ROUTE_TABLE"

// Media kind constants used as the third resolver argument and matched against
// channel_agent_routes.media_type (NULL on a route means "any").
const (
	MediaKindText  = "text"
	MediaKindVoice = "voice"
	MediaKindMedia = "media"
)

// DefaultCacheTTL is the time a cached per-channel route list stays fresh.
// REST mutations call Invalidate explicitly — the TTL is a safety net for
// missed invalidations (e.g. across cluster nodes once we shard).
const DefaultCacheTTL = 30 * time.Second

// DefaultAffinityTTL is how long a (channel, peer) → agent binding sticks.
// Long enough to span a typical chat session, short enough that operator
// route edits land at next conversation.
const DefaultAffinityTTL = 1 * time.Hour

// cachedRoutes is an immutable snapshot of the route list for one channel.
type cachedRoutes struct {
	routes    []store.ChannelAgentRouteData
	expiresAt time.Time
}

// AgentRouteResolver maps inbound messages to (agent, tool_allow) using
// channel_agent_routes. Safe for concurrent use.
type AgentRouteResolver struct {
	store         store.ChannelAgentRouteStore
	cache         sync.Map // channelInstanceID(uuid.UUID) -> cachedRoutes
	ttl           time.Duration
	clock         func() time.Time // injectable for tests
	disabled      bool             // GOCLAW_DISABLE_ROUTE_TABLE=true kill switch
	publisher     InvalidatePublisher
	affinityStore store.ChannelRoutingAffinityStore
	affinityTTL   time.Duration
	classifier    IntentClassifier
}

// SetIntentClassifier wires an opt-in LLM intent classifier. Pass nil to
// disable (default). When set, Resolve invokes the classifier ONLY if at
// least one route on the channel has a non-null Intent — avoids LLM cost
// for channels that don't use intent-based routing.
func (r *AgentRouteResolver) SetIntentClassifier(c IntentClassifier) {
	r.classifier = c
}

// SetInvalidatePublisher wires a multi-node invalidation broadcaster. Pass nil
// to operate in single-node mode (the default). Subsequent calls to Invalidate
// will fan out the event to peers AFTER evicting the local cache entry.
func (r *AgentRouteResolver) SetInvalidatePublisher(p InvalidatePublisher) {
	r.publisher = p
}

// SetAffinityStore turns on sticky (channel, peer) → agent routing. Pass nil
// to disable (default). ttl <= 0 falls back to DefaultAffinityTTL. When set,
// Resolve checks affinity FIRST before rule eval; on rule match the resolver
// upserts a new binding so the same peer continues to get the same agent
// (with the same tool_allow snapshot) until expiry.
func (r *AgentRouteResolver) SetAffinityStore(s store.ChannelRoutingAffinityStore, ttl time.Duration) {
	r.affinityStore = s
	if ttl <= 0 {
		ttl = DefaultAffinityTTL
	}
	r.affinityTTL = ttl
}

// NewAgentRouteResolver builds a resolver. ttl <= 0 defaults to DefaultCacheTTL.
// The kill switch GOCLAW_DISABLE_ROUTE_TABLE is read at construction time so
// the flag is stable for the process lifetime.
func NewAgentRouteResolver(s store.ChannelAgentRouteStore, ttl time.Duration) *AgentRouteResolver {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &AgentRouteResolver{
		store:    s,
		ttl:      ttl,
		clock:    time.Now,
		disabled: os.Getenv(DisableRouteTableEnv) == "true",
	}
}

// Decision is what Resolve returns when a route matches. TargetKind tells the
// channel handler whether TargetID refers to a single agent (legacy) or an
// agent team (Path 4 — dispatch via internal/orchestration).
type Decision struct {
	TargetID   uuid.UUID
	TargetKind string // store.RouteTargetAgent | store.RouteTargetTeam
	ToolAllow  []string
}

// Resolve returns the first matching route's (agentID, toolAllow) for the
// given inbound signal. matched=false means no rule applied — caller uses the
// channel_instance's default agent_id.
//
// peerID identifies the sender (chat_id for DM, group_id+user_id composite for
// group messages — channel decides). When non-empty AND an affinity store is
// wired via SetAffinityStore, the resolver shortcuts: returns the same agent
// the peer was previously bound to (within TTL), bypassing rule eval. On a
// rule match (sticky miss), the resolver upserts a new binding with the
// tool_allow snapshot so mid-conversation operator edits don't leak in.
// Pass peerID="" to bypass sticky entirely.
//
// mediaKind is one of MediaKind* constants; passing arbitrary strings matches
// nothing for typed routes (route.MediaType non-nil) and matches any route
// where MediaType is nil.
//
// On store error the resolver returns (uuid.Nil, nil, false, err). Callers
// MUST treat error as "fall back to default agent" — never block traffic on a
// resolver failure.
//
// LEGACY shim: Resolve returns the agent UUID + tool_allow (target_kind=agent
// is assumed). Use ResolveDecision for the full Decision struct including
// TargetKind to support team targets.
func (r *AgentRouteResolver) Resolve(
	ctx context.Context,
	channelInstanceID uuid.UUID,
	peerID string,
	messageText string,
	peerKind string,
	mediaKind string,
	mentionMatched bool,
) (uuid.UUID, []string, bool, error) {
	d, matched, err := r.ResolveDecision(ctx, channelInstanceID, peerID, messageText, peerKind, mediaKind, mentionMatched)
	if err != nil || !matched {
		return uuid.Nil, nil, matched, err
	}
	return d.TargetID, d.ToolAllow, true, nil
}

// ResolveDecision is the Path 4–aware variant that returns the full Decision
// struct (target_kind + target_id + tool_allow). Channel handlers branch on
// TargetKind to dispatch to a single agent or to an agent team.
func (r *AgentRouteResolver) ResolveDecision(
	ctx context.Context,
	channelInstanceID uuid.UUID,
	peerID string,
	messageText string,
	peerKind string,
	mediaKind string,
	mentionMatched bool,
) (Decision, bool, error) {
	if r.disabled {
		return Decision{}, false, nil
	}

	// Sticky shortcut: same (channel, peer) → same agent. Snapshot of tool_allow
	// at bind time avoids mid-conversation drift when operator edits the route.
	// Sticky bindings always re-emit target_kind=agent for now — see roadmap.md
	// for the Path 4 follow-up to store target_kind alongside agent_id.
	if r.affinityStore != nil && peerID != "" {
		if bound, err := r.affinityStore.Get(ctx, channelInstanceID, peerID); err == nil && bound != nil {
			var allow []string
			if bound.ToolAllow != nil {
				allow = append(allow, (*bound.ToolAllow)...)
			}
			return Decision{TargetID: bound.AgentID, TargetKind: store.RouteTargetAgent, ToolAllow: allow}, true, nil
		}
		// On Get error we silently fall through to rule eval — sticky is a perf
		// hint, never a correctness gate.
	}

	routes, err := r.loadRoutes(ctx, channelInstanceID)
	if err != nil {
		return Decision{}, false, err
	}

	// Intent classification (Path 1) — only run if SOME route on this channel
	// has a non-null Intent. Avoids LLM cost when no operator opted in.
	intent := ""
	if r.classifier != nil && messageText != "" && anyRouteHasIntent(routes) {
		if got, err := r.classifier.Classify(ctx, channelInstanceID.String(), messageText); err != nil {
			slog.Warn("intent classifier failed; treating intent as unknown",
				"channel_instance_id", channelInstanceID, "err", err)
		} else {
			intent = got
		}
	}

	for i := range routes {
		route := &routes[i]
		if !route.IsEnabled {
			continue
		}
		if route.PeerKind != peerKind {
			continue
		}
		if route.MediaType != nil && *route.MediaType != mediaKind {
			continue
		}
		if route.MentionRequired && !mentionMatched {
			continue
		}
		// Intent filter — null route intent matches ANY classification; non-null
		// only matches when classifier returned the same label.
		if route.Intent != nil && *route.Intent != intent {
			continue
		}
		var allow []string
		if route.ToolAllow != nil {
			allow = append(allow, (*route.ToolAllow)...)
		}

		kind := route.TargetKind
		if kind == "" {
			kind = store.RouteTargetAgent
		}

		// Persist sticky binding only for target_kind=agent — team bindings are
		// stateless (team orchestration handles its own per-task state). Best-
		// effort — log+continue on Upsert failure.
		if kind == store.RouteTargetAgent && r.affinityStore != nil && peerID != "" {
			var allowSnapshot *[]string
			if route.ToolAllow != nil {
				cp := append([]string{}, (*route.ToolAllow)...)
				allowSnapshot = &cp
			}
			binding := &store.ChannelRoutingAffinityData{
				ChannelInstanceID: channelInstanceID,
				PeerID:            peerID,
				AgentID:           route.AgentID,
				ToolAllow:         allowSnapshot,
				ExpiresAt:         r.clock().Add(r.affinityTTL),
			}
			if err := r.affinityStore.Upsert(ctx, binding); err != nil {
				slog.Warn("sticky affinity upsert failed",
					"channel_instance_id", channelInstanceID, "peer_id", peerID, "err", err)
			}
		}

		return Decision{TargetID: route.AgentID, TargetKind: kind, ToolAllow: allow}, true, nil
	}
	return Decision{}, false, nil
}

// Invalidate drops the cached route list for a channel AND broadcasts the
// event to peer nodes (when a publisher is wired). Called from REST handlers
// after Create/Update/Delete of channel_agent_routes rows.
//
// Publishing is best-effort — a broker failure logs WARN and falls back to
// the per-node TTL safety net (max 30s drift between nodes). The local cache
// is always evicted regardless of publisher state.
func (r *AgentRouteResolver) Invalidate(channelInstanceID uuid.UUID) {
	r.invalidateLocal(channelInstanceID)
	if r.publisher != nil {
		// 5s ceiling so a stuck broker can't hang the HTTP handler that
		// triggered the mutation; resolver TTL is the safety net.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.publisher.Publish(ctx, channelInstanceID); err != nil {
			slog.Warn("route_invalidate publish failed; peers will catch up via TTL",
				"channel_instance_id", channelInstanceID, "err", err)
		}
	}
}

// InvalidateLocal evicts the cached entry on THIS process only. Exported for
// the subscriber side of cross-node invalidation: when a peer publishes an
// event, the subscriber calls InvalidateLocal so we don't re-broadcast and
// create an event storm.
func (r *AgentRouteResolver) InvalidateLocal(channelInstanceID uuid.UUID) {
	r.invalidateLocal(channelInstanceID)
}

func (r *AgentRouteResolver) invalidateLocal(channelInstanceID uuid.UUID) {
	r.cache.Delete(channelInstanceID)
}

// anyRouteHasIntent reports whether at least one route has a non-null Intent
// column set. Used to gate the LLM classifier call — when no route opts in,
// classification is a waste of LLM tokens.
func anyRouteHasIntent(routes []store.ChannelAgentRouteData) bool {
	for i := range routes {
		if routes[i].Intent != nil && *routes[i].Intent != "" {
			return true
		}
	}
	return false
}

// loadRoutes returns the cached route list, refreshing from the store on miss
// or TTL expiry. The returned slice is the cache's own backing array — callers
// must NOT mutate it (Resolve only reads).
func (r *AgentRouteResolver) loadRoutes(ctx context.Context, channelInstanceID uuid.UUID) ([]store.ChannelAgentRouteData, error) {
	now := r.clock()
	if v, ok := r.cache.Load(channelInstanceID); ok {
		entry := v.(cachedRoutes)
		if now.Before(entry.expiresAt) {
			return entry.routes, nil
		}
	}
	routes, err := r.store.ListByChannelInstance(ctx, channelInstanceID)
	if err != nil {
		return nil, err
	}
	r.cache.Store(channelInstanceID, cachedRoutes{routes: routes, expiresAt: now.Add(r.ttl)})
	return routes, nil
}
