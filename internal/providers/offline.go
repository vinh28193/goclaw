// Offline provider — a rule-based "LLM" that costs zero tokens and needs no
// API key. Agents configured with this provider become offline agents: the
// normal pipeline (sessions, group history, tool execution, delivery) runs
// unchanged, but "thinking" is deterministic message-intent routing:
//
//	platform product URL → tool_calls [generate_shortlink, get_commission_for_url]
//	                       → rich reply (opener + 📦 product + 💵 rate)
//	question/request addressed to the bot → polite decline template
//	group chatter → "NO_REPLY" (existing suppression machinery keeps silence)
//
// Intended for operating channels while the real LLM is off, broken, or too
// expensive — configure via llm_providers row with provider_type "offline".
package providers

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/msgintent"
)

// OptWasMentioned carries the channel's mention signal into ChatRequest.Options
// so the offline provider can tell "question to the bot" from group chatter.
const OptWasMentioned = "was_mentioned"

// offlineCallID prefixes tool-call IDs the offline provider emits, letting it
// recognize its own calls when the tool results come back next iteration.
const offlineCallID = "offline_call_"

// OfflineSettings is stored in llm_providers.settings JSONB.
type OfflineSettings struct {
	Tone         string                      `json:"tone"`   // casual|humble|business|minimal (default humble)
	Locale       string                      `json:"locale"` // en|vi|zh (default vi)
	IntentConfig *msgintent.KeywordOverrides `json:"intent_config,omitempty"`
}

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

// OfflineProvider implements Provider without any upstream API.
type OfflineProvider struct {
	name     string
	settings OfflineSettings
}

// NewOfflineProvider creates an offline provider instance for one
// llm_providers row (multiple rows = multiple tone/locale presets).
func NewOfflineProvider(name string, settings OfflineSettings) *OfflineProvider {
	if name == "" {
		name = "offline"
	}
	return &OfflineProvider{name: name, settings: settings}
}

func (p *OfflineProvider) Name() string         { return p.name }
func (p *OfflineProvider) DefaultModel() string { return "offline" }

// ChatStream satisfies the streaming surface by emitting the final content as
// a single chunk — there is nothing to stream from a rule engine.
func (p *OfflineProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(StreamChunk)) (*ChatResponse, error) {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if onChunk != nil && resp.Content != "" {
		onChunk(StreamChunk{Content: resp.Content})
	}
	return resp, nil
}

// Chat is the rule engine. Two phases inferred from message history:
// classify (fresh user message) and compose (our tool results came back).
func (p *OfflineProvider) Chat(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	if results, intentTag, ok := trailingOfflineToolResults(req.Messages); ok {
		return p.compose(req, results, intentTag), nil
	}
	return p.classify(req), nil
}

// classify runs the deterministic intent matrix on the last user message.
func (p *OfflineProvider) classify(req ChatRequest) *ChatResponse {
	text := lastUserContent(req.Messages)
	peerKind, _ := req.Options[OptPeerKind].(string)
	wasMentioned, _ := req.Options[OptWasMentioned].(bool)

	decision := msgintent.Classify(msgintent.MessageSignals{
		Text:         text,
		PeerKind:     peerKind,
		WasMentioned: wasMentioned,
	}, p.settings.IntentConfig)

	seed := p.seed(req, text)
	switch decision.Intent {
	case msgintent.IntentShortlinkOffer, msgintent.IntentCommissionLookup:
		return p.emitToolCalls(req, decision)
	case msgintent.IntentChatter:
		return &ChatResponse{Content: "NO_REPLY", FinishReason: "stop"}
	default: // off_scope_question, general, commission_broadcast
		return &ChatResponse{Content: msgintent.RenderDecline(p.settings.Locale, seed), FinishReason: "stop"}
	}
}

// emitToolCalls asks the agent loop to execute the MCP tools. Tool names are
// discovered from req.Tools by suffix (the MCP bridge prefixes them with the
// server name) — no hardcoded server prefix.
func (p *OfflineProvider) emitToolCalls(req ChatRequest, decision msgintent.IntentDecision) *ChatResponse {
	shortlinkTool := findToolBySuffix(req.Tools, "generate_shortlink")
	if shortlinkTool == "" {
		// Agent has no shortlink tool granted — nothing useful to do offline.
		return &ChatResponse{
			Content:      msgintent.RenderDecline(p.settings.Locale, p.seed(req, decision.MatchedURL)),
			FinishReason: "stop",
		}
	}
	// Encode the classified intent into the call ID so compose() can pick the
	// right template family without re-classifying (history may have grown).
	calls := []ToolCall{{
		ID:        offlineCallID + "shortlink_" + string(decision.Intent),
		Name:      shortlinkTool,
		Arguments: map[string]any{"product_url": decision.MatchedURL},
	}}
	if commissionTool := findToolBySuffix(req.Tools, "get_commission_for_url"); commissionTool != "" {
		args := map[string]any{"product_url": decision.MatchedURL}
		if decision.Platform != "" {
			args["platform"] = decision.Platform
		}
		calls = append(calls, ToolCall{
			ID:        offlineCallID + "commission_" + string(decision.Intent),
			Name:      commissionTool,
			Arguments: args,
		})
	}
	return &ChatResponse{ToolCalls: calls, FinishReason: "tool_calls"}
}

// shortlinkEnvelope / commissionEnvelope mirror the backend MCP tool JSON.
type shortlinkEnvelope struct {
	OK           bool   `json:"ok"`
	ShortlinkURL string `json:"shortlink_url"`
}

type commissionEnvelope struct {
	OK          bool    `json:"ok"`
	Rate        float64 `json:"rate"` // fraction: 0.06 = 6%
	ProductName string  `json:"product_name"`
}

// compose builds the rich reply from returned tool results:
//
//	<tone opener with {url}>
//	📦 {product_name}          (when commission lookup returned a name)
//	💵 Hoa hồng {rate}%        (or the "no info yet" line)
func (p *OfflineProvider) compose(req ChatRequest, results map[string]Message, intentTag string) *ChatResponse {
	locale := p.settings.Locale
	seed := p.seed(req, lastUserContent(req.Messages))

	var sl shortlinkEnvelope
	slMsg, slFound := results["shortlink"]
	if slFound && !slMsg.IsError {
		_ = json.Unmarshal([]byte(extractFirstJSONObject(slMsg.Content)), &sl)
	}
	if !sl.OK || sl.ShortlinkURL == "" {
		// Business request we could not serve — apologize, never go silent.
		return &ChatResponse{Content: msgintent.RenderDegraded(locale, seed), FinishReason: "stop"}
	}

	var com commissionEnvelope
	if comMsg, ok := results["commission"]; ok && !comMsg.IsError {
		_ = json.Unmarshal([]byte(extractFirstJSONObject(comMsg.Content)), &com)
	}

	tone := msgintent.Tone(p.settings.Tone)
	lines := []string{msgintent.Render(msgintent.IntentShortlinkOffer, tone, locale,
		map[string]string{"url": sl.ShortlinkURL}, seed)}
	if com.OK && com.ProductName != "" {
		lines = append(lines, msgintent.RenderKey(i18n.MsgIntentRichProduct, locale,
			map[string]string{"name": com.ProductName}))
	}
	if com.OK && com.Rate > 0 {
		rate := strconv.FormatFloat(com.Rate*100, 'f', -1, 64)
		lines = append(lines, msgintent.RenderKey(i18n.MsgIntentRichRate, locale,
			map[string]string{"rate": rate}))
	} else if intentTag == string(msgintent.IntentCommissionLookup) {
		// Only surface "no commission info" when the user actually asked for it.
		lines = append(lines, msgintent.RenderKey(i18n.MsgIntentRichRateMissing, locale, nil))
	}
	return &ChatResponse{Content: strings.Join(lines, "\n"), FinishReason: "stop"}
}

// seed derives the deterministic template-pick seed for this turn.
func (p *OfflineProvider) seed(req ChatRequest, text string) uint64 {
	sessionKey, _ := req.Options[OptSessionKey].(string)
	return msgintent.SeedFromString(sessionKey + ":" + text)
}

// trailingOfflineToolResults detects the compose phase: the message history
// ends with tool results answering tool calls THIS provider emitted (IDs carry
// the offlineCallID prefix). Returns results keyed by kind ("shortlink" /
// "commission") plus the intent tag encoded in the call ID.
func trailingOfflineToolResults(msgs []Message) (map[string]Message, string, bool) {
	// Collect trailing role=="tool" messages.
	i := len(msgs) - 1
	byCallID := map[string]Message{}
	for ; i >= 0 && msgs[i].Role == "tool"; i-- {
		byCallID[msgs[i].ToolCallID] = msgs[i]
	}
	if len(byCallID) == 0 || i < 0 {
		return nil, "", false
	}
	// The preceding assistant message must hold our tool calls.
	assistant := msgs[i]
	if assistant.Role != "assistant" || len(assistant.ToolCalls) == 0 {
		return nil, "", false
	}
	results := map[string]Message{}
	intentTag := ""
	for _, tc := range assistant.ToolCalls {
		rest, ok := strings.CutPrefix(tc.ID, offlineCallID)
		if !ok {
			return nil, "", false // not our calls (mixed history — classify fresh)
		}
		kind, tag, _ := strings.Cut(rest, "_")
		if msg, ok := byCallID[tc.ID]; ok {
			results[kind] = msg
		}
		if tag != "" {
			intentTag = tag
		}
	}
	if len(results) == 0 {
		return nil, "", false
	}
	return results, intentTag, true
}

// lastUserContent returns the content of the most recent user message.
func lastUserContent(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

// findToolBySuffix locates a registered tool whose name ends with suffix
// (MCP bridge names are "{prefix}__{tool}"; native tools match exactly).
func findToolBySuffix(tools []ToolDefinition, suffix string) string {
	for _, td := range tools {
		if td.Function == nil {
			continue
		}
		name := td.Function.Name
		if name == suffix || strings.HasSuffix(name, "__"+suffix) {
			return name
		}
	}
	return ""
}

// extractFirstJSONObject pulls the first balanced {...} out of tool result
// text (the MCP bridge wraps results in untrusted-content markers).
func extractFirstJSONObject(content string) string {
	start := strings.Index(content, "{")
	if start < 0 {
		return "{}"
	}
	depth := 0
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1]
			}
		}
	}
	return "{}"
}
