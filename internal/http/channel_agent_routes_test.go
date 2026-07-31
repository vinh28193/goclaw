package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/crypto"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// fakeRouteStore is a minimal in-memory ChannelAgentRouteStore for handler tests.
type fakeRouteStore struct {
	mu      sync.Mutex
	rows    map[uuid.UUID]*store.ChannelAgentRouteData
	createE error
	getE    error
	updateE error
	deleteE error
}

func newFakeRouteStore() *fakeRouteStore {
	return &fakeRouteStore{rows: map[uuid.UUID]*store.ChannelAgentRouteData{}}
}

func (s *fakeRouteStore) Create(_ context.Context, r *store.ChannelAgentRouteData) error {
	if s.createE != nil {
		return s.createE
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	s.rows[r.ID] = r
	return nil
}

func (s *fakeRouteStore) Get(_ context.Context, id uuid.UUID) (*store.ChannelAgentRouteData, error) {
	if s.getE != nil {
		return nil, s.getE
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return r, nil
}

func (s *fakeRouteStore) Update(_ context.Context, id uuid.UUID, updates map[string]any) error {
	if s.updateE != nil {
		return s.updateE
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return sql.ErrNoRows
	}
	if v, ok := updates["name"].(string); ok {
		r.Name = v
	}
	if v, ok := updates["agent_id"].(uuid.UUID); ok {
		r.AgentID = v
	}
	if v, ok := updates["peer_kind"].(string); ok {
		r.PeerKind = v
	}
	if v, ok := updates["media_type"]; ok {
		if p, ok := v.(*string); ok {
			r.MediaType = p
		}
	}
	if v, ok := updates["mention_required"].(bool); ok {
		r.MentionRequired = v
	}
	if v, ok := updates["priority"].(int); ok {
		r.Priority = v
	}
	if v, ok := updates["is_enabled"].(bool); ok {
		r.IsEnabled = v
	}
	if v, ok := updates["tool_allow"]; ok {
		if p, ok := v.(*[]string); ok {
			r.ToolAllow = p
		}
	}
	if v, ok := updates["peer_id"]; ok {
		if v == nil {
			r.PeerID = nil
		} else if s, ok := v.(string); ok {
			r.PeerID = &s
		}
	}
	return nil
}

func (s *fakeRouteStore) Delete(_ context.Context, id uuid.UUID) error {
	if s.deleteE != nil {
		return s.deleteE
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[id]; !ok {
		return sql.ErrNoRows
	}
	delete(s.rows, id)
	return nil
}

func (s *fakeRouteStore) ListByChannelInstance(_ context.Context, channelInstanceID uuid.UUID) ([]store.ChannelAgentRouteData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.ChannelAgentRouteData{}
	for _, r := range s.rows {
		if r.ChannelInstanceID == channelInstanceID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (s *fakeRouteStore) ListByTenant(_ context.Context) ([]store.ChannelAgentRouteData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.ChannelAgentRouteData{}
	for _, r := range s.rows {
		out = append(out, *r)
	}
	return out, nil
}

// fakeAgentStoreRoutes satisfies only the methods the route handler needs.
// Other AgentStore methods panic — guards future drift if handler grows.
type fakeAgentStoreRoutes struct {
	store.AgentStore // embed interface so we satisfy it without listing every method
	byID             map[uuid.UUID]*store.AgentData
}

func (f *fakeAgentStoreRoutes) GetByID(_ context.Context, id uuid.UUID) (*store.AgentData, error) {
	a, ok := f.byID[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return a, nil
}

// fakeAffinityStore is a minimal in-memory ChannelRoutingAffinityStore for
// asserting the handler busts sticky bindings on peer-pinned route mutation.
type fakeAffinityStore struct {
	mu       sync.Mutex
	bindings map[string]*store.ChannelRoutingAffinityData
	deleted  []string // recorded "channelInstanceID|peerID" Delete calls
}

func newFakeAffinityStore() *fakeAffinityStore {
	return &fakeAffinityStore{bindings: map[string]*store.ChannelRoutingAffinityData{}}
}

func affinityKey(channelInstanceID uuid.UUID, peerID string) string {
	return channelInstanceID.String() + "|" + peerID
}

func (f *fakeAffinityStore) seed(channelInstanceID uuid.UUID, peerID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindings[affinityKey(channelInstanceID, peerID)] = &store.ChannelRoutingAffinityData{
		ChannelInstanceID: channelInstanceID,
		PeerID:            peerID,
	}
}

func (f *fakeAffinityStore) Get(_ context.Context, channelInstanceID uuid.UUID, peerID string) (*store.ChannelRoutingAffinityData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.bindings[affinityKey(channelInstanceID, peerID)]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return b, nil
}

func (f *fakeAffinityStore) Upsert(_ context.Context, row *store.ChannelRoutingAffinityData) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindings[affinityKey(row.ChannelInstanceID, row.PeerID)] = row
	return nil
}

func (f *fakeAffinityStore) Delete(_ context.Context, channelInstanceID uuid.UUID, peerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := affinityKey(channelInstanceID, peerID)
	f.deleted = append(f.deleted, key)
	delete(f.bindings, key)
	return nil
}

func (f *fakeAffinityStore) DeletePeerForChannel(_ context.Context, channelInstanceID uuid.UUID) (int, error) {
	return 0, nil
}

func (f *fakeAffinityStore) DeleteExpired(_ context.Context, now time.Time) (int, error) {
	return 0, nil
}

// recordingInvalidator captures Invalidate calls so tests assert cache flips.
type recordingInvalidator struct {
	called []uuid.UUID
}

func (r *recordingInvalidator) Invalidate(id uuid.UUID) {
	r.called = append(r.called, id)
}

// ---- test setup helpers ----

func buildRouteHandlerEnv(t *testing.T) (
	*ChannelAgentRoutesHandler,
	*fakeRouteStore,
	*recordingInvalidator,
	uuid.UUID, // tenant id
	uuid.UUID, // channel instance id
	uuid.UUID, // agent id
	string, // bearer token
) {
	t.Helper()
	token := "agent-routes-key"
	tenantID := uuid.New()
	setupTestCache(t, map[string]*store.APIKeyData{
		crypto.HashAPIKey(token): {
			TenantID: tenantID,
			Scopes:   []string{"operator.admin", "operator.write", "operator.read"},
			OwnerID:  "caller",
		},
	})

	instID := uuid.New()
	agentID := uuid.New()
	inst := &store.ChannelInstanceData{
		BaseModel:   store.BaseModel{ID: instID},
		TenantID:    tenantID,
		Name:        "telegram-main",
		ChannelType: "telegram",
		AgentID:     agentID,
	}
	routes := newFakeRouteStore()
	inval := &recordingInvalidator{}
	agents := &fakeAgentStoreRoutes{byID: map[uuid.UUID]*store.AgentData{
		agentID: {BaseModel: store.BaseModel{ID: agentID}, TenantID: tenantID, AgentKey: "partner"},
	}}
	h := NewChannelAgentRoutesHandler(routes, &stubChannelInstanceStore{inst: inst}, agents, nil, inval)

	return h, routes, inval, tenantID, instID, agentID, token
}

func mountRouteHandler(h *ChannelAgentRoutesHandler) *http.ServeMux {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func bearer(req *http.Request, token string) *http.Request {
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// ---- happy-path tests ----

func TestChannelAgentRoutes_CreateMinimal(t *testing.T) {
	h, routes, inval, _, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"direct","priority":50,"is_enabled":true}`, agentID.String())
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp agentRouteResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.AgentID != agentID || resp.PeerKind != "direct" {
		t.Fatalf("got %+v", resp)
	}
	if resp.MediaType != nil {
		t.Fatalf("media_type should default to nil; got %q", *resp.MediaType)
	}
	if len(routes.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(routes.rows))
	}
	if len(inval.called) != 1 || inval.called[0] != instID {
		t.Fatalf("invalidator should have been called once for %s; got %v", instID, inval.called)
	}
}

func TestChannelAgentRoutes_CreateWithToolAllow(t *testing.T) {
	h, _, _, _, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"group","mention_required":true,"media_type":"voice","tool_allow":["A","B"," "]}`, agentID.String())
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp agentRouteResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.ToolAllow == nil || len(*resp.ToolAllow) != 2 {
		t.Fatalf("tool_allow should have 2 entries (blank stripped); got %+v", resp.ToolAllow)
	}
	if resp.MediaType == nil || *resp.MediaType != "voice" {
		t.Fatalf("media_type = %v, want voice", resp.MediaType)
	}
}

func TestChannelAgentRoutes_List(t *testing.T) {
	h, routes, _, tenantID, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	// Seed two rows directly.
	routes.rows[uuid.New()] = &store.ChannelAgentRouteData{
		BaseModel: store.BaseModel{ID: uuid.New()}, TenantID: tenantID,
		ChannelInstanceID: instID, AgentID: agentID, PeerKind: "direct", Priority: 100, IsEnabled: true,
	}
	routes.rows[uuid.New()] = &store.ChannelAgentRouteData{
		BaseModel: store.BaseModel{ID: uuid.New()}, TenantID: tenantID,
		ChannelInstanceID: instID, AgentID: agentID, PeerKind: "group", MentionRequired: true, Priority: 50, IsEnabled: true,
	}

	req := bearer(httptest.NewRequest(http.MethodGet, "/v1/channels/instances/"+instID.String()+"/agent-routes", nil), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Routes []agentRouteResponse `json:"routes"`
		Total  int                  `json:"total"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Total != 2 || len(resp.Routes) != 2 {
		t.Fatalf("expected 2 routes; got %+v", resp)
	}
}

func TestChannelAgentRoutes_UpdateInvalidatesCache(t *testing.T) {
	h, routes, inval, tenantID, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	rid := uuid.New()
	routes.rows[rid] = &store.ChannelAgentRouteData{
		BaseModel: store.BaseModel{ID: rid}, TenantID: tenantID,
		ChannelInstanceID: instID, AgentID: agentID, PeerKind: "direct", Priority: 100, IsEnabled: true,
	}

	body := `{"priority":10,"is_enabled":false}`
	req := bearer(httptest.NewRequest(http.MethodPatch, "/v1/channels/instances/"+instID.String()+"/agent-routes/"+rid.String(), bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(inval.called) != 1 || inval.called[0] != instID {
		t.Fatalf("Invalidate should fire for %s; got %v", instID, inval.called)
	}
	r := routes.rows[rid]
	if r.Priority != 10 || r.IsEnabled {
		t.Fatalf("update did not apply: %+v", r)
	}
}

func TestChannelAgentRoutes_DeleteReturns204(t *testing.T) {
	h, routes, inval, tenantID, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	rid := uuid.New()
	routes.rows[rid] = &store.ChannelAgentRouteData{
		BaseModel: store.BaseModel{ID: rid}, TenantID: tenantID,
		ChannelInstanceID: instID, AgentID: agentID, PeerKind: "direct", IsEnabled: true,
	}

	req := bearer(httptest.NewRequest(http.MethodDelete, "/v1/channels/instances/"+instID.String()+"/agent-routes/"+rid.String(), nil), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if _, exists := routes.rows[rid]; exists {
		t.Fatalf("row should have been deleted")
	}
	if len(inval.called) != 1 {
		t.Fatalf("Invalidate should fire once; got %d", len(inval.called))
	}
}

// ---- validation tests ----

func TestChannelAgentRoutes_RejectsInvalidPeerKind(t *testing.T) {
	h, _, _, _, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"channel"}`, agentID.String())
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

func TestChannelAgentRoutes_RejectsInvalidMediaType(t *testing.T) {
	h, _, _, _, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"direct","media_type":"sticker"}`, agentID.String())
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

func TestChannelAgentRoutes_RejectsForeignAgent(t *testing.T) {
	h, _, _, _, instID, _, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	// Agent belonging to a different tenant.
	otherAgentID := uuid.New()
	if f, ok := h.agents.(*fakeAgentStoreRoutes); ok {
		f.byID[otherAgentID] = &store.AgentData{
			BaseModel: store.BaseModel{ID: otherAgentID},
			TenantID:  uuid.New(), // different tenant
			AgentKey:  "cross-tenant",
		}
	}

	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"direct"}`, otherAgentID.String())
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (foreign agent); body=%s", rec.Code, rec.Body.String())
	}
}

func TestChannelAgentRoutes_ReturnsNoOpUpdateAs400(t *testing.T) {
	h, routes, _, tenantID, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	rid := uuid.New()
	routes.rows[rid] = &store.ChannelAgentRouteData{
		BaseModel: store.BaseModel{ID: rid}, TenantID: tenantID,
		ChannelInstanceID: instID, AgentID: agentID, PeerKind: "direct", IsEnabled: true,
	}

	req := bearer(httptest.NewRequest(http.MethodPatch, "/v1/channels/instances/"+instID.String()+"/agent-routes/"+rid.String(), bytes.NewBufferString(`{}`)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestChannelAgentRoutes_NotFoundOnWrongChannelID(t *testing.T) {
	h, routes, _, tenantID, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	rid := uuid.New()
	// Route exists but belongs to a DIFFERENT channel instance.
	routes.rows[rid] = &store.ChannelAgentRouteData{
		BaseModel: store.BaseModel{ID: rid}, TenantID: tenantID,
		ChannelInstanceID: uuid.New(), // different channel
		AgentID:           agentID, PeerKind: "direct", IsEnabled: true,
	}

	req := bearer(httptest.NewRequest(http.MethodGet, "/v1/channels/instances/"+instID.String()+"/agent-routes/"+rid.String(), nil), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// normalizeToolAllow unit checks — guards the contract that nil = inherit.
func TestNormalizeToolAllow(t *testing.T) {
	if normalizeToolAllow(nil) != nil {
		t.Fatal("nil input should stay nil")
	}
	empty := &[]string{"", "  "}
	if normalizeToolAllow(empty) != nil {
		t.Fatal("all-whitespace slice should collapse to nil (inherit)")
	}
	in := &[]string{"a", " ", "b"}
	out := normalizeToolAllow(in)
	if out == nil || len(*out) != 2 || (*out)[0] != "a" || (*out)[1] != "b" {
		t.Fatalf("got %+v", out)
	}
}

// Sanity guard: a sql.ErrNoRows from the store on Get should surface as 404.
func TestChannelAgentRoutes_GetUnknown(t *testing.T) {
	h, _, _, _, instID, _, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	missing := uuid.New()
	req := bearer(httptest.NewRequest(http.MethodGet, "/v1/channels/instances/"+instID.String()+"/agent-routes/"+missing.String(), nil), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// ---- peer_id + sticky-affinity bust tests ----

func TestChannelAgentRoutes_CreateWithPeerID(t *testing.T) {
	h, routes, _, _, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"direct","peer_id":"12345"}`, agentID.String())
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp agentRouteResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.PeerID == nil || *resp.PeerID != "12345" {
		t.Fatalf("peer_id = %v, want 12345", resp.PeerID)
	}
	var stored *store.ChannelAgentRouteData
	for _, r := range routes.rows {
		stored = r
	}
	if stored == nil || stored.PeerID == nil || *stored.PeerID != "12345" {
		t.Fatalf("stored row missing peer_id: %+v", stored)
	}
}

func TestChannelAgentRoutes_RejectsTooLongPeerID(t *testing.T) {
	h, _, _, _, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	longPeer := strings.Repeat("9", 129)
	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"direct","peer_id":%q}`, agentID.String(), longPeer)
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

func TestChannelAgentRoutes_CreateBustsExistingAffinity(t *testing.T) {
	h, _, _, _, instID, agentID, token := buildRouteHandlerEnv(t)
	affinity := newFakeAffinityStore()
	affinity.seed(instID, "12345")
	h.SetAffinityStore(affinity)
	mux := mountRouteHandler(h)

	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"direct","peer_id":"12345"}`, agentID.String())
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if len(affinity.deleted) != 1 || affinity.deleted[0] != affinityKey(instID, "12345") {
		t.Fatalf("expected affinity Delete for (%s,12345); got %v", instID, affinity.deleted)
	}
	if _, err := affinity.Get(context.Background(), instID, "12345"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("binding should have been evicted from the fake store")
	}
}

func TestChannelAgentRoutes_UpdateClearsPeerIDOnEmptyString(t *testing.T) {
	h, routes, _, tenantID, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	rid := uuid.New()
	existingPeer := "old-peer"
	routes.rows[rid] = &store.ChannelAgentRouteData{
		BaseModel: store.BaseModel{ID: rid}, TenantID: tenantID,
		ChannelInstanceID: instID, AgentID: agentID, PeerKind: "direct", IsEnabled: true,
		PeerID: &existingPeer,
	}

	body := `{"peer_id":""}`
	req := bearer(httptest.NewRequest(http.MethodPatch, "/v1/channels/instances/"+instID.String()+"/agent-routes/"+rid.String(), bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp agentRouteResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.PeerID != nil {
		t.Fatalf("peer_id should be cleared; got %v", *resp.PeerID)
	}
	if routes.rows[rid].PeerID != nil {
		t.Fatalf("stored row peer_id should be nil")
	}
}

func TestChannelAgentRoutes_UpdateBustsOldAndNewPeerAffinity(t *testing.T) {
	h, routes, _, tenantID, instID, agentID, token := buildRouteHandlerEnv(t)
	affinity := newFakeAffinityStore()
	affinity.seed(instID, "old-peer")
	affinity.seed(instID, "new-peer")
	h.SetAffinityStore(affinity)
	mux := mountRouteHandler(h)

	rid := uuid.New()
	existingPeer := "old-peer"
	routes.rows[rid] = &store.ChannelAgentRouteData{
		BaseModel: store.BaseModel{ID: rid}, TenantID: tenantID,
		ChannelInstanceID: instID, AgentID: agentID, PeerKind: "direct", IsEnabled: true,
		PeerID: &existingPeer,
	}

	body := `{"peer_id":"new-peer"}`
	req := bearer(httptest.NewRequest(http.MethodPatch, "/v1/channels/instances/"+instID.String()+"/agent-routes/"+rid.String(), bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	wantOld := affinityKey(instID, "old-peer")
	wantNew := affinityKey(instID, "new-peer")
	if len(affinity.deleted) != 2 || !((affinity.deleted[0] == wantOld && affinity.deleted[1] == wantNew) || (affinity.deleted[0] == wantNew && affinity.deleted[1] == wantOld)) {
		t.Fatalf("expected bust of both old and new peer affinity; got %v", affinity.deleted)
	}
}

func TestChannelAgentRoutes_DeleteBustsAffinity(t *testing.T) {
	h, routes, _, tenantID, instID, agentID, token := buildRouteHandlerEnv(t)
	affinity := newFakeAffinityStore()
	affinity.seed(instID, "dead-peer")
	h.SetAffinityStore(affinity)
	mux := mountRouteHandler(h)

	rid := uuid.New()
	peer := "dead-peer"
	routes.rows[rid] = &store.ChannelAgentRouteData{
		BaseModel: store.BaseModel{ID: rid}, TenantID: tenantID,
		ChannelInstanceID: instID, AgentID: agentID, PeerKind: "direct", IsEnabled: true,
		PeerID: &peer,
	}

	req := bearer(httptest.NewRequest(http.MethodDelete, "/v1/channels/instances/"+instID.String()+"/agent-routes/"+rid.String(), nil), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(affinity.deleted) != 1 || affinity.deleted[0] != affinityKey(instID, "dead-peer") {
		t.Fatalf("expected affinity Delete for dead-peer; got %v", affinity.deleted)
	}
}

func TestChannelAgentRoutes_NilAffinityStoreIsNoop(t *testing.T) {
	h, _, _, _, instID, agentID, token := buildRouteHandlerEnv(t)
	mux := mountRouteHandler(h)

	// No SetAffinityStore call — must not panic.
	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"direct","peer_id":"12345"}`, agentID.String())
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// 500 path — store Create error propagates as internal error.
func TestChannelAgentRoutes_CreateStoreError(t *testing.T) {
	h, routes, _, _, instID, agentID, token := buildRouteHandlerEnv(t)
	routes.createE = errors.New("boom")
	mux := mountRouteHandler(h)

	body := fmt.Sprintf(`{"agent_id":%q,"peer_kind":"direct"}`, agentID.String())
	req := bearer(httptest.NewRequest(http.MethodPost, "/v1/channels/instances/"+instID.String()+"/agent-routes", bytes.NewBufferString(body)), token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
