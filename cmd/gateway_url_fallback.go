package cmd

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
)

// firstURLPattern extracts the first http(s) URL in arbitrary text.
// Allows query strings + path with common safe chars; stops at whitespace or
// trailing punctuation that's likely not part of the URL.
var firstURLPattern = regexp.MustCompile(`https?://[^\s<>"'\)]+`)

// extractFirstURL returns the first http(s) URL found in text, trimming
// trailing punctuation that's unlikely to be part of the URL.
func extractFirstURL(text string) string {
	m := firstURLPattern.FindString(text)
	if m == "" {
		return ""
	}
	return strings.TrimRight(m, ".,;:!?")
}

// isTransientLLMError reports whether the agent-run error is one of the
// transient classes that warrant offline fallback (vs. permanent code/config
// errors where a backend retry would just fail the same way).
func isTransientLLMError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return containsAny(lower,
		"rate limit", "rate_limit", "too many requests", "429",
		"quota exceeded", "resource_exhausted", "usage limit",
		"overloaded", "high demand", "service unavailable",
		"temporarily unavailable", "unavailable",
		" 503", "http 503", "resource_unavailable",
		"timeout", "timed out", "deadline exceeded",
	)
}

// MCP tool registered names (prefixed by server name in goclaw's bridge).
const (
	mcpToolDetectURL          = "mcp_affiliate_backend__detect_url"
	mcpToolGenerateShortlink  = "mcp_affiliate_backend__generate_shortlink"
	mcpToolGetCommissionForURL = "mcp_affiliate_backend__get_commission_for_url"
)

// callMCPDetectURL invokes the detect_url MCP tool and parses the JSON
// result envelope. Returns nil on any failure (caller falls back).
type detectURLResult struct {
	OK            bool   `json:"ok"`
	Type          string `json:"type"`           // "my_shortlink" | "external"
	ShortlinkID   int64  `json:"shortlink_id"`
	OriginalURL   string `json:"original_url"`
	ResolvedURL   string `json:"resolved_url"`
	Platform      string `json:"platform"`
	IsProduct     bool   `json:"is_product"`
	WasShortened  bool   `json:"was_shortened"`
}

type shortlinkResult struct {
	OK           bool   `json:"ok"`
	ShortlinkURL string `json:"shortlink_url"`
	TrackingID   int64  `json:"tracking_id"`
}

type commissionResult struct {
	OK          bool    `json:"ok"`
	Rate        float64 `json:"rate"`
	ProductName string  `json:"product_name"`
	ShopName    string  `json:"shop_name"`
}

// composeOfflineURLReply orchestrates MCP tools to handle a URL when the LLM
// is unavailable. Brain = goclaw: this Go code calls detect_url → branches by
// result type → calls shortlink + commission as needed → composes the
// Vietnamese reply. Returns empty string if any step fails fatally.
func composeOfflineURLReply(ctx context.Context, loop agent.Agent, url string) string {
	if loop == nil || url == "" {
		return ""
	}

	// Bound the whole fallback flow so a hung MCP server doesn't stall the
	// outbound publish indefinitely.
	flowCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// --- Step 1: detect_url to classify ---
	det := callMCPDetectURL(flowCtx, loop, url)
	if det == nil {
		slog.Warn("offline_url: detect_url returned nil", "url", url)
		return ""
	}
	slog.Info("offline_url: detect_url ok",
		"url", url, "type", det.Type, "platform", det.Platform, "is_product", det.IsProduct)

	// --- Branch by type ---
	if det.Type == "my_shortlink" {
		// User pasted a link goclaw generated. Without LLM we can't reliably
		// infer their intent (clicks / report / commission), so ask.
		return "Link này em đã tạo cho anh rồi nè 😊\n" +
			"Anh cần em làm gì với link này nhỉ — kiểm tra lượt click 📊, " +
			"báo lỗi không vào được 🛠️, hay hỏi hoa hồng 💵?"
	}

	// type == "external" (or anything else we treat as external)
	if det.Platform == "" {
		return "Link không hỗ trợ anh ơi 😅 Em chỉ làm được Shopee, TikTok Shop, Lazada thôi nha"
	}
	if !det.IsProduct {
		return "Link này không phải trang sản phẩm cụ thể anh ơi 🤔 Anh paste link sản phẩm Shopee/TikTok/Lazada nhé"
	}

	// External product URL — parallel call shortlink + commission.
	target := det.ResolvedURL
	if target == "" {
		target = url
	}

	var (
		wg   sync.WaitGroup
		slR  *shortlinkResult
		comR *commissionResult
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		slR = callMCPGenerateShortlink(flowCtx, loop, target)
	}()
	go func() {
		defer wg.Done()
		comR = callMCPCommissionForURL(flowCtx, loop, target, det.Platform)
	}()
	wg.Wait()

	if slR == nil || !slR.OK || slR.ShortlinkURL == "" {
		// Shortlink gen failed — without it, no useful reply.
		return ""
	}

	parts := []string{"Link cho anh đây nha 👇"}
	if comR != nil && comR.OK && comR.ProductName != "" {
		parts = append(parts, "📦 "+comR.ProductName)
	}
	parts = append(parts, "🔗 "+slR.ShortlinkURL)
	if comR != nil && comR.OK && comR.Rate > 0 {
		parts = append(parts, "💵 Hoa hồng "+formatPercent(comR.Rate*100))
	} else {
		parts = append(parts, "💵 Hiện chưa có thông tin hoa hồng")
	}
	return strings.Join(parts, "\n")
}

// formatPercent renders a percent value (e.g. 6.0 → "6%", 5.5 → "5.5%").
func formatPercent(p float64) string {
	if p == float64(int(p)) {
		return strings.TrimRight(strings.TrimRight(formatFloat(p), "0"), ".") + "%"
	}
	return formatFloat(p) + "%"
}

func formatFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func callMCPDetectURL(ctx context.Context, loop agent.Agent, url string) *detectURLResult {
	res, ok := loop.CallTool(ctx, mcpToolDetectURL, map[string]any{"url": url})
	if !ok {
		slog.Warn("offline_url: detect_url tool not registered", "tool", mcpToolDetectURL)
		return nil
	}
	if res == nil {
		slog.Warn("offline_url: detect_url returned nil result")
		return nil
	}
	if res.IsError {
		slog.Warn("offline_url: detect_url tool returned IsError", "content", truncateLogBody(res.ForLLM, 200))
		return nil
	}
	out := &detectURLResult{}
	jsonStr := extractFirstJSON(res.ForLLM)
	if err := json.Unmarshal([]byte(jsonStr), out); err != nil {
		slog.Warn("offline_url: detect_url parse failed",
			"error", err, "json_preview", truncateLogBody(jsonStr, 200))
		return nil
	}
	if !out.OK {
		slog.Warn("offline_url: detect_url returned ok=false")
		return nil
	}
	return out
}

func callMCPGenerateShortlink(ctx context.Context, loop agent.Agent, productURL string) *shortlinkResult {
	res, ok := loop.CallTool(ctx, mcpToolGenerateShortlink, map[string]any{"product_url": productURL})
	if !ok || res == nil || res.IsError {
		return nil
	}
	out := &shortlinkResult{}
	if err := json.Unmarshal([]byte(extractFirstJSON(res.ForLLM)), out); err != nil {
		return nil
	}
	return out
}

func callMCPCommissionForURL(ctx context.Context, loop agent.Agent, productURL, platform string) *commissionResult {
	res, ok := loop.CallTool(ctx, mcpToolGetCommissionForURL, map[string]any{
		"product_url": productURL,
		"platform":    platform,
	})
	if !ok || res == nil || res.IsError {
		return nil
	}
	out := &commissionResult{}
	if err := json.Unmarshal([]byte(extractFirstJSON(res.ForLLM)), out); err != nil {
		return nil
	}
	return out
}

// truncateLogBody clips a string to max n bytes plus an ellipsis indicator.
// Used for safe log output when echoing tool result bodies that may be large.
func truncateLogBody(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// extractFirstJSON pulls the first JSON object out of a tool result Content
// string. MCP bridge wraps results in <<<EXTERNAL_UNTRUSTED_CONTENT>>> markers
// plus surrounding text — find the first `{...}` and return it.
func extractFirstJSON(content string) string {
	start := strings.Index(content, "{")
	if start < 0 {
		return "{}"
	}
	// Find matching close brace, accounting for nesting.
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
