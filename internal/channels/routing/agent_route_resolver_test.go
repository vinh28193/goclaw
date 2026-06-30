package routing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// allowGate mirrors mcp.IsToolAllowed locally so we can verify the resolver→gate
// hand-off without creating an import cycle (channels/routing imports store,
// channels/channel.go imports channels/routing; importing internal/mcp here
// would loop back through internal/tools → internal/channels → routing).
//
// Keep this in sync with mcp/tool_filter.go: allow nil = open, deny first wins.
func allowGate(tool string, allow, deny []string) bool {
	for _, d := range deny {
		if d == tool {
			return false
		}
	}
	if len(allow) == 0 {
		return true
	}
	for _, a := range allow {
		if a == tool {
			return true
		}
	}
	return false
}

// fakeRouteStore is a controllable ChannelAgentRouteStore for resolver tests.
// Only ListByChannelInstance + a call counter are needed — Create/Get/etc.
// are stubbed.
type fakeRouteStore struct {
	routes  map[uuid.UUID][]store.ChannelAgentRouteData
	calls   int
	listErr error
}

func (f *fakeRouteStore) Create(context.Context, *store.ChannelAgentRouteData) error {
	return errors.New("unused")
}
func (f *fakeRouteStore) Get(context.Context, uuid.UUID) (*store.ChannelAgentRouteData, error) {
	return nil, errors.New("unused")
}
func (f *fakeRouteStore) Update(context.Context, uuid.UUID, map[string]any) error {
	return errors.New("unused")
}
func (f *fakeRouteStore) Delete(context.Context, uuid.UUID) error { return errors.New("unused") }
func (f *fakeRouteStore) ListByTenant(context.Context) ([]store.ChannelAgentRouteData, error) {
	return nil, errors.New("unused")
}
func (f *fakeRouteStore) ListByChannelInstance(_ context.Context, ch uuid.UUID) ([]store.ChannelAgentRouteData, error) {
	f.calls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]store.ChannelAgentRouteData, len(f.routes[ch]))
	copy(out, f.routes[ch])
	return out, nil
}

func ptrStr(s string) *string { return &s }

func ptrAllow(a ...string) *[]string {
	v := append([]string(nil), a...)
	return &v
}

func newRoute(agent uuid.UUID, peer string, media *string, mention bool, prio int, enabled bool, allow *[]string) store.ChannelAgentRouteData {
	return store.ChannelAgentRouteData{
		BaseModel:       store.BaseModel{ID: uuid.Must(uuid.NewV7())},
		AgentID:         agent,
		PeerKind:        peer,
		MediaType:       media,
		MentionRequired: mention,
		Priority:        prio,
		IsEnabled:       enabled,
		ToolAllow:       allow,
	}
}

func TestResolver_NoRoutesReturnsUnmatched(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	r := NewAgentRouteResolver(&fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{}}, 0)

	agentID, allow, matched, err := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if matched || agentID != uuid.Nil || allow != nil {
		t.Fatalf("expected unmatched zero-value, got agent=%v allow=%v matched=%v", agentID, allow, matched)
	}
}

func TestResolver_PeerKindFilter(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	dmAgent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(dmAgent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, 0)

	if a, _, m, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false); !m || a != dmAgent {
		t.Fatalf("direct should match: agent=%v matched=%v", a, m)
	}
	if _, _, m, _ := r.Resolve(context.Background(), chID, "", "", "group", MediaKindText, true); m {
		t.Fatal("group should not match a direct-only route")
	}
}

func TestResolver_MentionGate(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	admin := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(admin, "group", nil, true, 100, true, nil)}, // requires mention
	}}
	r := NewAgentRouteResolver(fs, 0)

	if _, _, m, _ := r.Resolve(context.Background(), chID, "", "", "group", MediaKindText, false); m {
		t.Fatal("group w/o mention should not match mention_required route")
	}
	if a, _, m, _ := r.Resolve(context.Background(), chID, "", "", "group", MediaKindText, true); !m || a != admin {
		t.Fatalf("group w/ mention should match: agent=%v matched=%v", a, m)
	}
}

func TestResolver_PriorityOrderingHighWins(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	high, low := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	// Store contract: returned in priority ASC. Resolver trusts that ordering.
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {
			newRoute(high, "group", nil, true, 50, true, nil),  // mention required
			newRoute(low, "group", nil, false, 100, true, nil), // catch-all
		},
	}}
	r := NewAgentRouteResolver(fs, 0)

	if a, _, m, _ := r.Resolve(context.Background(), chID, "", "", "group", MediaKindText, true); !m || a != high {
		t.Fatalf("mention message expected priority 50: agent=%v matched=%v", a, m)
	}
	if a, _, m, _ := r.Resolve(context.Background(), chID, "", "", "group", MediaKindText, false); !m || a != low {
		t.Fatalf("non-mention expected priority 100 catch-all: agent=%v matched=%v", a, m)
	}
}

func TestResolver_TieBreakByCreatedAtOrderingFromStore(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	// Same priority — store returns in created_at ASC, resolver MUST pick first.
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {
			newRoute(first, "direct", nil, false, 100, true, nil),
			newRoute(second, "direct", nil, false, 100, true, nil),
		},
	}}
	r := NewAgentRouteResolver(fs, 0)
	if a, _, _, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false); a != first {
		t.Fatalf("tie-break should pick first row, got %v", a)
	}
}

func TestResolver_MediaTypeFilter(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	voiceAgent, textAgent := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {
			newRoute(voiceAgent, "direct", ptrStr("voice"), false, 50, true, nil),
			newRoute(textAgent, "direct", nil, false, 100, true, nil),
		},
	}}
	r := NewAgentRouteResolver(fs, 0)
	if a, _, m, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindVoice, false); !m || a != voiceAgent {
		t.Fatalf("voice should route to voice route: agent=%v matched=%v", a, m)
	}
	if a, _, m, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false); !m || a != textAgent {
		t.Fatalf("text should fall through to catch-all: agent=%v matched=%v", a, m)
	}
}

func TestResolver_ToolAllowRoundtripAndDefensiveCopy(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	allow := ptrAllow("generate_shortlink", "get_commission_for_url")
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, allow)},
	}}
	r := NewAgentRouteResolver(fs, 0)

	_, got, _, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if len(got) != 2 || got[0] != "generate_shortlink" || got[1] != "get_commission_for_url" {
		t.Fatalf("tool_allow lost: %v", got)
	}
	// Mutating the returned slice must not corrupt the cached source.
	got[0] = "MUTATED"
	_, got2, _, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if got2[0] != "generate_shortlink" {
		t.Fatalf("defensive copy broken — cache corrupted: %v", got2)
	}
}

func TestResolver_NilToolAllowReturnsNil(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, 0)
	_, allow, m, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if !m {
		t.Fatal("expected match")
	}
	if allow != nil {
		t.Fatalf("nil tool_allow on route must surface as nil, got %v", allow)
	}
}

func TestResolver_DisabledRouteSkipped(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	disabled, enabled := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {
			newRoute(disabled, "direct", nil, false, 50, false, nil), // disabled
			newRoute(enabled, "direct", nil, false, 100, true, nil),
		},
	}}
	r := NewAgentRouteResolver(fs, 0)
	if a, _, _, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false); a != enabled {
		t.Fatalf("disabled route must be skipped; got %v", a)
	}
}

func TestResolver_CacheHitAvoidsRepeatStoreCall(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, 30*time.Second)
	for i := 0; i < 5; i++ {
		r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	}
	if fs.calls != 1 {
		t.Fatalf("expected 1 store call (cache hits 4x); got %d", fs.calls)
	}
}

func TestResolver_CacheTTLExpiry(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, time.Minute)
	now := time.Now()
	r.clock = func() time.Time { return now }
	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if fs.calls != 1 {
		t.Fatalf("expected 1 call after first Resolve; got %d", fs.calls)
	}
	r.clock = func() time.Time { return now.Add(time.Minute + time.Second) }
	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if fs.calls != 2 {
		t.Fatalf("TTL expired Resolve should re-hit store; got %d total calls", fs.calls)
	}
}

func TestResolver_KillSwitchSkipsLookup(t *testing.T) {
	t.Setenv(DisableRouteTableEnv, "true")
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, 0)

	a, allow, matched, err := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if err != nil {
		t.Fatalf("kill switch path should not return error, got %v", err)
	}
	if matched || a != uuid.Nil || allow != nil {
		t.Fatalf("kill switch must return unmatched; agent=%v matched=%v allow=%v", a, matched, allow)
	}
	if fs.calls != 0 {
		t.Fatalf("kill switch must NOT call the store; got %d calls", fs.calls)
	}
}

func TestResolver_KillSwitchOnlyHonoredAtConstruction(t *testing.T) {
	// Flag set AFTER NewAgentRouteResolver returns must NOT affect behavior —
	// the env read happens once at construction so the flag is stable for the
	// process lifetime (deterministic kill switch across cluster nodes).
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, 0)

	t.Setenv(DisableRouteTableEnv, "true") // set AFTER construction
	if a, _, m, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false); !m || a != agent {
		t.Fatalf("post-construction env flip must NOT disable: agent=%v matched=%v", a, m)
	}
}

// TestResolver_ToolAllowFlowsToMCPFilter is the locked-decision scenario (b):
// resolver outputs ToolAllow=["A","B"]; downstream the loop's tool authorizer
// calls allowGate with that slice → "A" and "B" pass, "C" is
// blocked. Wires resolver output to the gate at agent/loop_pipeline_callbacks.go
// without booting the full agent loop.
func TestResolver_ToolAllowFlowsToMCPFilter(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	allow := ptrAllow("A", "B")
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, allow)},
	}}
	r := NewAgentRouteResolver(fs, 0)

	_, gotAllow, matched, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if !matched || len(gotAllow) != 2 {
		t.Fatalf("expected match w/ 2 tools; matched=%v allow=%v", matched, gotAllow)
	}

	cases := []struct {
		name string
		tool string
		want bool
	}{
		{"A allowed", "A", true},
		{"B allowed", "B", true},
		{"C rejected", "C", false},
		{"empty rejected", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := allowGate(c.tool, gotAllow, nil); got != c.want {
				t.Fatalf("IsToolAllowed(%q, %v) = %v, want %v", c.tool, gotAllow, got, c.want)
			}
		})
	}
}

// Scenario (a): tool_allow=NULL → resolver returns nil allow → IsToolAllowed
// with nil allow lets every tool through (defense-in-depth still defers to the
// backend MCP-key whitelist).
func TestResolver_NilToolAllowKeepsMCPOpen(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, 0)
	_, allow, _, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if allow != nil {
		t.Fatalf("nil route tool_allow must surface as nil; got %v", allow)
	}
	if !allowGate("anything", nil, nil) {
		t.Fatal("IsToolAllowed with nil allow must pass anything through")
	}
}

// recordingClassifier captures Classify calls so tests assert the gate
// (anyRouteHasIntent + non-empty messageText) is respected.
type recordingClassifier struct {
	returnIntent string
	returnErr    error
	calls        int
	lastMessage  string
}

func (c *recordingClassifier) Classify(_ context.Context, _ string, msg string) (string, error) {
	c.calls++
	c.lastMessage = msg
	return c.returnIntent, c.returnErr
}

// Intent column = nil on all routes → classifier MUST NOT be called even when
// messageText is non-empty. Saves LLM tokens when no operator opted in.
func TestResolver_IntentClassifierSkippedWhenNoRouteHasIntent(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, 0)
	cls := &recordingClassifier{returnIntent: "billing"}
	r.SetIntentClassifier(cls)

	a, _, m, _ := r.Resolve(context.Background(), chID, "", "hello world", "direct", MediaKindText, false)
	if !m || a != agent {
		t.Fatalf("rule match should succeed; got agent=%v matched=%v", a, m)
	}
	if cls.calls != 0 {
		t.Fatalf("classifier MUST NOT be called when no route has Intent; got %d", cls.calls)
	}
}

// Empty messageText → skip classifier (e.g. media-only inbound has no text).
func TestResolver_IntentClassifierSkippedWhenMessageEmpty(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	billing := "billing"
	route := newRoute(agent, "direct", nil, false, 100, true, nil)
	route.Intent = &billing
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {route},
	}}
	r := NewAgentRouteResolver(fs, 0)
	cls := &recordingClassifier{returnIntent: "billing"}
	r.SetIntentClassifier(cls)

	// Empty message → classifier not invoked → no non-null-intent route matches
	// (intent="" != "billing") → unmatched fallback.
	_, _, m, _ := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if m {
		t.Fatal("empty message must NOT trigger classifier; route with non-null intent MUST NOT match unknown intent")
	}
	if cls.calls != 0 {
		t.Fatalf("classifier should be skipped on empty message; got %d calls", cls.calls)
	}
}

// Intent-tagged route matches when classifier returns the matching label.
func TestResolver_IntentLabelMatchRoutes(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	billingAgent := uuid.Must(uuid.NewV7())
	supportAgent := uuid.Must(uuid.NewV7())
	billing := "billing"
	support := "support"
	rt1 := newRoute(billingAgent, "direct", nil, false, 50, true, nil)
	rt1.Intent = &billing
	rt2 := newRoute(supportAgent, "direct", nil, false, 60, true, nil)
	rt2.Intent = &support
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {rt1, rt2},
	}}
	r := NewAgentRouteResolver(fs, 0)

	// Classifier returns "billing" → billingAgent matches.
	r.SetIntentClassifier(&recordingClassifier{returnIntent: "billing"})
	if a, _, m, _ := r.Resolve(context.Background(), chID, "", "I want refund", "direct", MediaKindText, false); !m || a != billingAgent {
		t.Fatalf("billing intent: agent=%v matched=%v", a, m)
	}

	// Classifier returns "support" → supportAgent matches.
	r.SetIntentClassifier(&recordingClassifier{returnIntent: "support"})
	if a, _, m, _ := r.Resolve(context.Background(), chID, "", "my app crashed", "direct", MediaKindText, false); !m || a != supportAgent {
		t.Fatalf("support intent: agent=%v matched=%v", a, m)
	}
}

// Classifier returns unknown label → only null-intent routes match.
func TestResolver_IntentUnknownFallsToNullIntentRoute(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	billingAgent := uuid.Must(uuid.NewV7())
	fallbackAgent := uuid.Must(uuid.NewV7())
	billing := "billing"
	rt1 := newRoute(billingAgent, "direct", nil, false, 50, true, nil)
	rt1.Intent = &billing
	rt2 := newRoute(fallbackAgent, "direct", nil, false, 100, true, nil) // intent nil = matches any
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {rt1, rt2},
	}}
	r := NewAgentRouteResolver(fs, 0)
	r.SetIntentClassifier(&recordingClassifier{returnIntent: "unknown_topic"})

	a, _, m, _ := r.Resolve(context.Background(), chID, "", "ambiguous", "direct", MediaKindText, false)
	if !m || a != fallbackAgent {
		t.Fatalf("unknown intent must fall through to null-intent route; got agent=%v matched=%v", a, m)
	}
}

// Classifier error → fail-open: treated as intent="" → null-intent routes still match.
func TestResolver_IntentClassifierErrorFailsOpen(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	billingAgent := uuid.Must(uuid.NewV7())
	fallbackAgent := uuid.Must(uuid.NewV7())
	billing := "billing"
	rt1 := newRoute(billingAgent, "direct", nil, false, 50, true, nil)
	rt1.Intent = &billing
	rt2 := newRoute(fallbackAgent, "direct", nil, false, 100, true, nil)
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {rt1, rt2},
	}}
	r := NewAgentRouteResolver(fs, 0)
	r.SetIntentClassifier(&recordingClassifier{returnErr: errors.New("LLM down")})

	a, _, m, _ := r.Resolve(context.Background(), chID, "", "any text", "direct", MediaKindText, false)
	if !m || a != fallbackAgent {
		t.Fatalf("classifier error must fail-open to null-intent route; got agent=%v matched=%v", a, m)
	}
}

// CachedIntentClassifier dedupes identical messages within TTL window.
func TestCachedIntentClassifier_DedupesIdenticalMessages(t *testing.T) {
	inner := &recordingClassifier{returnIntent: "billing"}
	c := NewCachedIntentClassifier(inner, time.Hour)

	for i := 0; i < 5; i++ {
		intent, _ := c.Classify(context.Background(), "ch-X", "I want refund")
		if intent != "billing" {
			t.Fatalf("got %q", intent)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("cache should dedupe; got %d underlying calls (expected 1)", inner.calls)
	}
}

// CachedIntentClassifier does NOT cache errors — next call retries inner.
func TestCachedIntentClassifier_DoesNotCacheErrors(t *testing.T) {
	inner := &recordingClassifier{returnErr: errors.New("transient")}
	c := NewCachedIntentClassifier(inner, time.Hour)

	_, _ = c.Classify(context.Background(), "ch-X", "hello")
	inner.returnErr = nil
	inner.returnIntent = "support"
	intent, err := c.Classify(context.Background(), "ch-X", "hello")
	if err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	if intent != "support" {
		t.Fatalf("retry intent: %q", intent)
	}
	if inner.calls != 2 {
		t.Fatalf("error should not be cached → retry hits inner; got %d", inner.calls)
	}
}

// TestResolver_LegacyRouteDefaultsTargetKindAgent: routes whose TargetKind
// field is empty (e.g. constructed by old code paths) must resolve as
// target_kind=agent — preserves Path 4 backwards compatibility.
func TestResolver_LegacyRouteDefaultsTargetKindAgent(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	rt := newRoute(agent, "direct", nil, false, 100, true, nil)
	// rt.TargetKind intentionally left "" — simulates rows from before migration 084.
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {rt},
	}}
	r := NewAgentRouteResolver(fs, 0)

	d, m, err := r.ResolveDecision(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if err != nil || !m {
		t.Fatalf("legacy route should still match: err=%v matched=%v", err, m)
	}
	if d.TargetKind != store.RouteTargetAgent {
		t.Fatalf("empty TargetKind must default to 'agent'; got %q", d.TargetKind)
	}
	if d.TargetID != agent {
		t.Fatalf("target_id should be the agent's UUID: %v", d.TargetID)
	}
}

// Routes with TargetKind="team" surface the team ID through ResolveDecision;
// channel handlers branch on this to dispatch via orchestration instead of
// publishing a single-agent inbound.
func TestResolver_TeamTargetSurfaced(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	rt := newRoute(teamID, "direct", nil, false, 100, true, nil)
	rt.TargetKind = store.RouteTargetTeam
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {rt},
	}}
	r := NewAgentRouteResolver(fs, 0)

	d, m, err := r.ResolveDecision(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if err != nil || !m {
		t.Fatalf("team route should match: err=%v matched=%v", err, m)
	}
	if d.TargetKind != store.RouteTargetTeam {
		t.Fatalf("expected target_kind=team; got %q", d.TargetKind)
	}
	if d.TargetID != teamID {
		t.Fatalf("expected target_id = teamID; got %v", d.TargetID)
	}
}

// Sticky binding must NOT persist for team-target routes — team orchestration
// has its own per-task state and we want each new conversation to re-evaluate
// the route (so operator changes to team membership land at next inbound).
func TestResolver_TeamTargetDoesNotCreateAffinity(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	rt := newRoute(teamID, "direct", nil, false, 100, true, nil)
	rt.TargetKind = store.RouteTargetTeam
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {rt},
	}}
	r := NewAgentRouteResolver(fs, time.Hour)
	aff := newFakeAffinityStore()
	r.SetAffinityStore(aff, time.Hour)

	_, m, _ := r.ResolveDecision(context.Background(), chID, "peer-A", "", "direct", MediaKindText, false)
	if !m {
		t.Fatal("team route should match")
	}
	if aff.upsertCalls != 0 {
		t.Fatalf("team target MUST NOT create sticky binding; got %d Upsert calls", aff.upsertCalls)
	}
}

// fakeAffinityStore is an in-memory ChannelRoutingAffinityStore used to
// test sticky resolver behavior without standing up PG.
type fakeAffinityStore struct {
	rows      map[string]*store.ChannelRoutingAffinityData // key: channelID+":"+peerID
	getCalls  int
	upsertCalls int
	upsertErr error
}

func newFakeAffinityStore() *fakeAffinityStore {
	return &fakeAffinityStore{rows: map[string]*store.ChannelRoutingAffinityData{}}
}

func (f *fakeAffinityStore) keyOf(ch uuid.UUID, peer string) string {
	return ch.String() + ":" + peer
}

func (f *fakeAffinityStore) Get(_ context.Context, ch uuid.UUID, peer string) (*store.ChannelRoutingAffinityData, error) {
	f.getCalls++
	r, ok := f.rows[f.keyOf(ch, peer)]
	if !ok {
		return nil, errors.New("not found")
	}
	if !r.ExpiresAt.IsZero() && r.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("expired")
	}
	return r, nil
}

func (f *fakeAffinityStore) Upsert(_ context.Context, r *store.ChannelRoutingAffinityData) error {
	f.upsertCalls++
	if f.upsertErr != nil {
		return f.upsertErr
	}
	cp := *r
	f.rows[f.keyOf(r.ChannelInstanceID, r.PeerID)] = &cp
	return nil
}

func (f *fakeAffinityStore) Delete(_ context.Context, ch uuid.UUID, peer string) error {
	delete(f.rows, f.keyOf(ch, peer))
	return nil
}

func (f *fakeAffinityStore) DeletePeerForChannel(_ context.Context, ch uuid.UUID) (int, error) {
	n := 0
	prefix := ch.String() + ":"
	for k := range f.rows {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(f.rows, k)
			n++
		}
	}
	return n, nil
}

func (f *fakeAffinityStore) DeleteExpired(_ context.Context, now time.Time) (int, error) {
	n := 0
	for k, r := range f.rows {
		if !r.ExpiresAt.IsZero() && r.ExpiresAt.Before(now) {
			delete(f.rows, k)
			n++
		}
	}
	return n, nil
}

// Sticky hit: 2nd Resolve with same peer + non-empty peerID returns the same
// agent WITHOUT touching the rule store (cache miss avoided too).
func TestResolver_StickyHitShortCircuitsRuleEval(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, time.Hour)
	aff := newFakeAffinityStore()
	r.SetAffinityStore(aff, time.Hour)

	// 1st call: rule eval matches + upsert affinity.
	a1, _, m1, _ := r.Resolve(context.Background(), chID, "peer-A", "", "direct", MediaKindText, false)
	if !m1 || a1 != agent {
		t.Fatalf("1st call rule eval: agent=%v matched=%v", a1, m1)
	}
	if aff.upsertCalls != 1 {
		t.Fatalf("Upsert should fire once on rule match; got %d", aff.upsertCalls)
	}

	// 2nd call: sticky hit returns same agent. Even if we mutate the rule store,
	// the bound peer keeps the snapshot.
	fs.routes[chID][0].AgentID = uuid.Must(uuid.NewV7()) // operator changed route
	r.invalidateLocal(chID)                              // flush rule cache
	a2, _, m2, _ := r.Resolve(context.Background(), chID, "peer-A", "", "direct", MediaKindText, false)
	if !m2 || a2 != agent {
		t.Fatalf("2nd call should hit sticky snapshot, not new rule; got agent=%v", a2)
	}
	if aff.upsertCalls != 1 {
		t.Fatalf("Sticky hit should NOT trigger Upsert; got %d", aff.upsertCalls)
	}
}

// Different peer → different (or same) decision, both stored independently.
func TestResolver_StickyIsolatedPerPeer(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, time.Hour)
	aff := newFakeAffinityStore()
	r.SetAffinityStore(aff, time.Hour)

	r.Resolve(context.Background(), chID, "peer-A", "", "direct", MediaKindText, false)
	r.Resolve(context.Background(), chID, "peer-B", "", "direct", MediaKindText, false)

	if aff.upsertCalls != 2 {
		t.Fatalf("each new peer should upsert its own binding; got %d", aff.upsertCalls)
	}
	if len(aff.rows) != 2 {
		t.Fatalf("expected 2 sticky rows (peer-A + peer-B); got %d", len(aff.rows))
	}
}

// Snapshot semantics: tool_allow captured at bind time, immune to mid-conversation rule edits.
func TestResolver_StickyToolAllowSnapshot(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	orig := []string{"A", "B"}
	origPtr := &orig
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, origPtr)},
	}}
	r := NewAgentRouteResolver(fs, time.Hour)
	aff := newFakeAffinityStore()
	r.SetAffinityStore(aff, time.Hour)

	// 1st call: snapshot ["A","B"] stored.
	_, allow1, _, _ := r.Resolve(context.Background(), chID, "peer-X", "", "direct", MediaKindText, false)
	if len(allow1) != 2 {
		t.Fatalf("1st call tool_allow: got %v", allow1)
	}

	// Operator edits route tool_allow to ["C"]. Cache flushed.
	newAllow := []string{"C"}
	fs.routes[chID][0].ToolAllow = &newAllow
	r.invalidateLocal(chID)

	// 2nd call: sticky returns snapshot ["A","B"], NOT new ["C"].
	_, allow2, _, _ := r.Resolve(context.Background(), chID, "peer-X", "", "direct", MediaKindText, false)
	if len(allow2) != 2 || allow2[0] != "A" || allow2[1] != "B" {
		t.Fatalf("sticky should serve snapshot ['A','B'], not new rule ['C']; got %v", allow2)
	}
}

// Empty peerID bypasses sticky entirely (backward-compat for callers that
// haven't been updated yet OR don't have peer info).
func TestResolver_EmptyPeerIDBypassesSticky(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, time.Hour)
	aff := newFakeAffinityStore()
	r.SetAffinityStore(aff, time.Hour)

	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)

	if aff.getCalls != 0 {
		t.Fatalf("empty peerID must NOT touch affinity store; got %d Get calls", aff.getCalls)
	}
	if aff.upsertCalls != 0 {
		t.Fatalf("empty peerID must NOT upsert; got %d Upsert calls", aff.upsertCalls)
	}
}

// Affinity Upsert failure is logged but doesn't block routing decision —
// fail-open contract preserved.
func TestResolver_StickyUpsertFailureDoesNotBlock(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, time.Hour)
	aff := newFakeAffinityStore()
	aff.upsertErr = errors.New("PG down")
	r.SetAffinityStore(aff, time.Hour)

	a, _, m, err := r.Resolve(context.Background(), chID, "peer-X", "", "direct", MediaKindText, false)
	if err != nil {
		t.Fatalf("rule match must succeed even when affinity Upsert fails; err=%v", err)
	}
	if !m || a != agent {
		t.Fatalf("rule resolution should still return agent; got agent=%v matched=%v", a, m)
	}
}

// fakeInvalidatePublisher captures Publish calls so tests assert that
// Invalidate fans out to peers in addition to evicting the local cache.
type fakeInvalidatePublisher struct {
	published []uuid.UUID
	err       error
}

func (f *fakeInvalidatePublisher) Publish(_ context.Context, id uuid.UUID) error {
	f.published = append(f.published, id)
	return f.err
}

// Invalidate must (a) evict local cache AND (b) call publisher.Publish when a
// publisher is wired. The local eviction must happen FIRST so even if the
// publisher fails, this node converges immediately.
func TestResolver_InvalidatePublishesToPeers(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, time.Hour)
	pub := &fakeInvalidatePublisher{}
	r.SetInvalidatePublisher(pub)

	// Warm cache.
	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if fs.calls != 1 {
		t.Fatalf("warm call: want 1; got %d", fs.calls)
	}

	r.Invalidate(chID)

	// Local cache must be evicted: next Resolve re-queries the store.
	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if fs.calls != 2 {
		t.Fatalf("post-invalidate Resolve must hit store; got %d total calls", fs.calls)
	}
	// Publisher saw exactly 1 event for the right channel ID.
	if len(pub.published) != 1 || pub.published[0] != chID {
		t.Fatalf("publisher should have been called once with chID=%s; got %v", chID, pub.published)
	}
}

// Publisher Publish() failure MUST NOT prevent local eviction — fail-open by
// design (TTL safety net catches up peer nodes; local node converges anyway).
func TestResolver_InvalidateSurvivesPublisherFailure(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, time.Hour)
	r.SetInvalidatePublisher(&fakeInvalidatePublisher{err: errors.New("broker down")})

	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	r.Invalidate(chID)
	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if fs.calls != 2 {
		t.Fatal("local eviction must happen even if publisher fails")
	}
}

// InvalidateLocal is the subscriber-side path: evicts local cache WITHOUT
// re-publishing (else 2 nodes would loop events forever).
func TestResolver_InvalidateLocalDoesNotRepublish(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, time.Hour)
	pub := &fakeInvalidatePublisher{}
	r.SetInvalidatePublisher(pub)

	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	r.InvalidateLocal(chID)

	if len(pub.published) != 0 {
		t.Fatalf("InvalidateLocal must NOT re-publish (would create event loop); got %v", pub.published)
	}
	// But local cache IS evicted.
	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if fs.calls != 2 {
		t.Fatalf("InvalidateLocal should evict cache; got %d total store calls", fs.calls)
	}
}

func TestResolver_InvalidateClearsEntry(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	agent := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{routes: map[uuid.UUID][]store.ChannelAgentRouteData{
		chID: {newRoute(agent, "direct", nil, false, 100, true, nil)},
	}}
	r := NewAgentRouteResolver(fs, time.Minute)
	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if fs.calls != 1 {
		t.Fatalf("expected 1 store call; got %d", fs.calls)
	}
	r.Invalidate(chID)
	r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if fs.calls != 2 {
		t.Fatalf("after Invalidate next Resolve must re-query store; got %d", fs.calls)
	}
}

func TestResolver_StoreErrorBubblesUp(t *testing.T) {
	chID := uuid.Must(uuid.NewV7())
	fs := &fakeRouteStore{
		routes:  map[uuid.UUID][]store.ChannelAgentRouteData{},
		listErr: errors.New("db down"),
	}
	r := NewAgentRouteResolver(fs, 0)
	_, _, m, err := r.Resolve(context.Background(), chID, "", "", "direct", MediaKindText, false)
	if err == nil {
		t.Fatal("expected store error to bubble up")
	}
	if m {
		t.Fatal("matched must be false on error")
	}
}
