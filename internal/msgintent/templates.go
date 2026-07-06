package msgintent

import (
	"hash/fnv"
	"log/slog"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
)

// Tone controls the voice of deterministic replies.
// Stored per channel instance in channel_instances.shortlink_tone (phase 06).
type Tone string

const (
	ToneHumble   Tone = "humble" // default
	ToneCasual   Tone = "casual"
	ToneBusiness Tone = "business"
	ToneMinimal  Tone = "minimal"
)

// ValidTone reports whether s is a known tone value (app-layer validation —
// the DB column is plain TEXT by goclaw convention).
func ValidTone(s string) bool {
	switch Tone(s) {
	case ToneHumble, ToneCasual, ToneBusiness, ToneMinimal:
		return true
	}
	return false
}

// Template pools: (intent, tone) → i18n keys. Multiple variations per pool so
// repeated replies to the same user don't read botlike; minimal tone repeats
// by design. Decline/degraded pools are tone-independent.
var templatePools = map[Intent]map[Tone][]string{
	IntentShortlinkOffer: {
		ToneHumble:   {i18n.MsgIntentShortlinkHumble1, i18n.MsgIntentShortlinkHumble2, i18n.MsgIntentShortlinkHumble3},
		ToneCasual:   {i18n.MsgIntentShortlinkCasual1, i18n.MsgIntentShortlinkCasual2, i18n.MsgIntentShortlinkCasual3},
		ToneBusiness: {i18n.MsgIntentShortlinkBusiness1, i18n.MsgIntentShortlinkBusiness2, i18n.MsgIntentShortlinkBusiness3},
		ToneMinimal:  {i18n.MsgIntentShortlinkMinimal1},
	},
	IntentCommissionLookup: {
		ToneHumble:   {i18n.MsgIntentCommissionHumble1, i18n.MsgIntentCommissionHumble2, i18n.MsgIntentCommissionHumble3},
		ToneCasual:   {i18n.MsgIntentCommissionCasual1, i18n.MsgIntentCommissionCasual2},
		ToneBusiness: {i18n.MsgIntentCommissionBusiness1},
		ToneMinimal:  {i18n.MsgIntentCommissionMinimal1},
	},
	// off_scope_question uses the decline pool (same message for all tones)
	IntentOffScopeQuestion: {
		ToneHumble: {i18n.MsgIntentDecline1, i18n.MsgIntentDecline2, i18n.MsgIntentDecline3},
	},
}

// degradedPool — MCP and agent path both failed; never go silent on a business request.
var degradedPool = []string{i18n.MsgIntentDegraded1}

// Render picks a template for (intent, tone, locale) using seed for a
// deterministic-per-message pseudo-random choice, then substitutes vars
// ({url}, {rate}, {source}). Fallback chain: unknown tone → humble;
// missing locale key → en (handled inside i18n.T). Never returns "".
func Render(intent Intent, tone Tone, locale string, vars map[string]string, seed uint64) string {
	tonePools, ok := templatePools[intent]
	if !ok {
		slog.Warn("msgintent.render_unknown_intent", "intent", string(intent))
		return ""
	}
	pool, ok := tonePools[tone]
	if !ok || len(pool) == 0 {
		// Only warn when this intent actually distinguishes tones — pools with a
		// single humble entry (decline) are tone-independent by design.
		if tone != ToneHumble && len(tonePools) > 1 {
			slog.Warn("msgintent.render_tone_fallback", "intent", string(intent), "tone", string(tone))
		}
		pool = tonePools[ToneHumble]
	}
	return renderFromPool(pool, locale, vars, seed)
}

// RenderDegraded returns the "system busy" apology used when both the
// deterministic MCP path and the agent pipeline failed.
func RenderDegraded(locale string, seed uint64) string {
	return renderFromPool(degradedPool, locale, nil, seed)
}

// RenderDecline returns an off-scope decline (also used by the NO_REPLY
// finalize guard in phase 05).
func RenderDecline(locale string, seed uint64) string {
	return renderFromPool(templatePools[IntentOffScopeQuestion][ToneHumble], locale, nil, seed)
}

func renderFromPool(pool []string, locale string, vars map[string]string, seed uint64) string {
	if len(pool) == 0 {
		return ""
	}
	key := pool[seed%uint64(len(pool))]
	return RenderRaw(i18n.T(i18n.Normalize(locale), key), vars)
}

// RenderKey renders a single i18n key with {var} substitution — used for the
// rich reply block lines (product/rate) that aren't part of a variation pool.
func RenderKey(key, locale string, vars map[string]string) string {
	return RenderRaw(i18n.T(i18n.Normalize(locale), key), vars)
}

// RenderRaw substitutes {var} placeholders in a raw template string — used by
// operator-supplied template overrides (offline provider settings) that bypass
// the i18n catalog.
func RenderRaw(tmpl string, vars map[string]string) string {
	if len(vars) == 0 {
		return tmpl
	}
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// SeedFromString derives a stable template-pick seed from a message ID so the
// choice is deterministic per message (debuggable, testable) but varies
// between messages.
func SeedFromString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
