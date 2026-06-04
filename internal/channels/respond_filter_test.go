package channels

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// ---------------------------------------------------------------------------
// Provider stub
// ---------------------------------------------------------------------------

// stubProvider is a minimal providers.Provider for classifier tests.
// Only Chat() is implemented; other methods panic if called.
type stubProvider struct {
	callCount int
	content   string // canned response content
	err       error  // if non-nil, Chat returns this error
	t         *testing.T
	expectCall bool // if false, any Chat() call triggers t.Fatalf
}

func (s *stubProvider) Chat(_ context.Context, _ providers.ChatRequest) (*providers.ChatResponse, error) {
	s.callCount++
	if !s.expectCall {
		s.t.Fatalf("stubProvider.Chat called unexpectedly (call #%d)", s.callCount)
	}
	if s.err != nil {
		return nil, s.err
	}
	return &providers.ChatResponse{Content: s.content}, nil
}

func (s *stubProvider) ChatStream(_ context.Context, _ providers.ChatRequest, _ func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	panic("ChatStream: not implemented in test stub")
}

func (s *stubProvider) DefaultModel() string { return "stub-model" }
func (s *stubProvider) Name() string         { return "stub" }

// noCallProvider is a provider that fails the test if Chat is ever invoked.
func noCallProvider(t *testing.T) *stubProvider {
	t.Helper()
	return &stubProvider{t: t, expectCall: false}
}

// cannedProvider returns a provider that returns canned content and expects Chat calls.
func cannedProvider(t *testing.T, content string) *stubProvider {
	t.Helper()
	return &stubProvider{t: t, expectCall: true, content: content}
}

// errProvider returns a provider whose Chat always returns an error.
func errProvider(t *testing.T) *stubProvider {
	t.Helper()
	return &stubProvider{t: t, expectCall: true, err: errors.New("rpc error")}
}

// ---------------------------------------------------------------------------
// Helper: build a json.RawMessage wrapping a respond_filter config.
// ---------------------------------------------------------------------------

func makeFilterJSON(f *RespondFilter) json.RawMessage {
	type wrapper struct {
		RF *RespondFilter `json:"respond_filter"`
	}
	b, err := json.Marshal(wrapper{RF: f})
	if err != nil {
		panic(err)
	}
	return b
}

// ---------------------------------------------------------------------------
// ParseRespondFilter tests
// ---------------------------------------------------------------------------

func TestParseRespondFilter_NilEmpty(t *testing.T) {
	if ParseRespondFilter(nil) != nil {
		t.Error("nil cfg: expected nil filter")
	}
	if ParseRespondFilter(json.RawMessage{}) != nil {
		t.Error("empty cfg: expected nil filter")
	}
}

func TestParseRespondFilter_ModeOff(t *testing.T) {
	raw := makeFilterJSON(&RespondFilter{Mode: "off"})
	if ParseRespondFilter(raw) != nil {
		t.Error("mode=off: expected nil filter")
	}
}

func TestParseRespondFilter_ModeEmpty(t *testing.T) {
	raw := makeFilterJSON(&RespondFilter{Mode: ""})
	if ParseRespondFilter(raw) != nil {
		t.Error("mode='': expected nil filter")
	}
}

func TestParseRespondFilter_ValidMode(t *testing.T) {
	for _, mode := range []string{"regex", "classifier", "hybrid"} {
		raw := makeFilterJSON(&RespondFilter{Mode: mode})
		f := ParseRespondFilter(raw)
		if f == nil {
			t.Errorf("mode=%q: expected non-nil filter", mode)
			continue
		}
		if f.ApplyScope != filterScopeBoth {
			t.Errorf("mode=%q: default ApplyScope want %q, got %q", mode, filterScopeBoth, f.ApplyScope)
		}
	}
}

func TestParseRespondFilter_RespondFilterKeyAbsent(t *testing.T) {
	// Config has no respond_filter key.
	raw := json.RawMessage(`{"other_key": "value"}`)
	if ParseRespondFilter(raw) != nil {
		t.Error("absent key: expected nil filter")
	}
}

func TestParseRespondFilter_InvalidJSON(t *testing.T) {
	if ParseRespondFilter(json.RawMessage(`not json`)) != nil {
		t.Error("invalid JSON: expected nil filter")
	}
}

// ---------------------------------------------------------------------------
// InScope tests
// ---------------------------------------------------------------------------

func TestInScope_Both(t *testing.T) {
	f := &RespondFilter{Mode: "regex", ApplyScope: filterScopeBoth}
	if !f.InScope("direct") {
		t.Error("scope=both: direct should be in scope")
	}
	if !f.InScope("group") {
		t.Error("scope=both: group should be in scope")
	}
}

func TestInScope_Direct(t *testing.T) {
	f := &RespondFilter{Mode: "regex", ApplyScope: filterScopeDirect}
	if !f.InScope("direct") {
		t.Error("scope=direct: direct should be in scope")
	}
	if f.InScope("group") {
		t.Error("scope=direct: group should NOT be in scope")
	}
}

func TestInScope_Group(t *testing.T) {
	f := &RespondFilter{Mode: "regex", ApplyScope: filterScopeGroup}
	if f.InScope("direct") {
		t.Error("scope=group: direct should NOT be in scope")
	}
	if !f.InScope("group") {
		t.Error("scope=group: group should be in scope")
	}
}

// ---------------------------------------------------------------------------
// Stage1 tests
// ---------------------------------------------------------------------------

func TestStage1_URLDomainMatch(t *testing.T) {
	f := &RespondFilter{
		Mode:       "regex",
		URLDomains: []string{"shopee.vn"},
	}
	// URL embedded in user message
	d := f.Stage1("check this https://shopee.vn/x")
	if d != DecisionWake {
		t.Errorf("url_domain match: want Wake, got %v", d)
	}
}

func TestStage1_URLDomainCaseInsensitive(t *testing.T) {
	f := &RespondFilter{
		Mode:       "regex",
		URLDomains: []string{"Shopee.VN"},
	}
	d := f.Stage1("visit shopee.vn today")
	if d != DecisionWake {
		t.Errorf("url_domain case-insensitive match: want Wake, got %v", d)
	}
}

func TestStage1_KeywordMatch(t *testing.T) {
	f := &RespondFilter{
		Mode:     "regex",
		Keywords: []string{"hoa hồng"},
	}
	d := f.Stage1("tôi muốn hỏi về hoa hồng của sản phẩm")
	if d != DecisionWake {
		t.Errorf("keyword match: want Wake, got %v", d)
	}
}

func TestStage1_KeywordCaseInsensitive(t *testing.T) {
	f := &RespondFilter{
		Mode:     "regex",
		Keywords: []string{"COMMISSION"},
	}
	d := f.Stage1("What is the commission rate?")
	if d != DecisionWake {
		t.Errorf("keyword case-insensitive: want Wake, got %v", d)
	}
}

func TestStage1_TrivialShort(t *testing.T) {
	f := &RespondFilter{Mode: "regex"}
	// "hi" = 2 runes < trivialMaxChars(4) → Ambiguous
	d := f.Stage1("hi")
	if d != DecisionAmbiguous {
		t.Errorf("trivial 'hi': want Ambiguous, got %v", d)
	}
}

func TestStage1_TrivialSingleEmoji(t *testing.T) {
	f := &RespondFilter{Mode: "regex"}
	d := f.Stage1("ok")
	if d != DecisionAmbiguous {
		t.Errorf("trivial 'ok': want Ambiguous, got %v", d)
	}
}

func TestStage1_NormalTextNoSignal(t *testing.T) {
	f := &RespondFilter{Mode: "regex"}
	d := f.Stage1("Hello there, how are you doing today?")
	if d != DecisionAmbiguous {
		t.Errorf("normal text no signal: want Ambiguous, got %v", d)
	}
}

func TestStage1_EmptyKeywordsURLs(t *testing.T) {
	// Empty string in slice should not trigger wake
	f := &RespondFilter{
		Mode:       "regex",
		URLDomains: []string{""},
		Keywords:   []string{""},
	}
	d := f.Stage1("check this out")
	if d != DecisionAmbiguous {
		t.Errorf("empty domains/keywords: want Ambiguous, got %v", d)
	}
}

// ---------------------------------------------------------------------------
// Evaluate tests — regex mode
// ---------------------------------------------------------------------------

func TestEvaluate_Regex_Stage1Wake_NoProviderCall(t *testing.T) {
	f := &RespondFilter{
		Mode:       filterModeRegex,
		URLDomains: []string{"example.com"},
		ApplyScope: filterScopeBoth,
	}
	p := noCallProvider(t)
	d := f.Evaluate(context.Background(), "visit example.com now", p, "m")
	if d != DecisionWake {
		t.Errorf("regex+wake: want Wake, got %v", d)
	}
	if p.callCount != 0 {
		t.Errorf("provider should not be called on Stage1 Wake")
	}
}

func TestEvaluate_Regex_Ambiguous_OnNoMatchIgnore_Drop(t *testing.T) {
	f := &RespondFilter{
		Mode:       filterModeRegex,
		OnNoMatch:  "ignore", // ignore = drop
		ApplyScope: filterScopeBoth,
	}
	p := noCallProvider(t)
	d := f.Evaluate(context.Background(), "hello there how are you", p, "m")
	if d != DecisionDrop {
		t.Errorf("regex+ambiguous+ignore: want Drop, got %v", d)
	}
}

func TestEvaluate_Regex_Ambiguous_OnNoMatchWake(t *testing.T) {
	f := &RespondFilter{
		Mode:       filterModeRegex,
		OnNoMatch:  "wake",
		ApplyScope: filterScopeBoth,
	}
	p := noCallProvider(t)
	d := f.Evaluate(context.Background(), "hello there how are you", p, "m")
	if d != DecisionWake {
		t.Errorf("regex+ambiguous+wake: want Wake, got %v", d)
	}
}

// ---------------------------------------------------------------------------
// Evaluate tests — hybrid mode
// ---------------------------------------------------------------------------

func TestEvaluate_Hybrid_Stage1Wake_NoProviderCall(t *testing.T) {
	f := &RespondFilter{
		Mode:     filterModeHybrid,
		Keywords: []string{"commission"},
	}
	p := noCallProvider(t)
	d := f.Evaluate(context.Background(), "what is the commission?", p, "m")
	if d != DecisionWake {
		t.Errorf("hybrid+stage1wake: want Wake, got %v", d)
	}
	if p.callCount != 0 {
		t.Errorf("hybrid: provider must NOT be called when Stage1 returns Wake")
	}
}

func TestEvaluate_Hybrid_Ambiguous_ClassifierRELEVANT(t *testing.T) {
	f := &RespondFilter{
		Mode: filterModeHybrid,
	}
	p := cannedProvider(t, "RELEVANT")
	d := f.Evaluate(context.Background(), "I want to know about the product", p, "m")
	if d != DecisionWake {
		t.Errorf("hybrid+ambiguous+RELEVANT: want Wake, got %v", d)
	}
	if p.callCount != 1 {
		t.Errorf("provider should be called exactly once, got %d", p.callCount)
	}
}

func TestEvaluate_Hybrid_Ambiguous_ClassifierIGNORE(t *testing.T) {
	f := &RespondFilter{
		Mode: filterModeHybrid,
	}
	p := cannedProvider(t, "IGNORE")
	d := f.Evaluate(context.Background(), "just saying hello to you", p, "m")
	if d != DecisionDrop {
		t.Errorf("hybrid+ambiguous+IGNORE: want Drop, got %v", d)
	}
	if p.callCount != 1 {
		t.Errorf("provider should be called exactly once, got %d", p.callCount)
	}
}

func TestEvaluate_Hybrid_Ambiguous_ClassifierGarbage_FallsBackToOnNoMatch(t *testing.T) {
	f := &RespondFilter{
		Mode:      filterModeHybrid,
		OnNoMatch: "ignore", // default → drop
	}
	p := cannedProvider(t, "MAYBE_SOMETHING_ELSE")
	d := f.Evaluate(context.Background(), "random long message about nothing in particular here", p, "m")
	if d != DecisionDrop {
		t.Errorf("hybrid+garbage: want Drop (on_no_match=ignore), got %v", d)
	}
}

func TestEvaluate_Hybrid_Ambiguous_ClassifierGarbage_OnNoMatchWake(t *testing.T) {
	f := &RespondFilter{
		Mode:      filterModeHybrid,
		OnNoMatch: "wake",
	}
	p := cannedProvider(t, "DUNNO")
	d := f.Evaluate(context.Background(), "random long message about nothing at all here", p, "m")
	if d != DecisionWake {
		t.Errorf("hybrid+garbage+on_no_match=wake: want Wake, got %v", d)
	}
}

// ---------------------------------------------------------------------------
// Evaluate tests — classifier mode
// ---------------------------------------------------------------------------

func TestEvaluate_Classifier_AlwaysCallsProvider_RELEVANT(t *testing.T) {
	// classifier mode: skips Stage1, goes straight to classifier.
	f := &RespondFilter{
		Mode: filterModeClassifier,
	}
	p := cannedProvider(t, "RELEVANT")
	// Short trivial message — Stage1 would say Ambiguous, but classifier skips Stage1.
	d := f.Evaluate(context.Background(), "hi", p, "m")
	if d != DecisionWake {
		t.Errorf("classifier+RELEVANT: want Wake, got %v", d)
	}
	if p.callCount != 1 {
		t.Errorf("classifier: provider must be called once, got %d", p.callCount)
	}
}

func TestEvaluate_Classifier_IGNORE(t *testing.T) {
	f := &RespondFilter{Mode: filterModeClassifier}
	p := cannedProvider(t, "IGNORE")
	// Even a keyword-bearing message — classifier decides exclusively.
	d := f.Evaluate(context.Background(), "commission", p, "m")
	if d != DecisionDrop {
		t.Errorf("classifier+IGNORE: want Drop, got %v", d)
	}
}

// ---------------------------------------------------------------------------
// Evaluate tests — fail-open scenarios
// ---------------------------------------------------------------------------

func TestEvaluate_NilProvider_FailOpen(t *testing.T) {
	// hybrid/classifier mode with nil provider → Wake (fail-open).
	for _, mode := range []string{filterModeHybrid, filterModeClassifier} {
		f := &RespondFilter{Mode: mode}
		// Ambiguous content to trigger stage2 path
		d := f.Evaluate(context.Background(), "some long ambiguous message here for testing", nil, "m")
		if d != DecisionWake {
			t.Errorf("nil provider mode=%q: want Wake (fail-open), got %v", mode, d)
		}
	}
}

func TestEvaluate_ProviderError_FailOpen(t *testing.T) {
	f := &RespondFilter{Mode: filterModeHybrid}
	p := errProvider(t)
	// Ambiguous content to trigger stage2
	d := f.Evaluate(context.Background(), "some long ambiguous message without known keywords", p, "m")
	if d != DecisionWake {
		t.Errorf("provider error: want Wake (fail-open), got %v", d)
	}
}

func TestEvaluate_UnknownMode_FailOpen(t *testing.T) {
	f := &RespondFilter{Mode: "totally_unknown"}
	p := noCallProvider(t)
	d := f.Evaluate(context.Background(), "test message", p, "m")
	if d != DecisionWake {
		t.Errorf("unknown mode: want Wake (fail-open), got %v", d)
	}
}

// ---------------------------------------------------------------------------
// Classifier response parsing edge cases
// ---------------------------------------------------------------------------

func TestEvaluate_Hybrid_RELEVANTWithWhitespace(t *testing.T) {
	// Classifier output may have extra whitespace/newlines — impl does TrimSpace+ToUpper.
	f := &RespondFilter{Mode: filterModeHybrid}
	p := cannedProvider(t, "  relevant  ")
	d := f.Evaluate(context.Background(), "tell me about the referral link", p, "m")
	if d != DecisionWake {
		t.Errorf("relevant with whitespace: want Wake, got %v", d)
	}
}

func TestEvaluate_Hybrid_RELEVANTEmbeddedInSentence(t *testing.T) {
	// strings.Contains is used, so "RELEVANT." should still match.
	f := &RespondFilter{Mode: filterModeHybrid}
	p := cannedProvider(t, "RELEVANT.")
	d := f.Evaluate(context.Background(), "how do I earn commission here", p, "m")
	if d != DecisionWake {
		t.Errorf("RELEVANT. embedded: want Wake, got %v", d)
	}
}

// ---------------------------------------------------------------------------
// Stage1 boundary: exactly trivialMaxChars runes
// ---------------------------------------------------------------------------

func TestStage1_AtTrivialBoundary(t *testing.T) {
	f := &RespondFilter{Mode: "regex"}
	// trivialMaxChars = 4; exactly 4 runes → NOT trivial → Ambiguous (no keywords)
	// "abcd" = 4 runes: RuneCountInString >= trivialMaxChars so falls through to Ambiguous
	d := f.Stage1("abcd")
	// Stage1 has no "normal text = Wake" path — non-trivial non-keyword content is still Ambiguous
	if d != DecisionAmbiguous {
		t.Errorf("4 rune message: want Ambiguous, got %v", d)
	}
}

func TestStage1_BelowTrivialBoundary(t *testing.T) {
	f := &RespondFilter{Mode: "regex"}
	// 3 runes < 4 → trivial → Ambiguous
	d := f.Stage1("abc")
	if d != DecisionAmbiguous {
		t.Errorf("3 rune message: want Ambiguous, got %v", d)
	}
}
