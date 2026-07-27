package providers

import (
	"encoding/json"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/msgintent"
)

// OfflineSettings is stored in llm_providers.settings JSONB.
//
// Template overrides: `templates` maps a reply SLOT to a pool of raw template
// strings ({var} placeholders substituted per slot). A slot absent from the
// map (or with an empty pool) falls back to the built-in i18n pool — so
// removing an override restores the default. Multiple entries per slot give
// per-message variation (deterministic seed pick, same as built-ins).
// For the optional rich-block lines an entry rendering to "" DROPS the line;
// the opener/decline/degraded slots never go empty (fallback to built-in).
type OfflineSettings struct {
	Tone         string                      `json:"tone"`   // casual|humble|business|minimal (default humble)
	Locale       string                      `json:"locale"` // en|vi|zh (default vi)
	ReplyPrefix  string                      `json:"reply_prefix,omitempty"` // e.g. "[offline]" — prepended to every reply (not NO_REPLY)
	HelpText     string                      `json:"help_text,omitempty"`    // value of the {help} template var; empty → built-in i18n hint
	Templates    map[string][]string         `json:"templates,omitempty"`    // slot → template pool (see Slot* consts)
	IntentConfig *msgintent.KeywordOverrides `json:"intent_config,omitempty"`
}

// Template slots — keys accepted in OfflineSettings.Templates.
const (
	SlotOpener      = "opener"            // rich-reply opening line; vars: {url}
	SlotDecline     = "decline"           // off-scope question / no-tool decline
	SlotDegraded    = "degraded"          // shortlink tool failed — "system busy" apology
	SlotProductLine = "product_line"      // 📦 line; vars: {name}; "" drops the line
	SlotRateLine    = "rate_line"         // 💵 line; vars: {rate}; "" drops the line
	SlotRateMissing = "rate_missing_line" // 💵 "no info yet" line; "" drops the line
)

// ParseOfflineSettings extracts offline provider settings with defaults.
func ParseOfflineSettings(raw json.RawMessage) OfflineSettings {
	var s OfflineSettings
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s) // zero-value fallback on malformed settings
	}
	if !msgintent.ValidTone(s.Tone) {
		s.Tone = string(msgintent.ToneHumble)
	}
	if s.Locale == "" {
		s.Locale = "vi"
	}
	return s
}

// ParseAgentOfflineOverride extracts a per-agent override from
// agent.other_config.offline (JSONB subkey). Unlike ParseOfflineSettings this
// does NOT default-fill Tone/Locale — zero values signal "this field is not
// overridden" so downstream MergeOfflineSettings can fall back to the provider
// value. Malformed / empty input yields a zero-value struct (no panic).
func ParseAgentOfflineOverride(raw json.RawMessage) OfflineSettings {
	var s OfflineSettings
	if len(raw) == 0 {
		return s
	}
	_ = json.Unmarshal(raw, &s)
	return s
}

// MergeOfflineSettings folds an agent-level override on top of provider
// defaults, per-field. Agent wins on non-empty / valid values; provider fills
// the rest. Both inputs are treated as immutable — result never aliases input
// maps. Deterministic + idempotent: MergeOfflineSettings(a, MergeOfflineSettings(a, p))
// yields the same value as MergeOfflineSettings(a, p) up to structural equality.
func MergeOfflineSettings(agent, provider OfflineSettings) OfflineSettings {
	out := provider
	if msgintent.ValidTone(agent.Tone) {
		out.Tone = agent.Tone
	}
	if agent.Locale != "" {
		out.Locale = agent.Locale
	}
	if agent.ReplyPrefix != "" {
		out.ReplyPrefix = agent.ReplyPrefix
	}
	if agent.HelpText != "" {
		out.HelpText = agent.HelpText
	}
	out.Templates = mergeTemplatePools(agent.Templates, provider.Templates)
	out.IntentConfig = mergeIntentConfig(agent.IntentConfig, provider.IntentConfig)
	return out
}

// mergeTemplatePools returns a new map holding, for each slot present in
// either input, the agent pool when it has ≥1 entry, otherwise the provider
// pool. An agent slot present but empty (`[]`) is treated as "not overridden"
// so removing all lines in the UI reverts to provider defaults.
func mergeTemplatePools(agent, provider map[string][]string) map[string][]string {
	if len(agent) == 0 && len(provider) == 0 {
		return nil
	}
	out := make(map[string][]string, len(agent)+len(provider))
	for slot, pool := range provider {
		out[slot] = pool
	}
	for slot, pool := range agent {
		if len(pool) > 0 {
			out[slot] = pool
		}
	}
	return out
}

// mergeIntentConfig merges the three keyword lists per-list. Nil in both →
// nil out (avoids allocating an empty struct when neither side configured
// keyword overrides).
func mergeIntentConfig(agent, provider *msgintent.KeywordOverrides) *msgintent.KeywordOverrides {
	if agent == nil && provider == nil {
		return nil
	}
	out := &msgintent.KeywordOverrides{}
	if provider != nil {
		out.CommissionKeywords = provider.CommissionKeywords
		out.BroadcastKeywords = provider.BroadcastKeywords
		out.QuestionKeywords = provider.QuestionKeywords
	}
	if agent != nil {
		if len(agent.CommissionKeywords) > 0 {
			out.CommissionKeywords = agent.CommissionKeywords
		}
		if len(agent.BroadcastKeywords) > 0 {
			out.BroadcastKeywords = agent.BroadcastKeywords
		}
		if len(agent.QuestionKeywords) > 0 {
			out.QuestionKeywords = agent.QuestionKeywords
		}
	}
	return out
}

// renderSlot renders an operator override for slot, or fallback() when no
// override is configured. An override entry that renders to "" is returned
// as-is — the caller decides whether empty means "drop the line" (rich-block
// lines) or "use the default anyway" (opener/decline/degraded, via
// renderSlotRequired). Every slot gets the {help} var on top of its own vars.
func (s OfflineSettings) renderSlot(slot string, vars map[string]string, seed uint64, fallback func() string) string {
	pool := s.Templates[slot]
	if len(pool) == 0 {
		return fallback()
	}
	return msgintent.RenderRaw(pool[seed%uint64(len(pool))], s.withHelpVar(vars))
}

// withHelpVar merges the {help} usage hint into slot vars — available in
// every template override so decline/degraded replies can guide the user.
func (s OfflineSettings) withHelpVar(vars map[string]string) map[string]string {
	merged := make(map[string]string, len(vars)+1)
	for k, v := range vars {
		merged[k] = v
	}
	merged["help"] = s.helpText()
	return merged
}

// helpText resolves the {help} var: operator-supplied help_text, or the
// built-in locale-aware usage hint.
func (s OfflineSettings) helpText() string {
	if s.HelpText != "" {
		return s.HelpText
	}
	return i18n.T(i18n.Normalize(s.Locale), i18n.MsgIntentHelp)
}

// renderSlotRequired is renderSlot for slots that must never be empty —
// a blank override falls back to the built-in template instead of silence.
func (s OfflineSettings) renderSlotRequired(slot string, vars map[string]string, seed uint64, fallback func() string) string {
	if out := s.renderSlot(slot, vars, seed, fallback); out != "" {
		return out
	}
	return fallback()
}

// withPrefix prepends ReplyPrefix to real replies. NO_REPLY (group-chatter
// suppression sentinel) and tool-call turns (empty content) pass unchanged.
func (s OfflineSettings) withPrefix(content string) string {
	if s.ReplyPrefix == "" || content == "" || content == "NO_REPLY" {
		return content
	}
	return s.ReplyPrefix + " " + content
}
