package pricing

import (
	"errors"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

//go:fix inline
func strp(s string) *string { return new(s) }

func TestCostMicrosTokenAndFlatDimensions(t *testing.T) {
	got, err := CostMicros(store.UsagePricingFields{
		Input:      new("0.000001"),
		Output:     new("0.000002"),
		CacheRead:  new("0.0000001"),
		CacheWrite: new("0.0000002"),
		Request:    new("0.01"),
		Image:      new("0.02"),
		WebSearch:  new("0.03"),
	}, BillableUsage{
		InputTokens:      1000,
		OutputTokens:     500,
		CacheReadTokens:  100,
		CacheWriteTokens: 50,
		RequestCount:     1,
		ImageCount:       2,
		WebSearchCount:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := int64(1000 + 1000 + 10 + 10 + 10_000 + 40_000 + 30_000)
	if got != want {
		t.Fatalf("cost micros = %d, want %d", got, want)
	}
}

func TestCostMicrosMissingRequiredPrice(t *testing.T) {
	_, err := CostMicros(store.UsagePricingFields{Input: new("0.000001")}, BillableUsage{
		InputTokens:  1,
		OutputTokens: 1,
	})
	if !errors.Is(err, ErrUnknownPricing) {
		t.Fatalf("err = %v, want ErrUnknownPricing", err)
	}
}

func TestFromProviderUsageSeparatesIncludedCacheTokens(t *testing.T) {
	got := FromProviderUsage(&providers.Usage{
		PromptTokens:                      2006,
		CompletionTokens:                  100,
		CacheReadTokens:                   1920,
		PromptTokensIncludeCachedSegments: true,
	})
	if got.InputTokens != 86 {
		t.Fatalf("InputTokens = %d, want 86", got.InputTokens)
	}
	if got.CacheReadTokens != 1920 {
		t.Fatalf("CacheReadTokens = %d, want 1920", got.CacheReadTokens)
	}
	if got.TotalTokens() != 2106 {
		t.Fatalf("TotalTokens = %d, want 2106", got.TotalTokens())
	}
}
