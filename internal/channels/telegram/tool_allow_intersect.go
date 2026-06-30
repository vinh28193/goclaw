package telegram

import "github.com/google/uuid"

// uuidNil is the zero UUID; comparing channel InstanceID against this avoids
// importing uuid in every handler site.
var uuidNil = uuid.Nil

// intersectToolAllow returns the intersection of two tool-allow lists when both
// are non-empty. When either side is nil/empty, the other side wins (most
// permissive). This implements the route-vs-topic precedence rule: route
// narrowing AND topic narrowing both apply; the union of restrictions wins.
//
// Returns nil when both inputs are nil/empty (no narrowing → downstream
// FilterTools treats nil as "no restriction").
func intersectToolAllow(routeAllow, topicAllow []string) []string {
	if len(routeAllow) == 0 {
		return topicAllow
	}
	if len(topicAllow) == 0 {
		return routeAllow
	}
	topicSet := make(map[string]bool, len(topicAllow))
	for _, t := range topicAllow {
		topicSet[t] = true
	}
	out := make([]string, 0, len(routeAllow))
	for _, t := range routeAllow {
		if topicSet[t] {
			out = append(out, t)
		}
	}
	return out
}
