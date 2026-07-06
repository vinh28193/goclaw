package providers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const (
	testShortlinkTool  = "mcp_affiliate_backend__generate_shortlink"
	testCommissionTool = "mcp_affiliate_backend__get_commission_for_url"
)

func offlineTestTools() []ToolDefinition {
	return []ToolDefinition{
		{Type: "function", Function: &ToolFunctionSchema{Name: "some_other_tool"}},
		{Type: "function", Function: &ToolFunctionSchema{Name: testShortlinkTool}},
		{Type: "function", Function: &ToolFunctionSchema{Name: testCommissionTool}},
	}
}

func offlineReq(text, peerKind string, mentioned bool, tools []ToolDefinition) ChatRequest {
	return ChatRequest{
		Messages: []Message{{Role: "user", Content: text}},
		Tools:    tools,
		Options: map[string]any{
			OptPeerKind:     peerKind,
			OptWasMentioned: mentioned,
			OptSessionKey:   "sess-1",
		},
	}
}

func TestOfflineClassifyShortlinkEmitsToolCalls(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	resp, err := p.Chat(context.Background(), offlineReq(
		"mua giúp mình https://shopee.vn/product-abc", "group", false, offlineTestTools()))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("want 2 tool calls (shortlink + commission), got %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Name != testShortlinkTool || resp.ToolCalls[1].Name != testCommissionTool {
		t.Fatalf("tool names wrong: %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments["product_url"] != "https://shopee.vn/product-abc" {
		t.Fatalf("product_url wrong: %+v", resp.ToolCalls[0].Arguments)
	}
	if !strings.HasPrefix(resp.ToolCalls[0].ID, offlineCallID) {
		t.Fatalf("tool call ID must carry offline prefix: %q", resp.ToolCalls[0].ID)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
}

func TestOfflineChatterStaysSilent(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	resp, err := p.Chat(context.Background(), offlineReq(
		"hôm nay đi ăn gì nhỉ hehe", "group", false, offlineTestTools()))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "NO_REPLY" {
		t.Fatalf("group chatter must be NO_REPLY, got %q", resp.Content)
	}
}

func TestOfflineQuestionToBotDeclines(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	// mentioned group question
	resp, _ := p.Chat(context.Background(), offlineReq(
		"bot ơi mai trời có mưa không?", "group", true, offlineTestTools()))
	if resp.Content == "" || resp.Content == "NO_REPLY" || len(resp.ToolCalls) != 0 {
		t.Fatalf("mentioned question must decline, got %+v", resp)
	}
	// DM statement
	resp, _ = p.Chat(context.Background(), offlineReq(
		"tôi muốn đăng ký làm cộng tác viên", "direct", false, offlineTestTools()))
	if resp.Content == "" || resp.Content == "NO_REPLY" {
		t.Fatalf("DM must never be silent, got %+v", resp)
	}
}

func TestOfflineDeclinesWhenShortlinkToolMissing(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	resp, _ := p.Chat(context.Background(), offlineReq(
		"https://shopee.vn/x", "direct", false, nil))
	if len(resp.ToolCalls) != 0 || resp.Content == "" || resp.Content == "NO_REPLY" {
		t.Fatalf("missing tool must decline, got %+v", resp)
	}
}

// composeReq builds the phase-2 history: user msg → assistant tool_calls → tool results.
func composeReq(intentTag, slResult, comResult string) ChatRequest {
	slID := offlineCallID + "shortlink_" + intentTag
	comID := offlineCallID + "commission_" + intentTag
	msgs := []Message{
		{Role: "user", Content: "https://shopee.vn/product-abc"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: slID, Name: testShortlinkTool},
			{ID: comID, Name: testCommissionTool},
		}},
		{Role: "tool", ToolCallID: slID, Content: slResult},
		{Role: "tool", ToolCallID: comID, Content: comResult},
	}
	return ChatRequest{
		Messages: msgs,
		Tools:    offlineTestTools(),
		Options:  map[string]any{OptPeerKind: "direct", OptSessionKey: "sess-1"},
	}
}

func TestOfflineComposeRichReply(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	resp, err := p.Chat(context.Background(), composeReq("shortlink_offer",
		`{"ok":true,"shortlink_url":"https://s.aff/abc"}`,
		`{"ok":true,"rate":0.12,"product_name":"Áo thun ABC"}`))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for _, want := range []string{"https://s.aff/abc", "Áo thun ABC", "12%"} {
		if !strings.Contains(resp.Content, want) {
			t.Fatalf("rich reply missing %q:\n%s", want, resp.Content)
		}
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatal("compose phase must not emit more tool calls")
	}
}

func TestOfflineComposeWrappedEnvelope(t *testing.T) {
	// MCP bridge wraps results in untrusted-content markers — parser must cope.
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	wrapped := "<<<EXTERNAL_UNTRUSTED_CONTENT>>>\nSource: MCP\n---\n" +
		`{"ok":true,"shortlink_url":"https://s.aff/xyz"}` + "\n<<<END>>>"
	resp, _ := p.Chat(context.Background(), composeReq("shortlink_offer", wrapped, `{"ok":false}`))
	if !strings.Contains(resp.Content, "https://s.aff/xyz") {
		t.Fatalf("wrapped envelope not parsed: %q", resp.Content)
	}
}

func TestOfflineComposeBracesInsideProductName(t *testing.T) {
	// Scraped product names can contain literal { or } — brace-counting in
	// extractFirstJSONObject must ignore braces inside JSON strings, or the
	// object is truncated → unmarshal fails → rate line silently drops.
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	resp, err := p.Chat(context.Background(), composeReq("shortlink_offer",
		`{"ok":true,"shortlink_url":"https://s.aff/abc"}`,
		`{"ok":true,"rate":0.1,"product_name":"Áo {SALE} 50% } khủng"}`))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for _, want := range []string{"https://s.aff/abc", "Áo {SALE} 50% } khủng", "10%"} {
		if !strings.Contains(resp.Content, want) {
			t.Fatalf("reply missing %q:\n%s", want, resp.Content)
		}
	}
}

func TestOfflineComposeShortlinkFailedDegrades(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	resp, _ := p.Chat(context.Background(), composeReq("shortlink_offer",
		`{"ok":false,"error":"backend down"}`, `{"ok":false}`))
	if resp.Content == "" || resp.Content == "NO_REPLY" || strings.Contains(resp.Content, "http") {
		t.Fatalf("failed shortlink must produce degraded apology, got %q", resp.Content)
	}
}

func TestOfflineComposeCommissionMissingLine(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	// commission_lookup intent + commission failed → "no info" line present
	resp, _ := p.Chat(context.Background(), composeReq("commission_lookup",
		`{"ok":true,"shortlink_url":"https://s.aff/abc"}`, `{"ok":false}`))
	if !strings.Contains(resp.Content, "💵") {
		t.Fatalf("commission_lookup must mention commission status: %q", resp.Content)
	}
	// shortlink_offer intent + commission failed → no commission line at all
	resp, _ = p.Chat(context.Background(), composeReq("shortlink_offer",
		`{"ok":true,"shortlink_url":"https://s.aff/abc"}`, `{"ok":false}`))
	if strings.Contains(resp.Content, "💵") {
		t.Fatalf("shortlink_offer with failed commission must skip the 💵 line: %q", resp.Content)
	}
}

func TestOfflineReplyPrefix(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(
		json.RawMessage(`{"reply_prefix":"[offline]"}`)))

	// Decline reply gets the prefix.
	resp, _ := p.Chat(context.Background(), offlineReq(
		"mai trời có mưa không?", "direct", false, offlineTestTools()))
	if !strings.HasPrefix(resp.Content, "[offline] ") {
		t.Fatalf("decline must carry prefix, got %q", resp.Content)
	}

	// NO_REPLY sentinel must NOT be prefixed (suppression machinery matches exact).
	resp, _ = p.Chat(context.Background(), offlineReq(
		"hôm nay đi ăn gì nhỉ hehe", "group", false, offlineTestTools()))
	if resp.Content != "NO_REPLY" {
		t.Fatalf("NO_REPLY must pass unprefixed, got %q", resp.Content)
	}

	// Tool-call turn has empty content — must stay empty.
	resp, _ = p.Chat(context.Background(), offlineReq(
		"https://shopee.vn/product-abc", "direct", false, offlineTestTools()))
	if resp.Content != "" || len(resp.ToolCalls) == 0 {
		t.Fatalf("tool-call turn must keep empty content, got %+v", resp)
	}

	// Composed rich reply gets the prefix.
	resp, _ = p.Chat(context.Background(), composeReq("shortlink_offer",
		`{"ok":true,"shortlink_url":"https://s.aff/abc"}`, `{"ok":false}`))
	if !strings.HasPrefix(resp.Content, "[offline] ") {
		t.Fatalf("composed reply must carry prefix, got %q", resp.Content)
	}
}

func TestOfflineTemplateOverrides(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(json.RawMessage(`{
		"templates": {
			"opener":    ["Link nè: {url}"],
			"rate_line": ["Chiết khấu {rate} phần trăm"],
			"decline":   ["Em chỉ xử lý link sản phẩm thôi ạ."]
		}
	}`)))

	// Overridden opener + rate line render with vars substituted.
	resp, _ := p.Chat(context.Background(), composeReq("commission_lookup",
		`{"ok":true,"shortlink_url":"https://s.aff/abc"}`,
		`{"ok":true,"rate":0.12,"product_name":"Áo thun"}`))
	if !strings.Contains(resp.Content, "Link nè: https://s.aff/abc") {
		t.Fatalf("opener override not applied: %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "Chiết khấu 12 phần trăm") {
		t.Fatalf("rate_line override not applied: %q", resp.Content)
	}
	// product_line has no override → built-in 📦 default still renders.
	if !strings.Contains(resp.Content, "Áo thun") {
		t.Fatalf("default product line missing: %q", resp.Content)
	}

	// Overridden decline.
	resp, _ = p.Chat(context.Background(), offlineReq(
		"mai trời có mưa không?", "direct", false, offlineTestTools()))
	if resp.Content != "Em chỉ xử lý link sản phẩm thôi ạ." {
		t.Fatalf("decline override not applied: %q", resp.Content)
	}
}

func TestOfflineHelpVar(t *testing.T) {
	// Custom help_text substitutes into {help}.
	p := NewOfflineProvider("offline", ParseOfflineSettings(json.RawMessage(`{
		"help_text": "Gõ /start để xem hướng dẫn.",
		"templates": {"decline": ["Em không rõ ạ. {help}"]}
	}`)))
	resp, _ := p.Chat(context.Background(), offlineReq(
		"mai trời có mưa không?", "direct", false, offlineTestTools()))
	if resp.Content != "Em không rõ ạ. Gõ /start để xem hướng dẫn." {
		t.Fatalf("custom help_text not substituted: %q", resp.Content)
	}

	// No help_text → {help} falls back to the built-in locale hint (vi default).
	p = NewOfflineProvider("offline", ParseOfflineSettings(json.RawMessage(`{
		"templates": {"decline": ["{help}"]}
	}`)))
	resp, _ = p.Chat(context.Background(), offlineReq(
		"mai trời có mưa không?", "direct", false, offlineTestTools()))
	if !strings.Contains(resp.Content, "Shopee/Lazada/TikTok") || strings.Contains(resp.Content, "{help}") {
		t.Fatalf("default help hint not rendered: %q", resp.Content)
	}

	// {help} works in compose-phase slots too (rate_missing_line).
	p = NewOfflineProvider("offline", ParseOfflineSettings(json.RawMessage(`{
		"help_text": "Hỏi em bằng: hoa hồng <link>",
		"templates": {"rate_missing_line": ["💵 Chưa có info. {help}"]}
	}`)))
	resp, _ = p.Chat(context.Background(), composeReq("commission_lookup",
		`{"ok":true,"shortlink_url":"https://s.aff/abc"}`, `{"ok":false}`))
	if !strings.Contains(resp.Content, "Hỏi em bằng: hoa hồng <link>") {
		t.Fatalf("help var missing in compose slot: %q", resp.Content)
	}
}

func TestOfflineTemplateRemoveLineAndRequiredFallback(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(json.RawMessage(`{
		"templates": {
			"rate_missing_line": [""],
			"opener":            [""]
		}
	}`)))

	// rate_missing_line overridden to "" → line dropped even on commission_lookup.
	resp, _ := p.Chat(context.Background(), composeReq("commission_lookup",
		`{"ok":true,"shortlink_url":"https://s.aff/abc"}`, `{"ok":false}`))
	if strings.Contains(resp.Content, "💵") {
		t.Fatalf("rate_missing_line [\"\"] must drop the 💵 line: %q", resp.Content)
	}
	// opener is REQUIRED — blank override falls back to built-in (never silent).
	if !strings.Contains(resp.Content, "https://s.aff/abc") {
		t.Fatalf("blank opener override must fall back to default: %q", resp.Content)
	}
}

func TestOfflineSettingsParsing(t *testing.T) {
	s := ParseOfflineSettings(nil)
	if s.Tone != "humble" || s.Locale != "vi" {
		t.Fatalf("defaults wrong: %+v", s)
	}
	s = ParseOfflineSettings(json.RawMessage(`{"tone":"business","locale":"en"}`))
	if s.Tone != "business" || s.Locale != "en" {
		t.Fatalf("parse wrong: %+v", s)
	}
	s = ParseOfflineSettings(json.RawMessage(`{"tone":"shouty"}`))
	if s.Tone != "humble" {
		t.Fatalf("invalid tone must fall back to humble: %+v", s)
	}
}

func TestOfflineChatStreamEmitsSingleChunk(t *testing.T) {
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	var chunks []string
	resp, err := p.ChatStream(context.Background(),
		offlineReq("mai trời mưa không?", "direct", false, offlineTestTools()),
		func(c StreamChunk) { chunks = append(chunks, c.Content) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if len(chunks) != 1 || chunks[0] != resp.Content {
		t.Fatalf("want single chunk mirroring content, got %v vs %q", chunks, resp.Content)
	}
}

func TestOfflineMixedHistoryClassifiesFresh(t *testing.T) {
	// Trailing tool results for UNRELATED tools (an LLM agent's web_search etc.)
	// must not trigger compose — neither the offline ID prefix nor a known
	// shortlink/commission tool name matches.
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	req := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "https://shopee.vn/x"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_llm_1", Name: "web_search"}}},
			{Role: "tool", ToolCallID: "call_llm_1", Content: `{"ok":true}`},
		},
		Tools:   offlineTestTools(),
		Options: map[string]any{OptPeerKind: "direct", OptSessionKey: "s"},
	}
	resp, _ := p.Chat(context.Background(), req)
	if len(resp.ToolCalls) == 0 {
		t.Fatalf("unrelated tool history must re-classify (emit own calls), got %+v", resp)
	}
}

func TestOfflineComposesAfterLoopRewritesCallIDs(t *testing.T) {
	// The agent loop rewrites tool-call IDs (uniquifyToolCallIDs) — compose must
	// still fire by matching tool NAMES, and the intent tag is recovered by
	// re-classifying the originating user message.
	p := NewOfflineProvider("offline", ParseOfflineSettings(nil))
	req := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "hoa hồng link này bao nhiêu https://shopee.vn/x-i.1.2"},
			{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "call_abc123rewritten", Name: testShortlinkTool},
				{ID: "call_def456rewritten", Name: testCommissionTool},
			}},
			{Role: "tool", ToolCallID: "call_abc123rewritten", Content: `{"ok":true,"shortlink_url":"https://aff.x/s/abc"}`},
			{Role: "tool", ToolCallID: "call_def456rewritten", Content: `{"ok":false}`, IsError: true},
		},
		Tools:   offlineTestTools(),
		Options: map[string]any{OptPeerKind: "direct", OptSessionKey: "s"},
	}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("must compose, not re-emit tool calls: %+v", resp.ToolCalls)
	}
	if !strings.Contains(resp.Content, "https://aff.x/s/abc") {
		t.Fatalf("reply must contain shortlink, got: %q", resp.Content)
	}
	// commission_lookup intent recovered via re-classification → a 💵 line renders
	if !strings.Contains(resp.Content, "💵") {
		t.Fatalf("commission_lookup must render a rate line (missing-info), got: %q", resp.Content)
	}
}
