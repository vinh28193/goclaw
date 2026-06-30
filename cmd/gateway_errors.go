package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
)

// Matching TS pi-embedded-helpers/errors.ts error classification.
// Never expose raw JSON/API payloads to the user.
//
// Returns (userMessage, classified). When classified=true the message has been
// matched to a known failure class and is safe to forward to public-facing
// channels (Telegram, Facebook, ...). When classified=false the message is a
// generic fallback and may correlate with raw error text in logs only —
// callers should suppress it on external channels to avoid leaking internals.
func formatAgentError(err error) (string, bool) {
	raw := err.Error()
	lower := strings.ToLower(raw)

	// 1. Timeout — must be checked BEFORE context overflow because
	// "context deadline exceeded" contains both "context" and "exceeded",
	// which would false-positive match the context overflow heuristic.
	if containsAny(lower, "timeout", "timed out", "deadline exceeded") {
		return "⏱️ Em hơi chậm xíu nha, anh thử lại sau ít giây giúp em 🙏", true
	}

	// 2. Context overflow
	if isContextOverflowError(lower) {
		return "📚 Hội thoại dài quá rồi, anh dùng /new để bắt đầu lại nha", true
	}

	// 3. Role ordering / message format errors (tool_use_id mismatch, roles must alternate, etc.)
	if isMessageFormatError(lower) {
		return "🔄 Lịch sử hội thoại bị lẫn — anh thử lại 1 lần nữa nhé. Nếu vẫn vậy dùng /new để reset", true
	}

	// 4. Rate limit
	if containsAny(lower, "rate limit", "rate_limit", "too many requests", "429", "quota exceeded", "resource_exhausted", "usage limit") {
		return "⏳ Em đang xử lý nhiều quá anh ơi, đợi 1 phút rồi gửi lại giúp em 🙏", true
	}

	// 5. Overloaded / Unavailable (HTTP 503, Gemini "high demand", Anthropic overloaded, etc.)
	if containsAny(lower,
		"overloaded",
		"high demand",
		"service unavailable",
		"temporarily unavailable",
		"unavailable",
		" 503",
		"http 503",
		"resource_unavailable",
	) {
		return "🛠️ Hệ thống AI đang quá tải, anh thử lại sau ít giây nha 🙏", true
	}

	// 6. Billing
	if containsAny(lower, "billing", "insufficient credits", "credit balance", "payment required", "402") {
		return "💳 API billing có vấn đề — admin kiểm tra credit của API key giúp em nha", true
	}

	// 7. Auth errors
	if containsAny(lower, "invalid api key", "invalid_api_key", "unauthorized", "forbidden", "authentication", "401", "403", "access denied") {
		return "🔐 Lỗi xác thực API — admin kiểm tra cấu hình API key giúp em", true
	}

	// 8. Model config
	if strings.Contains(lower, "not a valid model") {
		return "⚙️ Lỗi cấu hình model — admin kiểm tra config rồi restart giúp em", true
	}

	// 9. Generic — log the full error but show only a safe message to user.
	// Returned classified=false so external channels suppress to avoid leaking raw error context.
	slog.Warn("unclassified agent error", "error", raw)
	return "😅 Có chút trục trặc anh ơi, anh thử lại nha 🙏", false
}

// isContextOverflowError checks for context window/size overflow patterns.
func isContextOverflowError(lower string) bool {
	return containsAny(lower,
		"request_too_large",
		"context length exceeded",
		"maximum context length",
		"prompt is too long",
		"exceeds model context window",
		"request exceeds the maximum size",
		// Issue 958: Additional patterns (sync with providers/error_classify.go)
		"prompt exceeds max length", // ZAI/GLM-5
		"input is too long",         // DashScope
		"token limit",
		"too many tokens",
		"请求输入过长",       // Chinese generic
		"超出最大长度限制",     // Chinese Qwen
		"上下文长度",        // Chinese context length
	) || (strings.Contains(lower, "context") &&
		containsAny(lower, "overflow", "too large", "too long", "limit", "exceeded"))
}

// isExternalChannel reports whether a channel type serves end users on a
// public-facing platform (Facebook, Telegram, etc.). Internal error details
// must not be forwarded to these channels — the caller publishes an empty
// outbound instead so placeholders get cleaned up without leaking technical
// error text to end users. Internal types ("ws", "") return false.
func isExternalChannel(channelType string) bool {
	switch channelType {
	case channels.TypeFacebook,
		channels.TypeTelegram,
		channels.TypeDiscord,
		channels.TypeFeishu,
		channels.TypeWhatsApp,
		channels.TypeZaloOA,
		channels.TypeZaloPersonal,
		channels.TypePancake,
		channels.TypeSlack:
		return true
	}
	return false
}

// isMessageFormatError checks for tool_use/tool_result mismatch, role ordering,
// and other message format errors that indicate corrupted session history.
func isMessageFormatError(lower string) bool {
	return containsAny(lower,
		"tool_use_id",
		"tool_use.id",
		"unexpected tool",
		"roles must alternate",
		"incorrect role information",
		"invalid request format",
		"tool_result block",
		"tool_use block",
	)
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// formatQuotaExceeded formats a user-friendly quota exceeded message.
func formatQuotaExceeded(result channels.QuotaResult) string {
	labels := map[string]string{"hour": "Hourly", "day": "Daily", "week": "Weekly"}
	return fmt.Sprintf("⚠️ %s request limit reached (%d/%d). Please try again later.",
		labels[result.Window], result.Used, result.Limit)
}
