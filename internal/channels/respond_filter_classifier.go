package channels

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// classifyTimeout is the per-Stage-2 call deadline.
const classifyTimeout = 15 * time.Second

// maxClassifierInputChars caps the content length sent to the classifier LLM.
// Prevents runaway token spend on very long user messages (50KB+ chat).
// First 2000 chars carry enough signal for relevance classification.
const maxClassifierInputChars = 2000

// defaultClassifierPrompt is used when RespondFilter.ClassifierPrompt is empty.
const defaultClassifierPrompt = `You are a relevance gate for an AI sales assistant.
Reply with EXACTLY one word: RELEVANT if the user message is asking about affiliate products, referral links, commissions, orders, or any topic the assistant should respond to. Otherwise reply IGNORE.
User message: `

// stage2 calls the classifier LLM and parses its response.
// Fail-open on nil provider or RPC error → always returns Wake in those cases.
func (f *RespondFilter) stage2(ctx context.Context, content string, provider providers.Provider, model string) FilterDecision {
	if provider == nil {
		slog.Warn("filter.stage2.fail_open", "reason", "nil_provider")
		return DecisionWake
	}

	classifierModel := f.ClassifierModel
	if classifierModel == "" {
		classifierModel = model
	}
	if classifierModel == "" {
		classifierModel = provider.DefaultModel()
	}

	prompt := f.ClassifierPrompt
	if prompt == "" {
		prompt = defaultClassifierPrompt
	}

	// Truncate content to avoid excessive token spend on very long messages.
	trimmed := content
	if len(trimmed) > maxClassifierInputChars {
		trimmed = trimmed[:maxClassifierInputChars]
	}

	ctx, cancel := context.WithTimeout(ctx, classifyTimeout)
	defer cancel()

	resp, err := provider.Chat(ctx, providers.ChatRequest{
		Messages: []providers.Message{
			{Role: "user", Content: prompt + trimmed},
		},
		Model: classifierModel,
		Options: map[string]any{
			providers.OptMaxTokens:   10,
			providers.OptTemperature: 0.0,
		},
	})
	if err != nil {
		slog.Warn("filter.stage2.fail_open", "reason", "rpc_error", "error", err)
		return DecisionWake
	}

	result := strings.TrimSpace(strings.ToUpper(resp.Content))
	switch {
	case strings.Contains(result, "RELEVANT"):
		slog.Debug("filter.stage2.wake", "result", resp.Content)
		return DecisionWake
	case strings.Contains(result, "IGNORE"):
		slog.Debug("filter.stage2.drop", "result", resp.Content)
		return DecisionDrop
	default:
		// Unexpected output → on_no_match (default drop).
		slog.Warn("filter.stage2.unexpected_output", "output", resp.Content)
		return f.onNoMatchDecision()
	}
}
