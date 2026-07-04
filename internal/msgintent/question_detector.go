package msgintent

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// DetectQuestion reports whether text reads as a question/request, returning
// the keywords that matched (for logging/tuning). Tier 1 only: `?` regex +
// interrogative keyword scan. An LLM tier-2 classifier is a v2 candidate if
// the measured miss rate warrants it.
func DetectQuestion(text string, keywords []string) (isQuestion bool, matched []string) {
	if strings.ContainsRune(text, '?') || strings.ContainsRune(text, '？') {
		matched = append(matched, "?")
		isQuestion = true
	}
	lower := strings.ToLower(text)
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if containsKeyword(lower, kw) {
			matched = append(matched, kw)
			isQuestion = true
		}
	}
	return isQuestion, matched
}

// containsKeyword matches multi-word phrases by containment; single short
// words require word boundaries so "how" doesn't fire inside "showroom".
// Pure-symbol keywords ("%") also use containment — "6%" has no word boundary
// before the symbol but is exactly the signal we want.
func containsKeyword(lowerText, keyword string) bool {
	if strings.ContainsRune(keyword, ' ') || len([]rune(keyword)) > 6 || !containsLetterOrDigit(keyword) {
		return strings.Contains(lowerText, keyword)
	}
	return containsWord(lowerText, keyword)
}

func containsLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// containsWord checks keyword occurs delimited by non-letter/digit runes.
func containsWord(text, word string) bool {
	idx := 0
	for {
		i := strings.Index(text[idx:], word)
		if i < 0 {
			return false
		}
		start := idx + i
		end := start + len(word)
		prev, _ := utf8.DecodeLastRuneInString(text[:start])
		next, _ := utf8.DecodeRuneInString(text[end:])
		beforeOK := start == 0 || isBoundary(prev)
		afterOK := end == len(text) || isBoundary(next)
		if beforeOK && afterOK {
			return true
		}
		idx = end
	}
}

func isBoundary(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}
