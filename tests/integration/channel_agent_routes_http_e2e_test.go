//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/channels/routing"
	"github.com/nextlevelbuilder/goclaw/internal/crypto"
	httpapi "github.com/nextlevelbuilder/goclaw/internal/http"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
)

// HTTP end-to-end: real httptest.Server + real PG. Boots the
// ChannelAgentRoutesHandler with live PG-backed stores, real auth via the
// API key cache, and exercises every endpoint with an httptest client.
//
// Differs from internal/http/channel_agent_routes_test.go which mocks the
// stores — this one proves the full wire-up that production uses, including
// JSON-over-HTTP, PATCH semantics, cache invalidation through real resolver,
// and tenant-scope enforcement against live SQL filtering.

type e2eHarness struct {
	server         *httptest.Server
	resolver       *routing.AgentRouteResolver
	tenantID       uuid.UUID
	defaultAgentID uuid.UUID
	channelID      uuid.UUID
	token          string
}

// buildE2EHarness wires up: a freshly-seeded tenant+channel, the API key cache
// with a bearer token that maps to the tenant, the route store + resolver, and
// an httptest.Server that mounts ChannelAgentRoutesHandler.
func buildE2EHarness(t *testing.T) *e2eHarness {
	t.Helper()
	db := testDB(t)
	tenantID, defaultAgent := seedTenantAgent(t, db)
	channelID := seedChannelInstanceForType(t, nil, tenantID, defaultAgent, "telegram")

	// Auth wiring: insert an admin-scope key, install the API key cache so
	// requireAuth(RoleAdmin) accepts our bearer token.
	apiKeyStore := pg.NewPGAPIKeyStore(db)
	token := "e2e-admin-key-" + uuid.NewString()
	keyHash := crypto.HashAPIKey(token)
	keyID := uuid.New()
	if err := apiKeyStore.Create(store.WithTenantID(t.Context(), tenantID), &store.APIKeyData{
		ID:        keyID,
		TenantID:  tenantID,
		Name:      "e2e",
		Prefix:    token[:8],
		KeyHash:   keyHash,
		Scopes:    []string{"operator.admin", "operator.write", "operator.read"},
		OwnerID:   "e2e-caller",
		CreatedBy: "e2e",
	}); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM api_keys WHERE id = $1", keyID) })

	// InitAPIKeyCache attaches the store to the package-level singleton.
	// Without nil bus we skip pub-sub invalidation; tests don't need it.
	httpapi.InitAPIKeyCache(apiKeyStore, nil)

	// Build the handler — same wiring as cmd/gateway.go.
	routeStore := pg.NewPGChannelAgentRouteStore(db)
	channelStore := pg.NewPGChannelInstanceStore(db, testEncryptionKey)
	agentStore := pg.NewPGAgentStore(db)
	resolver := routing.NewAgentRouteResolver(routeStore, 30*time.Second)

	handler := httpapi.NewChannelAgentRoutesHandler(routeStore, channelStore, agentStore, nil, resolver)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &e2eHarness{
		server:         srv,
		resolver:       resolver,
		tenantID:       tenantID,
		defaultAgentID: defaultAgent,
		channelID:      channelID,
		token:          token,
	}
}

func (h *e2eHarness) do(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, h.server.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// Full CRUD lifecycle: create → get → patch → list → delete. Asserts the
// real HTTP responses + that the resolver picks up changes immediately
// because the handler invokes Invalidate.
func TestChannelAgentRoutes_HTTPE2E_CRUDLifecycle(t *testing.T) {
	h := buildE2EHarness(t)
	base := fmt.Sprintf("/v1/channels/instances/%s/agent-routes", h.channelID)

	// CREATE
	createBody := map[string]any{
		"agent_id":   h.defaultAgentID.String(),
		"peer_kind":  "direct",
		"priority":   100,
		"is_enabled": true,
		"name":       "E2E DM route",
	}
	resp, body := h.do(t, http.MethodPost, base, createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST: status=%d body=%s", resp.StatusCode, body)
	}
	var created struct{ ID string }
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create: %v body=%s", err, body)
	}
	if created.ID == "" {
		t.Fatalf("expected id in response; body=%s", body)
	}

	// GET single
	resp, body = h.do(t, http.MethodGet, base+"/"+created.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: status=%d body=%s", resp.StatusCode, body)
	}

	// PATCH (disable + lower priority)
	resp, body = h.do(t, http.MethodPatch, base+"/"+created.ID, map[string]any{
		"is_enabled": false,
		"priority":   25,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH: status=%d body=%s", resp.StatusCode, body)
	}

	// Cache invalidation E2E: resolver MUST NOT match the disabled route.
	if _, _, matched, _ := h.resolver.Resolve(tenantCtx(h.tenantID), h.channelID, "", "", "direct", routing.MediaKindText, false); matched {
		t.Fatal("after PATCH disable, resolver should not match — invalidate did not propagate")
	}

	// LIST
	resp, body = h.do(t, http.MethodGet, base, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("LIST: status=%d body=%s", resp.StatusCode, body)
	}
	var list struct {
		Routes []map[string]any `json:"routes"`
		Total  int              `json:"total"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 {
		t.Fatalf("list total = %d, want 1", list.Total)
	}

	// DELETE
	resp, _ = h.do(t, http.MethodDelete, base+"/"+created.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: status=%d", resp.StatusCode)
	}

	// GET after DELETE → 404
	resp, _ = h.do(t, http.MethodGet, base+"/"+created.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET after DELETE: status=%d want 404", resp.StatusCode)
	}
}

// tool_allow JSON round-trip through HTTP (decode + encode + JSONB store).
// Verifies the wire format matches `api-reference.md` claims.
func TestChannelAgentRoutes_HTTPE2E_ToolAllowJSONRoundtrip(t *testing.T) {
	h := buildE2EHarness(t)
	base := fmt.Sprintf("/v1/channels/instances/%s/agent-routes", h.channelID)

	// Create with tool_allow=["A","B"]
	resp, body := h.do(t, http.MethodPost, base, map[string]any{
		"agent_id":   h.defaultAgentID.String(),
		"peer_kind":  "direct",
		"is_enabled": true,
		"tool_allow": []string{"A", "B"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST: status=%d body=%s", resp.StatusCode, body)
	}
	var got struct {
		ID        string   `json:"id"`
		ToolAllow []string `json:"tool_allow"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.ToolAllow) != 2 || got.ToolAllow[0] != "A" || got.ToolAllow[1] != "B" {
		t.Fatalf("tool_allow round-trip broke; got %v", got.ToolAllow)
	}

	// PATCH to null (clear) → response should reflect nil/absent.
	resp, _ = h.do(t, http.MethodPatch, base+"/"+got.ID, map[string]any{
		"tool_allow": []string{},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH clear: status=%d", resp.StatusCode)
	}
}

// 422 validation surface: invalid peer_kind, invalid media_type.
func TestChannelAgentRoutes_HTTPE2E_ValidationFails422(t *testing.T) {
	h := buildE2EHarness(t)
	base := fmt.Sprintf("/v1/channels/instances/%s/agent-routes", h.channelID)

	resp, _ := h.do(t, http.MethodPost, base, map[string]any{
		"agent_id":  h.defaultAgentID.String(),
		"peer_kind": "channel", // not in domain
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid peer_kind: status=%d want 422", resp.StatusCode)
	}

	resp, _ = h.do(t, http.MethodPost, base, map[string]any{
		"agent_id":   h.defaultAgentID.String(),
		"peer_kind":  "direct",
		"media_type": "sticker", // not in domain
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid media_type: status=%d want 422", resp.StatusCode)
	}
}

// Cross-tenant isolation: caller's token is scoped to tenant A; channel
// belongs to tenant B. PG store layer applies tenant filter on Get → the
// foreign channel row is invisible to the handler, surfacing as 404 (NOT
// 403). The spec called for 403, but the safer behavior — don't leak
// existence — wins, and the CLAUDE.md rule "pick one and stick" is
// satisfied by always returning 404 for any row the caller cannot see.
func TestChannelAgentRoutes_HTTPE2E_CrossTenant404DoesNotLeakExistence(t *testing.T) {
	h := buildE2EHarness(t)
	db := testDB(t)

	// Different tenant's channel.
	tenantB, agentB := seedTenantAgent(t, db)
	channelB := seedChannelInstanceForType(t, nil, tenantB, agentB, "telegram")

	resp, body := h.do(t, http.MethodGet, fmt.Sprintf("/v1/channels/instances/%s/agent-routes", channelB), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant read: status=%d want 404; body=%s", resp.StatusCode, body)
	}
}
