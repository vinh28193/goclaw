package routing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// --- fakes ---

type fakeInstanceStore struct {
	list   []store.ChannelInstanceData
	listFn func(ctx context.Context) ([]store.ChannelInstanceData, error)
}

func (f *fakeInstanceStore) Create(context.Context, *store.ChannelInstanceData) error {
	return errors.New("unused")
}
func (f *fakeInstanceStore) Get(context.Context, uuid.UUID) (*store.ChannelInstanceData, error) {
	return nil, errors.New("unused")
}
func (f *fakeInstanceStore) GetByName(context.Context, string) (*store.ChannelInstanceData, error) {
	return nil, errors.New("unused")
}
func (f *fakeInstanceStore) Update(context.Context, uuid.UUID, map[string]any) error {
	return errors.New("unused")
}
func (f *fakeInstanceStore) Delete(context.Context, uuid.UUID) error { return errors.New("unused") }
func (f *fakeInstanceStore) ListEnabled(context.Context) ([]store.ChannelInstanceData, error) {
	return nil, errors.New("unused")
}
func (f *fakeInstanceStore) ListAll(context.Context) ([]store.ChannelInstanceData, error) {
	return nil, errors.New("unused")
}
func (f *fakeInstanceStore) ListAllInstances(ctx context.Context) ([]store.ChannelInstanceData, error) {
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return f.list, nil
}
func (f *fakeInstanceStore) ListAllEnabled(context.Context) ([]store.ChannelInstanceData, error) {
	return nil, errors.New("unused")
}
func (f *fakeInstanceStore) ListPaged(context.Context, store.ChannelInstanceListOpts) ([]store.ChannelInstanceData, error) {
	return nil, errors.New("unused")
}
func (f *fakeInstanceStore) CountInstances(context.Context, store.ChannelInstanceListOpts) (int, error) {
	return 0, errors.New("unused")
}

type fakeRouteStoreMig struct {
	rows   map[uuid.UUID][]store.ChannelAgentRouteData
	create func(r *store.ChannelAgentRouteData) error
}

func (f *fakeRouteStoreMig) Create(_ context.Context, r *store.ChannelAgentRouteData) error {
	if f.create != nil {
		if err := f.create(r); err != nil {
			return err
		}
	}
	r.ID = uuid.Must(uuid.NewV7())
	if f.rows == nil {
		f.rows = make(map[uuid.UUID][]store.ChannelAgentRouteData)
	}
	f.rows[r.ChannelInstanceID] = append(f.rows[r.ChannelInstanceID], *r)
	return nil
}
func (f *fakeRouteStoreMig) Get(context.Context, uuid.UUID) (*store.ChannelAgentRouteData, error) {
	return nil, errors.New("unused")
}
func (f *fakeRouteStoreMig) Update(context.Context, uuid.UUID, map[string]any) error {
	return errors.New("unused")
}
func (f *fakeRouteStoreMig) Delete(context.Context, uuid.UUID) error { return errors.New("unused") }
func (f *fakeRouteStoreMig) ListByChannelInstance(_ context.Context, id uuid.UUID) ([]store.ChannelAgentRouteData, error) {
	return f.rows[id], nil
}
func (f *fakeRouteStoreMig) ListByTenant(context.Context) ([]store.ChannelAgentRouteData, error) {
	return nil, errors.New("unused")
}

type fakeAgentResolver struct {
	byKey map[string]*store.AgentData
	err   error
}

func (f *fakeAgentResolver) GetByKey(_ context.Context, key string) (*store.AgentData, error) {
	if f.err != nil {
		return nil, f.err
	}
	if a, ok := f.byKey[key]; ok {
		return a, nil
	}
	return nil, errors.New("agent not found")
}

// --- helpers ---

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func newInstance(t *testing.T, name string, tenantID uuid.UUID, voiceAgentKey string) store.ChannelInstanceData {
	t.Helper()
	cfg := map[string]any{}
	if voiceAgentKey != "" {
		cfg["voice_agent_id"] = voiceAgentKey
	}
	return store.ChannelInstanceData{
		BaseModel:   store.BaseModel{ID: uuid.Must(uuid.NewV7())},
		TenantID:    tenantID,
		Name:        name,
		ChannelType: "telegram",
		Config:      mustJSON(t, cfg),
	}
}

// --- tests ---

func TestMigrate_NoInstances_NoCreates(t *testing.T) {
	is := &fakeInstanceStore{}
	rs := &fakeRouteStoreMig{}
	ar := &fakeAgentResolver{}
	scanned, created, err := MigrateVoiceAgentIDs(context.Background(), is, rs, ar)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if scanned != 0 || created != 0 {
		t.Errorf("got scanned=%d created=%d, want 0/0", scanned, created)
	}
}

func TestMigrate_SkipInstanceWithoutVoiceAgentID(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	is := &fakeInstanceStore{list: []store.ChannelInstanceData{
		newInstance(t, "tg-1", tenantID, ""),
	}}
	rs := &fakeRouteStoreMig{}
	ar := &fakeAgentResolver{byKey: map[string]*store.AgentData{}}
	scanned, created, _ := MigrateVoiceAgentIDs(context.Background(), is, rs, ar)
	if scanned != 1 || created != 0 {
		t.Errorf("got scanned=%d created=%d, want 1/0", scanned, created)
	}
}

func TestMigrate_CreatesRouteForVoiceAgentID(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	inst := newInstance(t, "tg-1", tenantID, "speaking-bot")
	agentID := uuid.Must(uuid.NewV7())
	is := &fakeInstanceStore{list: []store.ChannelInstanceData{inst}}
	rs := &fakeRouteStoreMig{}
	ar := &fakeAgentResolver{byKey: map[string]*store.AgentData{
		"speaking-bot": {BaseModel: store.BaseModel{ID: agentID}, AgentKey: "speaking-bot"},
	}}
	scanned, created, err := MigrateVoiceAgentIDs(context.Background(), is, rs, ar)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if scanned != 1 || created != 1 {
		t.Fatalf("got scanned=%d created=%d, want 1/1", scanned, created)
	}
	got := rs.rows[inst.ID]
	if len(got) != 1 {
		t.Fatalf("got %d routes, want 1", len(got))
	}
	r := got[0]
	if r.AgentID != agentID {
		t.Errorf("agent id mismatch: got %s want %s", r.AgentID, agentID)
	}
	if r.TenantID != tenantID {
		t.Errorf("tenant id mismatch: got %s want %s", r.TenantID, tenantID)
	}
	if r.MediaType == nil || *r.MediaType != MediaKindVoice {
		t.Errorf("media type want voice, got %v", r.MediaType)
	}
	if r.PeerKind != "direct" || r.MentionRequired || !r.IsEnabled || r.Priority != 50 {
		t.Errorf("route defaults wrong: %+v", r)
	}
}

func TestMigrate_Idempotent_SecondRunNoop(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	inst := newInstance(t, "tg-1", tenantID, "speaking-bot")
	agentID := uuid.Must(uuid.NewV7())
	is := &fakeInstanceStore{list: []store.ChannelInstanceData{inst}}
	rs := &fakeRouteStoreMig{}
	ar := &fakeAgentResolver{byKey: map[string]*store.AgentData{
		"speaking-bot": {BaseModel: store.BaseModel{ID: agentID}, AgentKey: "speaking-bot"},
	}}
	// First run creates 1.
	if _, c, _ := MigrateVoiceAgentIDs(context.Background(), is, rs, ar); c != 1 {
		t.Fatalf("first run created=%d want 1", c)
	}
	// Second run finds existing → no-op.
	if _, c, _ := MigrateVoiceAgentIDs(context.Background(), is, rs, ar); c != 0 {
		t.Fatalf("second run created=%d want 0", c)
	}
	if got := len(rs.rows[inst.ID]); got != 1 {
		t.Errorf("rows after two runs = %d, want 1", got)
	}
}

func TestMigrate_AgentKeyNotFound_Skips(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	inst := newInstance(t, "tg-1", tenantID, "ghost-agent")
	is := &fakeInstanceStore{list: []store.ChannelInstanceData{inst}}
	rs := &fakeRouteStoreMig{}
	ar := &fakeAgentResolver{byKey: map[string]*store.AgentData{}}
	_, created, err := MigrateVoiceAgentIDs(context.Background(), is, rs, ar)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if created != 0 {
		t.Errorf("created=%d want 0 (agent not found)", created)
	}
}

func TestMigrate_MultiTenantIsolation(t *testing.T) {
	tenantA := uuid.Must(uuid.NewV7())
	tenantB := uuid.Must(uuid.NewV7())
	instA := newInstance(t, "tg-A", tenantA, "shared-key")
	instB := newInstance(t, "tg-B", tenantB, "shared-key")
	agentAID := uuid.Must(uuid.NewV7())
	agentBID := uuid.Must(uuid.NewV7())
	is := &fakeInstanceStore{list: []store.ChannelInstanceData{instA, instB}}
	rs := &fakeRouteStoreMig{}
	// Two agents with the same key (in different tenants) — resolver uses the
	// tenant-scoped ctx. fakeAgentResolver doesn't actually inspect ctx; the
	// test approximates by returning a single agent for now. The real test
	// of tenant scope happens at the store layer (covered in phase 02 tests).
	ar := &fakeAgentResolver{byKey: map[string]*store.AgentData{
		"shared-key": {BaseModel: store.BaseModel{ID: agentAID}, AgentKey: "shared-key"},
	}}
	_, createdFirst, _ := MigrateVoiceAgentIDs(context.Background(), is, rs, ar)
	if createdFirst != 2 {
		t.Fatalf("first run created %d, want 2 (one per instance)", createdFirst)
	}
	if rs.rows[instA.ID][0].TenantID != tenantA {
		t.Errorf("route A tenant mismatch")
	}
	if rs.rows[instB.ID][0].TenantID != tenantB {
		t.Errorf("route B tenant mismatch")
	}
	_ = agentBID
}

func TestMigrate_MalformedConfig_Skipped(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	inst := store.ChannelInstanceData{
		BaseModel: store.BaseModel{ID: uuid.Must(uuid.NewV7())},
		TenantID:  tenantID,
		Name:      "tg-bad",
		Config:    json.RawMessage(`not json{`),
	}
	is := &fakeInstanceStore{list: []store.ChannelInstanceData{inst}}
	rs := &fakeRouteStoreMig{}
	ar := &fakeAgentResolver{}
	_, created, err := MigrateVoiceAgentIDs(context.Background(), is, rs, ar)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if created != 0 {
		t.Errorf("created=%d want 0", created)
	}
}

func TestMigrate_NilDependency_Error(t *testing.T) {
	_, _, err := MigrateVoiceAgentIDs(context.Background(), nil, nil, nil)
	if err == nil {
		t.Error("want error on nil deps, got nil")
	}
}
