// Package msgintent implements a deterministic (regex+keyword, zero LLM cost)
// message intent classifier that runs BEFORE the agent pipeline. It routes
// core business ops (shortlink, commission lookup) to direct MCP tool calls
// so they never depend on LLM availability.
//
// This is distinct from internal/channels/routing's LLM route-level intent
// classifier: msgintent is message-level, pure-function, and channel-agnostic.
package msgintent

// Intent is the deterministic classification of an inbound message.
type Intent string

const (
	// IntentShortlinkOffer — message contains a platform product URL; offer a shortlink.
	IntentShortlinkOffer Intent = "shortlink_offer"
	// IntentCommissionLookup — platform URL + commission keyword; look up rate.
	IntentCommissionLookup Intent = "commission_lookup"
	// IntentCommissionBroadcast — platform URL + admin broadcast keyword.
	// v1: NOT dispatched deterministically (needs confirm flow) — classified for logging only.
	IntentCommissionBroadcast Intent = "commission_broadcast"
	// IntentOffScopeQuestion — question addressed to the bot that matches no business intent.
	IntentOffScopeQuestion Intent = "off_scope_question"
	// IntentChatter — conversation between other people; bot must stay silent.
	IntentChatter Intent = "chatter"
	// IntentGeneral — addressed to bot but no specific match; falls through to full agent pipeline.
	IntentGeneral Intent = "general"
)

// MessageSignals is the channel-agnostic input to the classifier.
// Channels build this from their own message context types.
type MessageSignals struct {
	Text         string
	URLs         []string // optional pre-extracted URLs; classifier also scans Text
	PeerKind     string   // "direct" | "group"
	WasMentioned bool
	IsReplyToBot bool
	MediaKind    string // "", "photo", "video", ...
}

// AddressedToBot reports whether the message is directed at the bot
// (DM, explicit @mention, or a reply to a bot message).
func (s MessageSignals) AddressedToBot() bool {
	return s.PeerKind == "direct" || s.WasMentioned || s.IsReplyToBot
}

// IntentDecision is the classifier output.
type IntentDecision struct {
	Intent          Intent
	MatchedURL      string // first platform URL matched (shortlink/commission intents)
	Platform        string // "shopee" | "lazada" | "tiktok"
	Confidence      float64
	MatchedKeywords []string
	IsQuestion      bool // propagated to RunState for the NO_REPLY finalize guard
	AddressedToBot  bool
}

// KeywordOverrides lets tenants extend/replace default keyword lists via
// channel_instances.intent_config (phase 06). Nil slices mean "use defaults".
type KeywordOverrides struct {
	CommissionKeywords []string `json:"commission_keywords,omitempty"`
	BroadcastKeywords  []string `json:"broadcast_keywords,omitempty"`
	QuestionKeywords   []string `json:"question_keywords,omitempty"`
}
