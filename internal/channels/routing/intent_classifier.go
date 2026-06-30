package routing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// IntentClassifier returns a short label (e.g. "billing", "support", "sales")
// for an inbound message. Empty string means "uncertain" — caller falls back
// to null-intent rule eval.
//
// Implementations MUST be best-effort: panic-free, ctx-aware, fail-open on
// transient errors. Resolver never blocks routing on a classifier failure.
type IntentClassifier interface {
	Classify(ctx context.Context, channelInstanceID string, message string) (intent string, err error)
}

// IntentCacheTTL keeps classifier results short-lived to dedupe identical
// messages within a burst (retries, broadcasts), without surfacing stale
// labels on real new conversations.
const IntentCacheTTL = 60 * time.Second

// CachedIntentClassifier wraps an inner classifier with a sync.Map cache
// keyed by (channelInstanceID, sha256(message)). On hit, no LLM call.
//
// Cache is per-process (no Redis fan-out); the TTL is short enough that
// multi-node drift is irrelevant — at worst each node makes its own LLM
// call for the same message.
type CachedIntentClassifier struct {
	inner IntentClassifier
	cache sync.Map // key: string -> entry
	ttl   time.Duration
	clock func() time.Time
}

type cachedIntent struct {
	intent    string
	expiresAt time.Time
}

// NewCachedIntentClassifier wraps `inner` with TTL caching. ttl<=0 defaults
// to IntentCacheTTL.
func NewCachedIntentClassifier(inner IntentClassifier, ttl time.Duration) *CachedIntentClassifier {
	if ttl <= 0 {
		ttl = IntentCacheTTL
	}
	return &CachedIntentClassifier{
		inner: inner,
		ttl:   ttl,
		clock: time.Now,
	}
}

func (c *CachedIntentClassifier) Classify(ctx context.Context, channelInstanceID string, message string) (string, error) {
	key := intentCacheKey(channelInstanceID, message)
	now := c.clock()
	if v, ok := c.cache.Load(key); ok {
		entry := v.(cachedIntent)
		if now.Before(entry.expiresAt) {
			return entry.intent, nil
		}
	}
	intent, err := c.inner.Classify(ctx, channelInstanceID, message)
	if err != nil {
		// Don't cache errors — next call may have a working broker.
		return "", err
	}
	c.cache.Store(key, cachedIntent{
		intent:    intent,
		expiresAt: now.Add(c.ttl),
	})
	return intent, nil
}

// intentCacheKey produces a stable string key from (channel, message). Hash
// the message to avoid storing arbitrary user content in the cache map.
func intentCacheKey(channelInstanceID, message string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(message)))
	return channelInstanceID + ":" + hex.EncodeToString(h[:8])
}

// StaticIntentClassifier returns a fixed intent regardless of input. Useful
// for tests + as a no-op default when an operator wants intent matching but
// hasn't wired an LLM yet (e.g. set all messages to a single intent label).
type StaticIntentClassifier struct {
	Intent string
}

func (s *StaticIntentClassifier) Classify(_ context.Context, _ string, _ string) (string, error) {
	return s.Intent, nil
}
