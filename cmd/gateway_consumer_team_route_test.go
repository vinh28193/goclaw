package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// fakeTeamLookup implements teamLeadLookup for unit tests.
type fakeTeamLookup struct {
	teams map[uuid.UUID]*store.TeamData
	err   error
	calls int
}

func (f *fakeTeamLookup) GetTeam(_ context.Context, teamID uuid.UUID) (*store.TeamData, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	t, ok := f.teams[teamID]
	if !ok {
		return nil, nil
	}
	return t, nil
}

// Happy path: valid team UUID + team with non-nil lead → returns lead agent ID
// and team scope ID.
func TestResolveTeamTarget_HappyPath(t *testing.T) {
	teamID := uuid.Must(uuid.NewV7())
	leadID := uuid.Must(uuid.NewV7())
	fake := &fakeTeamLookup{teams: map[uuid.UUID]*store.TeamData{
		teamID: {LeadAgentID: leadID},
	}}

	agentID, scopeID, ok := resolveTeamTarget(context.Background(), fake, teamID.String(), "telegram")

	if !ok {
		t.Fatal("expected ok=true for valid team with lead")
	}
	if agentID != leadID.String() {
		t.Errorf("expected agentID=%s, got %s", leadID, agentID)
	}
	if scopeID != teamID.String() {
		t.Errorf("expected scopeID=%s, got %s", teamID, scopeID)
	}
}

// TeamStore nil → fail-open (ok=false), no panic.
func TestResolveTeamTarget_NilStoreFailsOpen(t *testing.T) {
	agentID, scopeID, ok := resolveTeamTarget(context.Background(), nil, uuid.Must(uuid.NewV7()).String(), "telegram")
	if ok || agentID != "" || scopeID != "" {
		t.Errorf("expected fail-open (false, \"\", \"\"); got (%v, %q, %q)", ok, agentID, scopeID)
	}
}

// Garbage agent_id (not a UUID) → fail-open, no GetTeam call.
func TestResolveTeamTarget_InvalidUUIDFailsOpen(t *testing.T) {
	fake := &fakeTeamLookup{teams: map[uuid.UUID]*store.TeamData{}}
	agentID, scopeID, ok := resolveTeamTarget(context.Background(), fake, "not-a-uuid", "telegram")
	if ok || agentID != "" || scopeID != "" {
		t.Errorf("expected fail-open; got (%v, %q, %q)", ok, agentID, scopeID)
	}
	if fake.calls != 0 {
		t.Errorf("expected 0 GetTeam calls on parse error; got %d", fake.calls)
	}
}

// Nil UUID (all zeros) → fail-open, no GetTeam call.
func TestResolveTeamTarget_NilUUIDFailsOpen(t *testing.T) {
	fake := &fakeTeamLookup{teams: map[uuid.UUID]*store.TeamData{}}
	agentID, scopeID, ok := resolveTeamTarget(context.Background(), fake, uuid.Nil.String(), "telegram")
	if ok || agentID != "" || scopeID != "" {
		t.Errorf("expected fail-open on nil UUID; got (%v, %q, %q)", ok, agentID, scopeID)
	}
	if fake.calls != 0 {
		t.Errorf("expected 0 GetTeam calls on nil UUID; got %d", fake.calls)
	}
}

// GetTeam returns store error (DB down, perms, etc) → fail-open.
func TestResolveTeamTarget_StoreErrorFailsOpen(t *testing.T) {
	fake := &fakeTeamLookup{err: errors.New("db unavailable")}
	agentID, scopeID, ok := resolveTeamTarget(context.Background(), fake, uuid.Must(uuid.NewV7()).String(), "telegram")
	if ok || agentID != "" || scopeID != "" {
		t.Errorf("expected fail-open on store error; got (%v, %q, %q)", ok, agentID, scopeID)
	}
}

// Team row exists but row not found in tenant scope (GetTeam returns nil, nil)
// → fail-open. Mirrors real PG behavior: cross-tenant team lookups silently
// return nil to avoid leaking existence.
func TestResolveTeamTarget_TeamNotFoundFailsOpen(t *testing.T) {
	fake := &fakeTeamLookup{teams: map[uuid.UUID]*store.TeamData{}}
	agentID, scopeID, ok := resolveTeamTarget(context.Background(), fake, uuid.Must(uuid.NewV7()).String(), "telegram")
	if ok || agentID != "" || scopeID != "" {
		t.Errorf("expected fail-open on team not found; got (%v, %q, %q)", ok, agentID, scopeID)
	}
}

// Team exists but LeadAgentID is uuid.Nil (lead was removed) → fail-open.
// Prevents publishing inbound with empty agent ID downstream.
func TestResolveTeamTarget_NoLeadFailsOpen(t *testing.T) {
	teamID := uuid.Must(uuid.NewV7())
	fake := &fakeTeamLookup{teams: map[uuid.UUID]*store.TeamData{
		teamID: {LeadAgentID: uuid.Nil},
	}}
	agentID, scopeID, ok := resolveTeamTarget(context.Background(), fake, teamID.String(), "telegram")
	if ok || agentID != "" || scopeID != "" {
		t.Errorf("expected fail-open when lead missing; got (%v, %q, %q)", ok, agentID, scopeID)
	}
}
