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
