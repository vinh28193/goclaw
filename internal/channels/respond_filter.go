package channels

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// FilterDecision is the outcome of a respond filter evaluation.
type FilterDecision int

const (
	// DecisionWake means the message should wake the agent (proceed normally).
	DecisionWake FilterDecision = iota
	// DecisionDrop means the message should be silently discarded.
	DecisionDrop
	// DecisionAmbiguous is an internal Stage 1 signal meaning Stage 2 is needed.
	// Only returned by Stage1; Evaluate always returns Wake or Drop.
	DecisionAmbiguous
)

// filterMode values for RespondFilter.Mode.
const (
	filterModeOff        = "off"
	filterModeRegex      = "regex"
	filterModeClassifier = "classifier"
	filterModeHybrid     = "hybrid"
)

// filterScope values for RespondFilter.ApplyScope.
const (
	filterScopeBoth   = "both"
	filterScopeDirect = "direct"
	filterScopeGroup  = "group"
)

// trivialMaxChars: messages shorter than this rune count (after trim) with no URLs/keywords
// are considered trivial (greeting, sticker, single emoji) → no strong signal.
const trivialMaxChars = 4

// RespondFilter is the parsed per-channel-instance message gate configuration.
// Stored in channel_instances.Config["respond_filter"] JSONB — no DB migration needed.
type RespondFilter struct {
	Mode              string   `json:"mode"`               // off | regex | classifier | hybrid (default off)
	URLDomains        []string `json:"url_domains"`        // substring domain match triggers WAKE
	Keywords          []string `json:"keywords"`           // case-insensitive substring triggers WAKE
	ClassifierModel   string   `json:"classifier_model"`   // empty = agent's own model
	ClassifierPrompt  string   `json:"classifier_prompt"`  // empty = defaultClassifierPrompt
	// OnNoMatch controls what happens when Stage 1 finds no match or Stage 2 returns unexpected output.
	// Default "ignore" = drop. Set to "wake" to fail-open (useful in classifier mode to avoid losing
	// real messages on LLM hiccups).
	OnNoMatch         string   `json:"on_no_match"`        // ignore = drop (default) | wake
	ApplyScope        string   `json:"apply_scope"`        // both | direct | group (default both)

	// Pre-lowered slices for Stage1 — populated at Parse time to avoid per-message ToLower calls.
	lowerURLDomains []string
	lowerKeywords   []string
}

// rawFilterWrapper is used to extract the respond_filter key from instance Config.
type rawFilterWrapper struct {
	RespondFilter *RespondFilter `json:"respond_filter"`
}

// ParseRespondFilter extracts a RespondFilter from a channel instance Config JSONB blob.
// Returns nil when the key is absent, mode is "off", or mode is unrecognised (catches typos).
func ParseRespondFilter(cfg json.RawMessage) *RespondFilter {
	if len(cfg) == 0 {
		return nil
	}
	var wrap rawFilterWrapper
	if err := json.Unmarshal(cfg, &wrap); err != nil || wrap.RespondFilter == nil {
		return nil
	}
	f := wrap.RespondFilter
	if f.Mode == "" || f.Mode == filterModeOff {
		return nil
	}
	// Validate mode — unknown values treated as off to catch operator typos in raw JSONB edits.
	switch f.Mode {
	case filterModeRegex, filterModeClassifier, filterModeHybrid:
		// valid
	default:
		return nil
	}
	// Defaults
	if f.ApplyScope == "" {
		f.ApplyScope = filterScopeBoth
	}
	// Pre-lower domains and keywords once at parse time to avoid per-message ToLower in Stage1.
	f.lowerURLDomains = make([]string, 0, len(f.URLDomains))
	for _, d := range f.URLDomains {
		if d != "" {
			f.lowerURLDomains = append(f.lowerURLDomains, strings.ToLower(d))
		}
	}
	f.lowerKeywords = make([]string, 0, len(f.Keywords))
	for _, kw := range f.Keywords {
		if kw != "" {
			f.lowerKeywords = append(f.lowerKeywords, strings.ToLower(kw))
		}
	}
	return f
}

// InScope reports whether this filter applies to the given peer kind.
// peerKind values: "direct", "group".
func (f *RespondFilter) InScope(peerKind string) bool {
	switch f.ApplyScope {
	case filterScopeDirect:
		return peerKind == "direct"
	case filterScopeGroup:
		return peerKind == "group"
	default: // "both" or anything unrecognised
		return true
	}
}

// initLowerSlices populates lowerURLDomains and lowerKeywords from the public slices
// when they are nil (i.e. filter was constructed directly rather than via ParseRespondFilter).
// ParseRespondFilter pre-computes these; this is a safety fallback for direct construction.
func (f *RespondFilter) initLowerSlices() {
	if f.lowerURLDomains == nil {
		f.lowerURLDomains = make([]string, 0, len(f.URLDomains))
		for _, d := range f.URLDomains {
			if d != "" {
				f.lowerURLDomains = append(f.lowerURLDomains, strings.ToLower(d))
			}
		}
	}
	if f.lowerKeywords == nil {
		f.lowerKeywords = make([]string, 0, len(f.Keywords))
		for _, kw := range f.Keywords {
			if kw != "" {
				f.lowerKeywords = append(f.lowerKeywords, strings.ToLower(kw))
			}
		}
	}
}

// Stage1 performs regex/keyword scan without any LLM cost.
// Returns DecisionWake, DecisionDrop, or DecisionAmbiguous.
func (f *RespondFilter) Stage1(content string) FilterDecision {
	f.initLowerSlices()

	trimmed := strings.TrimSpace(content)

	// URL domain match or keyword match → WAKE immediately.
	// Pre-lowered slices avoid per-message ToLower in the hot path.
	lower := strings.ToLower(trimmed)
	for _, domain := range f.lowerURLDomains {
		if strings.Contains(lower, domain) {
			slog.Debug("filter.stage1.wake", "reason", "url_domain", "domain", domain)
			return DecisionWake
		}
	}
	for _, kw := range f.lowerKeywords {
		if strings.Contains(lower, kw) {
			slog.Debug("filter.stage1.wake", "reason", "keyword", "keyword", kw)
			return DecisionWake
		}
	}

	// Trivial heuristic: very short / empty message → no strong signal.
	// Return Ambiguous so regex mode applies on_no_match (drop) and
	// hybrid mode escalates to Stage 2 classifier.
	if utf8.RuneCountInString(trimmed) < trivialMaxChars {
		slog.Debug("filter.stage1.trivial", "len", utf8.RuneCountInString(trimmed))
	}

	return DecisionAmbiguous
}

// Evaluate orchestrates Stage 1 and (if needed) Stage 2 per mode.
// Returns DecisionWake or DecisionDrop — never DecisionAmbiguous.
// Fail-open: on any infrastructure error (nil provider, RPC failure) → Wake.
func (f *RespondFilter) Evaluate(ctx context.Context, content string, provider providers.Provider, model string) FilterDecision {
	switch f.Mode {
	case filterModeRegex:
		d := f.Stage1(content)
		if d == DecisionAmbiguous {
			return f.onNoMatchDecision()
		}
		return d

	case filterModeClassifier:
		// Skip Stage 1; go straight to classifier.
		return f.stage2(ctx, content, provider, model)

	case filterModeHybrid:
		d := f.Stage1(content)
		switch d {
		case DecisionWake:
			return DecisionWake
		case DecisionDrop:
			return DecisionDrop
		default: // Ambiguous → Stage 2
			return f.stage2(ctx, content, provider, model)
		}

	default:
		// Unknown mode → fail open (wake).
		slog.Warn("filter.unknown_mode", "mode", f.Mode)
		return DecisionWake
	}
}

// onNoMatchDecision maps the on_no_match config to a FilterDecision.
// Default "ignore" = Drop. Only "wake" overrides to Wake (case-insensitive).
func (f *RespondFilter) onNoMatchDecision() FilterDecision {
	if strings.EqualFold(f.OnNoMatch, "wake") {
		return DecisionWake
	}
	return DecisionDrop
}
