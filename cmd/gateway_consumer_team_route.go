package cmd

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// teamLeadLookup is the narrow subset of store.TeamStore needed to resolve a
// team-target channel route. Declared locally so tests don't need a full
// TeamStore stub.
type teamLeadLookup interface {
	GetTeam(ctx context.Context, teamID uuid.UUID) (*store.TeamData, error)
}

// resolveTeamTarget translates a team-target route into the lead agent that
// actually runs the inbound. Returns (agentID, teamScopeID, ok).
//
// Contract:
//   - If rawAgentID is a valid team UUID and the team loads with a non-nil lead,
//     returns (lead.String(), team.String(), true).
//   - Any failure mode (parse error, team store nil, team not found, lead removed)
//     returns ("", "", false) so the caller can fail-open to the default channel agent.
//
// Lead-coordinated execution model (Path 4): the lead agent receives the
// inbound and delegates to team members via the team_tasks tool. Consumer-side
// dispatch is therefore "load lead + scope ctx to team", not parallel fan-out.
func resolveTeamTarget(
	ctx context.Context,
	teamStore teamLeadLookup,
	rawAgentID, channelName string,
) (agentID, teamScopeID string, ok bool) {
	if teamStore == nil {
		slog.Warn("team-route: TeamStore not wired; cannot resolve team", "channel", channelName)
		return "", "", false
	}
	teamUUID, err := uuid.Parse(rawAgentID)
	if err != nil || teamUUID == uuid.Nil {
		slog.Warn("team-route: agent_id is not a valid team UUID",
			"raw", rawAgentID, "channel", channelName)
		return "", "", false
	}
	team, err := teamStore.GetTeam(ctx, teamUUID)
	if err != nil {
		slog.Warn("team-route: team load failed",
			"team_id", teamUUID, "channel", channelName, "error", err)
		return "", "", false
	}
	if team == nil || team.LeadAgentID == uuid.Nil {
		slog.Warn("team-route: team missing or has no lead",
			"team_id", teamUUID, "channel", channelName)
		return "", "", false
	}
	slog.Info("team-route: resolved to team lead",
		"team_id", teamUUID, "lead_agent_id", team.LeadAgentID, "channel", channelName)
	return team.LeadAgentID.String(), teamUUID.String(), true
}
