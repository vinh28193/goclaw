package cmd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// ---------------------------------------------------------------------------
// Minimal stubs
// ---------------------------------------------------------------------------

// stubAgentForGate is a minimal agent.Agent stub for gate tests.
// Only Provider() and Model() are exercised by evaluateRespondGate.
type stubAgentForGate struct {
	provider providers.Provider
	model    string
}

func (s *stubAgentForGate) ID() string { return "stub" }
func (s *stubAgentForGate) UUID() uuid.UUID { return uuid.Nil }
func (s *stubAgentForGate) OtherConfig() json.RawMessage { return nil }
func (s *stubAgentForGate) Run(_ context.Context, _ agent.RunRequest) (*agent.RunResult, error) {
	return nil, nil
}
func (s *stubAgentForGate) IsRunning() bool              { return false }
func (s *stubAgentForGate) Model() string                { return s.model }
func (s *stubAgentForGate) ProviderName() string         { return "stub" }
func (s *stubAgentForGate) Provider() providers.Provider { return s.provider }

// gateStubProvider is a minimal providers.Provider that records Chat calls.
type gateStubProvider struct {
	content    string
	err        error
	callCount  int
	t          *testing.T
	expectCall bool
}

func (p *gateStubProvider) Chat(_ context.Context, _ providers.ChatRequest) (*providers.ChatResponse, error) {
	p.callCount++
	if !p.expectCall {
		p.t.Fatalf("Chat called unexpectedly (call #%d)", p.callCount)
	}
	if p.err != nil {
		return nil, p.err
	}
	return &providers.ChatResponse{Content: p.content}, nil
}
func (p *gateStubProvider) ChatStream(_ context.Context, _ providers.ChatRequest, _ func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	panic("ChatStream not implemented in gate test stub")
}
func (p *gateStubProvider) DefaultModel() string { return "stub-model" }
func (p *gateStubProvider) Name() string         { return "stub" }

// noCallGateProvider fails the test if Chat is ever called.
func noCallGateProvider(t *testing.T) *gateStubProvider {
	t.Helper()
	return &gateStubProvider{t: t, expectCall: false}
}

// cannedGateProvider returns a provider that returns a fixed classifier response.
func cannedGateProvider(t *testing.T, content string) *gateStubProvider {
	t.Helper()
	return &gateStubProvider{t: t, expectCall: true, content: content}
}

// buildManager creates a Manager with a respond filter set for a given channel.
// cfg is raw JSONB for the instance config (must include respond_filter).
func buildManager(t *testing.T, channelName string, cfg json.RawMessage) *channels.Manager {
	t.Helper()
	mgr := channels.NewManager(nil)
	mgr.SetRespondFilter(channelName, channels.ParseRespondFilter(cfg))
	return mgr
}

// ---------------------------------------------------------------------------
// Tests for evaluateRespondGate
// ---------------------------------------------------------------------------

func TestEvaluateRespondGate_NilMgr(t *testing.T) {
	ag := &stubAgentForGate{provider: noCallGateProvider(t), model: "m"}
	got := evaluateRespondGate(context.Background(), nil, "ch", "direct", "hello", ag)
	if got != channels.DecisionWake {
		t.Errorf("nil mgr: want Wake, got %v", got)
	}
}

func TestEvaluateRespondGate_NilFilter(t *testing.T) {
	// Manager has no filter set for channel → nil → Wake.
	mgr := channels.NewManager(nil)
	ag := &stubAgentForGate{provider: noCallGateProvider(t), model: "m"}
	got := evaluateRespondGate(context.Background(), mgr, "unknown-ch", "direct", "hello", ag)
	if got != channels.DecisionWake {
		t.Errorf("nil filter: want Wake, got %v", got)
	}
}

func TestEvaluateRespondGate_FilterOutOfScope(t *testing.T) {
	// Filter applies only to "group"; message is "direct" → out of scope → Wake.
	cfg := json.RawMessage(`{"respond_filter":{"mode":"regex","apply_scope":"group","keywords":["promo"]}}`)
	mgr := buildManager(t, "ch1", cfg)
	ag := &stubAgentForGate{provider: noCallGateProvider(t), model: "m"}
	got := evaluateRespondGate(context.Background(), mgr, "ch1", "direct", "promo deal", ag)
	if got != channels.DecisionWake {
		t.Errorf("out-of-scope: want Wake, got %v", got)
	}
}

func TestEvaluateRespondGate_Stage1KeywordMatch_Wake(t *testing.T) {
	// Regex mode, keyword matches → Wake.
	cfg := json.RawMessage(`{"respond_filter":{"mode":"regex","apply_scope":"both","keywords":["promo"]}}`)
	mgr := buildManager(t, "ch1", cfg)
	ag := &stubAgentForGate{provider: noCallGateProvider(t), model: "m"}
	got := evaluateRespondGate(context.Background(), mgr, "ch1", "direct", "check out this promo", ag)
	if got != channels.DecisionWake {
		t.Errorf("keyword match: want Wake, got %v", got)
	}
}

func TestEvaluateRespondGate_Stage1NoMatch_Drop(t *testing.T) {
	// Regex mode, no keyword/domain match, on_no_match=ignore (default) → Drop.
	cfg := json.RawMessage(`{"respond_filter":{"mode":"regex","apply_scope":"both","keywords":["promo"],"on_no_match":"ignore"}}`)
	mgr := buildManager(t, "ch1", cfg)
	ag := &stubAgentForGate{provider: noCallGateProvider(t), model: "m"}
	got := evaluateRespondGate(context.Background(), mgr, "ch1", "direct", "just saying hello world to you", ag)
	if got != channels.DecisionDrop {
		t.Errorf("no match regex: want Drop, got %v", got)
	}
}

func TestEvaluateRespondGate_ClassifierWake(t *testing.T) {
	// Classifier mode, provider returns "RELEVANT" → Wake.
	cfg := json.RawMessage(`{"respond_filter":{"mode":"classifier","apply_scope":"both"}}`)
	mgr := buildManager(t, "ch1", cfg)
	p := cannedGateProvider(t, "RELEVANT")
	ag := &stubAgentForGate{provider: p, model: "m"}
	got := evaluateRespondGate(context.Background(), mgr, "ch1", "direct", "I want to order something", ag)
	if got != channels.DecisionWake {
		t.Errorf("classifier RELEVANT: want Wake, got %v", got)
	}
	if p.callCount != 1 {
		t.Errorf("expected 1 Chat call, got %d", p.callCount)
	}
}

func TestEvaluateRespondGate_ClassifierDrop(t *testing.T) {
	// Classifier mode, provider returns "IGNORE" → Drop.
	cfg := json.RawMessage(`{"respond_filter":{"mode":"classifier","apply_scope":"both"}}`)
	mgr := buildManager(t, "ch1", cfg)
	p := cannedGateProvider(t, "IGNORE")
	ag := &stubAgentForGate{provider: p, model: "m"}
	got := evaluateRespondGate(context.Background(), mgr, "ch1", "direct", "just saying hi there", ag)
	if got != channels.DecisionDrop {
		t.Errorf("classifier IGNORE: want Drop, got %v", got)
	}
}
