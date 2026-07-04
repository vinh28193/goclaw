package msgintent

import (
	"strings"
	"testing"
)

var allTones = []Tone{ToneHumble, ToneCasual, ToneBusiness, ToneMinimal}
var allLocales = []string{"en", "vi", "zh"}

func TestRenderAllCombinationsNonEmpty(t *testing.T) {
	vars := map[string]string{"url": "https://s.aff/abc", "rate": "12.5", "source": "official_api"}
	for _, intent := range []Intent{IntentShortlinkOffer, IntentCommissionLookup, IntentOffScopeQuestion} {
		for _, tone := range allTones {
			for _, locale := range allLocales {
				for seed := range uint64(5) {
					got := Render(intent, tone, locale, vars, seed)
					if got == "" {
						t.Fatalf("empty render: intent=%s tone=%s locale=%s seed=%d", intent, tone, locale, seed)
					}
					if strings.Contains(got, "{url}") || strings.Contains(got, "{rate}") || strings.Contains(got, "{source}") {
						t.Fatalf("unsubstituted placeholder in %q (intent=%s tone=%s locale=%s)", got, intent, tone, locale)
					}
					if strings.HasPrefix(got, "msgintent.") {
						t.Fatalf("raw i18n key leaked: %q (locale=%s)", got, locale)
					}
				}
			}
		}
	}
}

func TestRenderContainsVars(t *testing.T) {
	got := Render(IntentShortlinkOffer, ToneHumble, "vi", map[string]string{"url": "https://s.aff/xyz"}, 1)
	if !strings.Contains(got, "https://s.aff/xyz") {
		t.Fatalf("rendered template missing url: %q", got)
	}
	got = Render(IntentCommissionLookup, ToneBusiness, "en", map[string]string{"rate": "8", "source": "cache"}, 0)
	if !strings.Contains(got, "8%") || !strings.Contains(got, "cache") {
		t.Fatalf("rendered commission missing vars: %q", got)
	}
}

func TestRenderSeedDeterminism(t *testing.T) {
	vars := map[string]string{"url": "u"}
	a := Render(IntentShortlinkOffer, ToneHumble, "vi", vars, 42)
	b := Render(IntentShortlinkOffer, ToneHumble, "vi", vars, 42)
	if a != b {
		t.Fatalf("same seed produced different output: %q vs %q", a, b)
	}
	// pool size 3 → seeds 0,1,2 give distinct templates
	s0 := Render(IntentShortlinkOffer, ToneHumble, "vi", vars, 0)
	s1 := Render(IntentShortlinkOffer, ToneHumble, "vi", vars, 1)
	if s0 == s1 {
		t.Fatalf("different seeds picked the same template: %q", s0)
	}
}

func TestRenderFallbacks(t *testing.T) {
	// unknown tone falls back to humble
	got := Render(IntentShortlinkOffer, Tone("bogus"), "vi", map[string]string{"url": "u"}, 0)
	if got == "" {
		t.Fatal("unknown tone must fall back to humble, got empty")
	}
	// unknown locale falls back to en via i18n.Normalize
	got = Render(IntentShortlinkOffer, ToneHumble, "fr", map[string]string{"url": "u"}, 0)
	if got == "" || strings.HasPrefix(got, "msgintent.") {
		t.Fatalf("unknown locale must fall back to en, got %q", got)
	}
	// degraded + decline never empty
	if RenderDegraded("vi", 0) == "" || RenderDecline("vi", 0) == "" {
		t.Fatal("degraded/decline pools must never render empty")
	}
}

func TestValidTone(t *testing.T) {
	for _, tone := range allTones {
		if !ValidTone(string(tone)) {
			t.Fatalf("tone %q should be valid", tone)
		}
	}
	if ValidTone("formal") || ValidTone("") {
		t.Fatal("unknown tones must be rejected")
	}
}

func TestSeedFromStringStable(t *testing.T) {
	if SeedFromString("msg-123") != SeedFromString("msg-123") {
		t.Fatal("seed must be stable for the same input")
	}
	if SeedFromString("msg-123") == SeedFromString("msg-124") {
		t.Fatal("different inputs should (practically) differ")
	}
}
