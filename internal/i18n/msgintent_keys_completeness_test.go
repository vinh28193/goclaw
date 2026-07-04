// Catalog parity for deterministic message-intent reply templates.
//
// A msgintent.* key missing from any catalog would leak the raw key string
// into a customer-facing chat reply — worse than a normal error message,
// since these templates ARE the reply. Enumerates every MsgIntent* constant
// and asserts each locale renders a non-key, non-empty message.
package i18n

import (
	"strings"
	"testing"
)

// msgIntentKeys lists every deterministic reply template key. Hand-curated
// (same convention as gitKeys): adding a key to keys.go is a one-line change
// here too.
var msgIntentKeys = []string{
	MsgIntentShortlinkHumble1,
	MsgIntentShortlinkHumble2,
	MsgIntentShortlinkHumble3,
	MsgIntentShortlinkCasual1,
	MsgIntentShortlinkCasual2,
	MsgIntentShortlinkCasual3,
	MsgIntentShortlinkBusiness1,
	MsgIntentShortlinkBusiness2,
	MsgIntentShortlinkBusiness3,
	MsgIntentShortlinkMinimal1,
	MsgIntentCommissionHumble1,
	MsgIntentCommissionHumble2,
	MsgIntentCommissionHumble3,
	MsgIntentCommissionCasual1,
	MsgIntentCommissionCasual2,
	MsgIntentCommissionBusiness1,
	MsgIntentCommissionMinimal1,
	MsgIntentDecline1,
	MsgIntentDecline2,
	MsgIntentDecline3,
	MsgIntentDegraded1,
	MsgIntentRichProduct,
	MsgIntentRichRate,
	MsgIntentRichRateMissing,
}

func TestI18nCatalogs_HasMsgIntentKeys(t *testing.T) {
	for _, locale := range []string{LocaleEN, LocaleVI, LocaleZH} {
		for _, key := range msgIntentKeys {
			msg := lookup(locale, key)
			if msg == key {
				t.Errorf("locale=%s key=%s falls back to key string (translation missing)", locale, key)
				continue
			}
			if strings.TrimSpace(msg) == "" {
				t.Errorf("locale=%s key=%s has empty translation", locale, key)
			}
		}
	}
}

// TestI18nCatalogs_MsgIntentPlaceholdersConsistent guards against a locale
// dropping a {url}/{rate} placeholder during translation — the reply would
// silently lose its payload.
func TestI18nCatalogs_MsgIntentPlaceholdersConsistent(t *testing.T) {
	for _, key := range msgIntentKeys {
		ref := lookup(LocaleEN, key)
		for _, ph := range []string{"{url}", "{rate}"} {
			want := strings.Contains(ref, ph)
			for _, locale := range []string{LocaleVI, LocaleZH} {
				got := strings.Contains(lookup(locale, key), ph)
				if got != want {
					t.Errorf("key=%s locale=%s placeholder %s presence mismatch vs en", key, locale, ph)
				}
			}
		}
	}
}
