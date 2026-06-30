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
	"sync"
	"testing"

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
