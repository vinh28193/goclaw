package msgintent

import (
	"net/url"
	"regexp"
	"strings"
)

// Platform URL whitelist — mirrors affiliate-backend app/utils/url_processor.py domains.
// Suffix match on hostname so subdomains (vn.shp.ee-style regional hosts) are NOT
// accidentally matched: each entry must equal the host or be a dot-prefixed suffix.
var platformDomains = map[string]string{
	"shopee.vn":     "shopee",
	"s.shopee.vn":   "shopee",
	"shp.ee":        "shopee",
	"lazada.vn":     "lazada",
	"s.lazada.vn":   "lazada",
	"tiktok.com":    "tiktok",
	"vt.tiktok.com": "tiktok",
}

// urlPattern extracts http(s) URLs from free text. RE2 — no catastrophic backtracking.
var urlPattern = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)

// DetectPlatformURL returns the first platform-whitelisted URL found in the
// pre-extracted urls (checked first) or in the raw text, plus its platform tag.
// It only pattern-matches — it never follows redirects (SSRF stays out of the gateway).
func DetectPlatformURL(text string, urls []string) (matchedURL, platform string) {
	for _, u := range urls {
		if p := matchPlatform(u); p != "" {
			return strings.TrimRight(u, ".,;:!?)"), p
		}
	}
	for _, u := range urlPattern.FindAllString(text, -1) {
		u = strings.TrimRight(u, ".,;:!?)")
		if p := matchPlatform(u); p != "" {
			return u, p
		}
	}
	return "", ""
}

// matchPlatform returns the platform tag when the URL's host is whitelisted.
func matchPlatform(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	for domain, platform := range platformDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return platform
		}
	}
	return ""
}
