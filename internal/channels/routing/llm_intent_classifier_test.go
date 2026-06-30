package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// fakeChatProvider is a minimal providers.Provider used to drive LLMIntentClassifier
// without standing up a real LLM connection.
type fakeChatProvider struct {
	resp     *providers.ChatResponse
	err      error
	lastReq  providers.ChatRequest
	calls    int
}

func (f *fakeChatProvider) Chat(_ context.Context, req providers.ChatRequest) (*providers.ChatResponse, error) {
	f.calls++
	f.lastReq = req
	return f.resp, f.err
}
func (f *fakeChatProvider) ChatStream(_ context.Context, _ providers.ChatRequest, _ func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	return nil, errors.New("unused")
}
func (f *fakeChatProvider) DefaultModel() string { return "fake-model" }
func (f *fakeChatProvider) Name() string         { return "fake" }

func TestLLMIntentClassifier_ReturnsConfiguredLabel(t *testing.T) {
	p := &fakeChatProvider{resp: &providers.ChatResponse{Content: "billing"}}
	c := NewLLMIntentClassifier(p, "haiku", []string{"billing", "support"}, "")

	got, err := c.Classify(context.Background(), "ch-X", "I want a refund")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got != "billing" {
		t.Fatalf("got %q, want billing", got)
	}
	if p.calls != 1 {
		t.Fatalf("provider should be called once; got %d", p.calls)
	}
	// Verify prompt template was expanded with labels.
	sysMsg := p.lastReq.Messages[0]
	if sysMsg.Role != "system" {
		t.Fatalf("first message should be system; got %q", sysMsg.Role)
	}
	if !containsAll(sysMsg.Content, "billing", "support") {
		t.Fatalf("system prompt should list both labels; got %q", sysMsg.Content)
	}
}

// LLM hallucinates label not in the configured set → classifier returns ""
// (treated as unknown by resolver, falls through to null-intent route).
func TestLLMIntentClassifier_RejectsUnknownLabel(t *testing.T) {
	p := &fakeChatProvider{resp: &providers.ChatResponse{Content: "marketing"}}
	c := NewLLMIntentClassifier(p, "haiku", []string{"billing", "support"}, "")

	got, err := c.Classify(context.Background(), "ch-X", "anything")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got != "" {
		t.Fatalf("unknown label must be normalized to empty; got %q", got)
	}
}

// Noise around the label (quotes, trailing punct, prefix) should still match.
func TestLLMIntentClassifier_NormalizesNoisyOutput(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"billing", "billing"},
		{"  billing  ", "billing"},
		{`"billing"`, "billing"},
		{"billing.", "billing"},
		{"Billing", "billing"}, // case-insensitive match
		{"billing — refund inquiry", "billing"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			p := &fakeChatProvider{resp: &providers.ChatResponse{Content: tc.raw}}
			c := NewLLMIntentClassifier(p, "haiku", []string{"billing"}, "")
			got, _ := c.Classify(context.Background(), "ch-X", "test")
			if got != tc.want {
				t.Fatalf("input %q → %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// Empty messageText → short-circuit, no LLM call.
func TestLLMIntentClassifier_EmptyMessageSkipsProvider(t *testing.T) {
	p := &fakeChatProvider{resp: &providers.ChatResponse{Content: "billing"}}
	c := NewLLMIntentClassifier(p, "haiku", []string{"billing"}, "")

	got, err := c.Classify(context.Background(), "ch-X", "")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got != "" {
		t.Fatalf("empty message → empty intent; got %q", got)
	}
	if p.calls != 0 {
		t.Fatalf("provider must NOT be called for empty message; got %d", p.calls)
	}
}

// Empty labels list → short-circuit (operator hasn't configured the classifier yet).
func TestLLMIntentClassifier_NoLabelsSkipsProvider(t *testing.T) {
	p := &fakeChatProvider{}
	c := NewLLMIntentClassifier(p, "haiku", nil, "")

	got, err := c.Classify(context.Background(), "ch-X", "anything")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got != "" {
		t.Fatalf("no labels → empty intent; got %q", got)
	}
	if p.calls != 0 {
		t.Fatal("provider must NOT be called when labels empty")
	}
}

// Provider error propagates as classifier error → resolver fails open.
func TestLLMIntentClassifier_PropagatesProviderError(t *testing.T) {
	p := &fakeChatProvider{err: errors.New("provider down")}
	c := NewLLMIntentClassifier(p, "haiku", []string{"billing"}, "")

	_, err := c.Classify(context.Background(), "ch-X", "test")
	if err == nil {
		t.Fatal("expected provider error to propagate")
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !containsSubstring(haystack, n) {
			return false
		}
	}
	return true
}

func containsSubstring(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && stringContains(haystack, needle))
}

// stringContains avoids importing strings just for the helper (Go test deps).
func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
