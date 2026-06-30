//go:build redis

package routing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// RedisInvalidateChannel is the Redis pub/sub channel name used to broadcast
// per-channel_instance route invalidations across goclaw nodes. Payload is
// the channel_instance UUID as a plain string (no JSON wrapping — the only
// field needed is the ID, and Redis pub/sub is at-most-once anyway).
const RedisInvalidateChannel = "goclaw:route_invalidate"

// redisPublisher implements InvalidatePublisher backed by a Redis client.
type redisPublisher struct {
	client *redis.Client
}

func (p *redisPublisher) Publish(ctx context.Context, channelInstanceID uuid.UUID) error {
	if p == nil || p.client == nil {
		return errors.New("redis publisher not initialized")
	}
	return p.client.Publish(ctx, RedisInvalidateChannel, channelInstanceID.String()).Err()
}

// StartRedisInvalidate wires multi-node route-invalidation fan-out:
//   - sets a publisher on the resolver so REST mutations broadcast to peers,
//   - starts a subscriber goroutine that evicts local cache when a peer
//     publishes an event.
//
// The returned stop func must be called on shutdown to unsubscribe and close
// the subscription. Pass `client` as `any` so callers can pass an untyped
// value (cmd/gateway.go reads it from build-tag-gated initRedisClient).
//
// Returns (stop, error). Error is non-nil only when the client is the wrong
// type or the subscribe call itself fails; absent Redis (nil client) is NOT
// an error — returns a noop stop function so callers can always defer it.
func StartRedisInvalidate(rawClient any, resolver *AgentRouteResolver) (stop func(), err error) {
	noop := func() {}
	if rawClient == nil || resolver == nil {
		return noop, nil
	}
	client, ok := rawClient.(*redis.Client)
	if !ok {
		return noop, fmt.Errorf("StartRedisInvalidate: expected *redis.Client, got %T", rawClient)
	}

	// Publisher side: REST mutation → Invalidate → Publish to Redis.
	resolver.SetInvalidatePublisher(&redisPublisher{client: client})

	// Subscriber side: receive events from peers (and our own publishes), evict
	// local cache. invalidateLocal does NOT re-publish so there's no event loop.
	sub := client.Subscribe(context.Background(), RedisInvalidateChannel)
	ch := sub.Channel() // buffered, lossy under back-pressure — fine, TTL safety net catches up

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		slog.Info("route_invalidate subscriber started", "channel", RedisInvalidateChannel)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					slog.Warn("route_invalidate subscriber channel closed")
					return
				}
				id, err := uuid.Parse(msg.Payload)
				if err != nil {
					slog.Warn("route_invalidate malformed payload", "payload", msg.Payload, "err", err)
					continue
				}
				resolver.InvalidateLocal(id)
			}
		}
	}()

	stop = func() {
		cancel()
		_ = sub.Close()
	}
	return stop, nil
}
