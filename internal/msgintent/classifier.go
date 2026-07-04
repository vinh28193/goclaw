package msgintent

import "strings"

// Classify runs the deterministic priority matrix. Pure function — no I/O,
// no allocation beyond keyword-match bookkeeping, <1ms.
//
// Priority order (first match wins):
//  1. platform URL + broadcast keyword  → commission_broadcast
//  2. platform URL + commission keyword → commission_lookup
//  3. platform URL (any context — interrupting chatter is the product)
//     → shortlink_offer
//  4. question addressed to bot, no business match → off_scope_question
//  5. question NOT addressed to bot → chatter (not the bot's business)
//  6. no URL, no question, not addressed → chatter
//  7. addressed to bot but nothing specific → general (full agent pipeline)
func Classify(signals MessageSignals, overrides *KeywordOverrides) IntentDecision {
	var o KeywordOverrides
	if overrides != nil {
		o = *overrides
	}
	commissionKW := resolveKeywords(defaultCommissionKeywords, o.CommissionKeywords)
	broadcastKW := resolveKeywords(defaultBroadcastKeywords, o.BroadcastKeywords)
	questionKW := resolveKeywords(defaultQuestionKeywords, o.QuestionKeywords)

	addressed := signals.AddressedToBot()
	// Strip URLs before question detection — `?` in a query string is not a question.
	textNoURLs := urlPattern.ReplaceAllString(signals.Text, " ")
	isQuestion, questionMatches := DetectQuestion(textNoURLs, questionKW)

	decision := IntentDecision{
		IsQuestion:     isQuestion,
		AddressedToBot: addressed,
	}

	if matchedURL, platform := DetectPlatformURL(signals.Text, signals.URLs); matchedURL != "" {
		decision.MatchedURL = matchedURL
		decision.Platform = platform
		decision.Confidence = 1.0
		lower := strings.ToLower(signals.Text)
		if kws := matchAny(lower, broadcastKW); len(kws) > 0 {
			decision.Intent = IntentCommissionBroadcast
			decision.MatchedKeywords = kws
			return decision
		}
		if kws := matchAny(lower, commissionKW); len(kws) > 0 {
			decision.Intent = IntentCommissionLookup
			decision.MatchedKeywords = kws
			return decision
		}
		decision.Intent = IntentShortlinkOffer
		return decision
	}

	switch {
	case isQuestion && addressed:
		decision.Intent = IntentOffScopeQuestion
		decision.Confidence = 0.8
		decision.MatchedKeywords = questionMatches
	case isQuestion: // question between other people — not the bot's business
		decision.Intent = IntentChatter
		decision.Confidence = 0.8
	case !addressed: // no URL, no question, no mention
		decision.Intent = IntentChatter
		decision.Confidence = 0.9
	default: // addressed (DM/mention/reply) but nothing specific
		decision.Intent = IntentGeneral
		decision.Confidence = 0.5
	}
	return decision
}

// matchAny returns all keywords found in lowerText (word-boundary aware for
// short single words, containment for phrases).
func matchAny(lowerText string, keywords []string) []string {
	var matched []string
	for _, kw := range keywords {
		if kw != "" && containsKeyword(lowerText, kw) {
			matched = append(matched, kw)
		}
	}
	return matched
}
