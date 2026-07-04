package msgintent

// Default keyword lists (vi + en merged — messages arrive in mixed locales,
// scanning both costs nothing). Tenants extend via KeywordOverrides (phase 06).
// All entries MUST be lowercase — matching lowercases the input once.

// defaultCommissionKeywords: platform URL + any of these → commission_lookup.
var defaultCommissionKeywords = []string{
	// vi
	"hoa hồng", "chiết khấu", "bao nhiêu %", "được bao nhiêu",
	// en
	"commission", "rate", "%",
}

// defaultBroadcastKeywords: platform URL + any of these → commission_broadcast
// (admin bulk-report request; v1 falls through to agent pipeline).
var defaultBroadcastKeywords = []string{
	"gửi bảng kê", "gửi thống kê", "gửi báo cáo",
	"send report", "send summary",
}

// defaultQuestionKeywords: interrogative markers. The `?`/`？` regex alone
// catches ~60% of Vietnamese questions — these keywords close the gap.
var defaultQuestionKeywords = []string{
	// vi interrogatives
	"làm sao", "thế nào", "như nào", "bao nhiêu", "tại sao", "vì sao",
	"có cách nào", "được không", "đúng không", "phải không", "là gì",
	"ở đâu", "khi nào", "cái nào", "giúp", "hỏi", "cho mình hỏi", "cho em hỏi",
	// en interrogatives (word-boundary matched)
	"how", "what", "why", "when", "where", "which", "who",
	"can you", "could you", "help", "please",
}

// resolveKeywords merges overrides on top of defaults. Override slices EXTEND
// defaults rather than replace them — tenants add domain jargon without
// re-listing the built-ins. Nil/empty override = defaults untouched.
func resolveKeywords(defaults []string, extra []string) []string {
	if len(extra) == 0 {
		return defaults
	}
	merged := make([]string, 0, len(defaults)+len(extra))
	merged = append(merged, defaults...)
	merged = append(merged, extra...)
	return merged
}
