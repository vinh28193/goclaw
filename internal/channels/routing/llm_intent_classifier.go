package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// LLMIntentClassifier implements IntentClassifier by asking a cheap LLM to
// pick the best-matching label from a fixed list. The intent labels + prompt
// template are channel-config — operator decides at route-design time.
//
// Cost discipline: caller wraps this in CachedIntentClassifier (60s TTL) and
// the resolver gates by anyRouteHasIntent + non-empty messageText.
type LLMIntentClassifier struct {
	provider providers.Provider
	model    string
	// Labels is the closed-set of allowed intent strings — classifier output
	// outside this set is normalized to "" (unknown).
	Labels []string
	// PromptTemplate is the system prompt; the inbound message is appended as
	// the user message. Use {labels} placeholder to inject the labels list.
	PromptTemplate string
	// MaxTokens caps response length — labels are short; 32 tokens enough.
	MaxTokens int
}

// NewLLMIntentClassifier builds a classifier backed by `provider` with the
// given closed-set labels. Empty labels list disables classification.
func NewLLMIntentClassifier(p providers.Provider, model string, labels []string, promptTemplate string) *LLMIntentClassifier {
	if promptTemplate == "" {
		promptTemplate = DefaultIntentPromptTemplate
	}
	return &LLMIntentClassifier{
		provider:       p,
		model:          model,
		Labels:         labels,
		PromptTemplate: promptTemplate,
		MaxTokens:      32,
	}
}

// DefaultIntentPromptTemplate is the system prompt used when operator doesn't
// supply a custom one. {labels} is replaced with the comma-separated label list.
const DefaultIntentPromptTemplate = `You are a customer-message classifier.

Classify the user's message into EXACTLY ONE of these intent labels:
{labels}

Reply with ONLY the label (no quotes, no explanation, no punctuation).
If no label is a good fit, reply with: unknown`

func (c *LLMIntentClassifier) Classify(ctx context.Context, _ string, message string) (string, error) {
	if c == nil || c.provider == nil {
		return "", errors.New("llm intent classifier not configured")
	}
	if len(c.Labels) == 0 || strings.TrimSpace(message) == "" {
		return "", nil
	}

	prompt := strings.ReplaceAll(c.PromptTemplate, "{labels}", strings.Join(c.Labels, ", "))
	req := providers.ChatRequest{
		Messages: []providers.Message{
			{Role: "system", Content: prompt},
			{Role: "user", Content: message},
		},
		Model: c.model,
		Options: map[string]any{
			"max_tokens":  c.MaxTokens,
			"temperature": 0.0, // deterministic — same message → same label
		},
	}
	resp, err := c.provider.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("classifier llm call: %w", err)
	}
	if resp == nil {
		return "", errors.New("classifier returned nil response")
	}

	label := normalizeIntentLabel(resp.Content)
	// Reject labels outside the configured set so a hallucinated label can't
	// short-circuit routing — fallback to null-intent route via "" (unknown).
	for _, allowed := range c.Labels {
		if strings.EqualFold(label, allowed) {
			return allowed, nil
		}
	}
	return "", nil
}

// normalizeIntentLabel strips whitespace, quotes, and trailing punctuation
// from the model's response so noisy outputs ("billing.", "  Billing  ",
// "\"billing\"") still match the configured label set.
func normalizeIntentLabel(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, `"'.,!?:;`)
	s = strings.TrimSpace(s)
	// Take only the first word — model sometimes emits "billing — refund request"
	if idx := strings.IndexAny(s, " \n\t"); idx > 0 {
		s = s[:idx]
	}
	return s
}
