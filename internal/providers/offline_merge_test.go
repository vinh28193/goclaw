package providers

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/msgintent"
)

func TestParseAgentOfflineOverride_EmptyReturnsZero(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("{}")} {
		got := ParseAgentOfflineOverride(raw)
		if got.Tone != "" || got.Locale != "" || got.ReplyPrefix != "" ||
			got.HelpText != "" || got.Templates != nil || got.IntentConfig != nil {
			t.Fatalf("expected zero-value OfflineSettings for %q, got %+v", string(raw), got)
		}
	}
}

func TestParseAgentOfflineOverride_MalformedJSON(t *testing.T) {
	got := ParseAgentOfflineOverride(json.RawMessage(`{"tone":`))
	if got.Tone != "" || got.Locale != "" {
		t.Fatalf("malformed JSON should yield zero value, got %+v", got)
	}
}

func TestMergeOfflineSettings_AgentEmptyProviderWins(t *testing.T) {
	provider := OfflineSettings{
		Tone: "humble", Locale: "vi", ReplyPrefix: "[p]", HelpText: "help",
		Templates: map[string][]string{SlotOpener: {"hi {url}"}},
		IntentConfig: &msgintent.KeywordOverrides{
			CommissionKeywords: []string{"ck"},
		},
	}
	got := MergeOfflineSettings(OfflineSettings{}, provider)
	if got.Tone != "humble" || got.Locale != "vi" || got.ReplyPrefix != "[p]" || got.HelpText != "help" {
		t.Fatalf("expected provider fields to win, got %+v", got)
	}
	if !reflect.DeepEqual(got.Templates[SlotOpener], []string{"hi {url}"}) {
		t.Fatalf("expected provider template, got %+v", got.Templates)
	}
	if got.IntentConfig == nil || !reflect.DeepEqual(got.IntentConfig.CommissionKeywords, []string{"ck"}) {
		t.Fatalf("expected provider intent kept, got %+v", got.IntentConfig)
	}
}

func TestMergeOfflineSettings_AgentToneOverrides(t *testing.T) {
	got := MergeOfflineSettings(
		OfflineSettings{Tone: "casual"},
		OfflineSettings{Tone: "humble", Locale: "vi"},
	)
	if got.Tone != "casual" {
		t.Fatalf("expected agent tone to win, got %q", got.Tone)
	}
	if got.Locale != "vi" {
		t.Fatalf("expected provider locale kept, got %q", got.Locale)
	}
}

func TestMergeOfflineSettings_AgentInvalidToneFallsBack(t *testing.T) {
	got := MergeOfflineSettings(
		OfflineSettings{Tone: "grumpy"},
		OfflineSettings{Tone: "humble"},
	)
	if got.Tone != "humble" {
		t.Fatalf("invalid agent tone should fall back to provider, got %q", got.Tone)
	}
}

func TestMergeOfflineSettings_ReplyPrefixHelpTextOverride(t *testing.T) {
	got := MergeOfflineSettings(
		OfflineSettings{ReplyPrefix: "[a]", HelpText: "agent help"},
		OfflineSettings{ReplyPrefix: "[p]", HelpText: "provider help"},
	)
	if got.ReplyPrefix != "[a]" || got.HelpText != "agent help" {
		t.Fatalf("expected agent prefix/help to win, got %+v", got)
	}
}

func TestMergeOfflineSettings_TemplatesPerSlotOverride(t *testing.T) {
	agent := OfflineSettings{Templates: map[string][]string{
		SlotOpener:  {"agent opener {url}"},
		SlotDecline: {"agent decline"},
	}}
	provider := OfflineSettings{Templates: map[string][]string{
		SlotOpener:   {"provider opener"},
		SlotDegraded: {"provider degraded"},
	}}
	got := MergeOfflineSettings(agent, provider)
	if !reflect.DeepEqual(got.Templates[SlotOpener], []string{"agent opener {url}"}) {
		t.Fatalf("opener should be agent's, got %+v", got.Templates[SlotOpener])
	}
	if !reflect.DeepEqual(got.Templates[SlotDecline], []string{"agent decline"}) {
		t.Fatalf("decline should be agent's, got %+v", got.Templates[SlotDecline])
	}
	if !reflect.DeepEqual(got.Templates[SlotDegraded], []string{"provider degraded"}) {
		t.Fatalf("degraded should stay provider's, got %+v", got.Templates[SlotDegraded])
	}
}

func TestMergeOfflineSettings_TemplatesEmptyAgentPoolFallsBack(t *testing.T) {
	agent := OfflineSettings{Templates: map[string][]string{
		SlotOpener: {}, // present but empty → not an override
	}}
	provider := OfflineSettings{Templates: map[string][]string{
		SlotOpener: {"provider opener"},
	}}
	got := MergeOfflineSettings(agent, provider)
	if !reflect.DeepEqual(got.Templates[SlotOpener], []string{"provider opener"}) {
		t.Fatalf("empty agent pool should fall back to provider, got %+v", got.Templates[SlotOpener])
	}
}

func TestMergeOfflineSettings_IntentConfigPerListOverride(t *testing.T) {
	agent := OfflineSettings{IntentConfig: &msgintent.KeywordOverrides{
		CommissionKeywords: []string{"hh"},
	}}
	provider := OfflineSettings{IntentConfig: &msgintent.KeywordOverrides{
		BroadcastKeywords: []string{"bc"},
		QuestionKeywords:  []string{"?"},
	}}
	got := MergeOfflineSettings(agent, provider)
	if got.IntentConfig == nil {
		t.Fatal("expected non-nil intent config")
	}
	if !reflect.DeepEqual(got.IntentConfig.CommissionKeywords, []string{"hh"}) {
		t.Fatalf("commission from agent, got %+v", got.IntentConfig.CommissionKeywords)
	}
	if !reflect.DeepEqual(got.IntentConfig.BroadcastKeywords, []string{"bc"}) {
		t.Fatalf("broadcast from provider, got %+v", got.IntentConfig.BroadcastKeywords)
	}
	if !reflect.DeepEqual(got.IntentConfig.QuestionKeywords, []string{"?"}) {
		t.Fatalf("question from provider, got %+v", got.IntentConfig.QuestionKeywords)
	}
}

func TestMergeOfflineSettings_IntentConfigBothNil(t *testing.T) {
	got := MergeOfflineSettings(OfflineSettings{}, OfflineSettings{})
	if got.IntentConfig != nil {
		t.Fatalf("expected nil intent config, got %+v", got.IntentConfig)
	}
}

func TestMergeOfflineSettings_Idempotent(t *testing.T) {
	agent := OfflineSettings{
		Tone: "casual", ReplyPrefix: "[a]",
		Templates: map[string][]string{SlotOpener: {"a"}},
		IntentConfig: &msgintent.KeywordOverrides{
			CommissionKeywords: []string{"ck"},
		},
	}
	provider := OfflineSettings{
		Tone: "humble", Locale: "vi",
		Templates: map[string][]string{SlotDegraded: {"p degraded"}},
	}
	once := MergeOfflineSettings(agent, provider)
	twice := MergeOfflineSettings(agent, once)
	if !reflect.DeepEqual(once, twice) {
		t.Fatalf("merge should be idempotent\nonce:  %+v\ntwice: %+v", once, twice)
	}
}

func TestMergeOfflineSettings_DoesNotAliasProviderMap(t *testing.T) {
	provider := OfflineSettings{Templates: map[string][]string{
		SlotOpener: {"p"},
	}}
	agent := OfflineSettings{Templates: map[string][]string{
		SlotDecline: {"a"},
	}}
	merged := MergeOfflineSettings(agent, provider)
	// Mutating merged.Templates must not leak into provider.Templates.
	merged.Templates[SlotOpener] = []string{"mutated"}
	if !reflect.DeepEqual(provider.Templates[SlotOpener], []string{"p"}) {
		t.Fatalf("provider template mutated via merged map alias: %+v", provider.Templates[SlotOpener])
	}
}
