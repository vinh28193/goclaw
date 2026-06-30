//go:build !redis

package routing

// StartRedisInvalidate is a no-op when goclaw is built without the `redis`
// build tag. Callers can always invoke this safely — single-node deployments
// don't need cross-node invalidation, and the resolver's TTL is the safety
// net for any rare drift.
//
// To enable Redis-backed cross-node invalidation, rebuild with `-tags redis`.
func StartRedisInvalidate(_ any, _ *AgentRouteResolver) (stop func(), err error) {
	return func() {}, nil
}
