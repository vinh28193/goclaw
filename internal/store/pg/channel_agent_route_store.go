package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// PGChannelAgentRouteStore implements store.ChannelAgentRouteStore backed by Postgres.
type PGChannelAgentRouteStore struct {
	db *sql.DB
}

func NewPGChannelAgentRouteStore(db *sql.DB) *PGChannelAgentRouteStore {
	return &PGChannelAgentRouteStore{db: db}
}

const channelAgentRouteSelectCols = `id, tenant_id, channel_instance_id, agent_id, name,
 peer_kind, media_type, mention_required, priority, is_enabled, tool_allow,
 intent, target_kind, created_at, updated_at`

// Create derives tenant_id from the parent channel_instance row (single source of truth).
// If the caller passes a tenant context or sets r.TenantID, the values must match.
func (s *PGChannelAgentRouteStore) Create(ctx context.Context, r *store.ChannelAgentRouteData) error {
	if r.ChannelInstanceID == uuid.Nil {
		return errors.New("channel_instance_id required")
	}
	if r.AgentID == uuid.Nil {
		return errors.New("agent_id required")
	}
	if !isValidPeerKind(r.PeerKind) {
		return fmt.Errorf("invalid peer_kind: %q", r.PeerKind)
	}
	if r.MediaType != nil && !isValidMediaType(*r.MediaType) {
		return fmt.Errorf("invalid media_type: %q", *r.MediaType)
	}

	var derivedTenantID uuid.UUID
	if err := s.db.QueryRowContext(ctx,
		`SELECT tenant_id FROM channel_instances WHERE id = $1`,
		r.ChannelInstanceID).Scan(&derivedTenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("channel_instance %s not found", r.ChannelInstanceID)
		}
		return fmt.Errorf("lookup channel_instance tenant: %w", err)
	}

	if r.TenantID != uuid.Nil && r.TenantID != derivedTenantID {
		return fmt.Errorf("tenant_id mismatch: caller=%s channel=%s", r.TenantID, derivedTenantID)
	}
	if ctxTID := store.TenantIDFromContext(ctx); !store.IsCrossTenant(ctx) && ctxTID != uuid.Nil && ctxTID != derivedTenantID {
		return fmt.Errorf("tenant context %s does not own channel %s", ctxTID, r.ChannelInstanceID)
	}
	r.TenantID = derivedTenantID

	if r.ID == uuid.Nil {
		r.ID = store.GenNewID()
	}
	now := time.Now()
	r.CreatedAt = now
	r.UpdatedAt = now

	toolAllowJSON, err := marshalToolAllow(r.ToolAllow)
	if err != nil {
		return err
	}

	// Default target_kind to "agent" — legacy CRUD callers that don't set it
	// keep the original single-agent semantics.
	if r.TargetKind == "" {
		r.TargetKind = store.RouteTargetAgent
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO channel_agent_routes (id, tenant_id, channel_instance_id, agent_id, name,
		 peer_kind, media_type, mention_required, priority, is_enabled, tool_allow,
		 intent, target_kind, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		r.ID, r.TenantID, r.ChannelInstanceID, r.AgentID, r.Name,
		r.PeerKind, r.MediaType, r.MentionRequired, r.Priority, r.IsEnabled, toolAllowJSON,
		r.Intent, r.TargetKind, now, now,
	)
	return err
}

func (s *PGChannelAgentRouteStore) Get(ctx context.Context, id uuid.UUID) (*store.ChannelAgentRouteData, error) {
	if store.IsCrossTenant(ctx) {
		row := s.db.QueryRowContext(ctx,
			`SELECT `+channelAgentRouteSelectCols+` FROM channel_agent_routes WHERE id = $1`, id)
		return scanRoute(row)
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+channelAgentRouteSelectCols+` FROM channel_agent_routes WHERE id = $1 AND tenant_id = $2`,
		id, tid)
	return scanRoute(row)
}

func (s *PGChannelAgentRouteStore) Update(ctx context.Context, id uuid.UUID, updates map[string]any) error {
	if v, ok := updates["peer_kind"]; ok {
		if str, _ := v.(string); !isValidPeerKind(str) {
			return fmt.Errorf("invalid peer_kind: %q", str)
		}
	}
	if v, ok := updates["media_type"]; ok && v != nil {
		switch s := v.(type) {
		case string:
			if !isValidMediaType(s) {
				return fmt.Errorf("invalid media_type: %q", s)
			}
		case *string:
			if s != nil && !isValidMediaType(*s) {
				return fmt.Errorf("invalid media_type: %q", *s)
			}
		}
	}
	if v, ok := updates["tool_allow"]; ok {
		// Normalize *[]string or []string to JSON bytes; nil stays NULL.
		marshalled, err := normalizeToolAllowForUpdate(v)
		if err != nil {
			return err
		}
		updates["tool_allow"] = marshalled
	}
	if store.IsCrossTenant(ctx) {
		return execMapUpdate(ctx, s.db, "channel_agent_routes", id, updates)
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return errors.New("tenant_id required for update")
	}
	return execMapUpdateWhereTenant(ctx, s.db, "channel_agent_routes", updates, id, tid)
}

func (s *PGChannelAgentRouteStore) Delete(ctx context.Context, id uuid.UUID) error {
	if store.IsCrossTenant(ctx) {
		_, err := s.db.ExecContext(ctx, "DELETE FROM channel_agent_routes WHERE id = $1", id)
		return err
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return errors.New("tenant_id required")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM channel_agent_routes WHERE id = $1 AND tenant_id = $2", id, tid)
	return err
}

func (s *PGChannelAgentRouteStore) ListByChannelInstance(ctx context.Context, channelInstanceID uuid.UUID) ([]store.ChannelAgentRouteData, error) {
	query := `SELECT ` + channelAgentRouteSelectCols + ` FROM channel_agent_routes WHERE channel_instance_id = $1`
	args := []any{channelInstanceID}
	if !store.IsCrossTenant(ctx) {
		tid := store.TenantIDFromContext(ctx)
		if tid == uuid.Nil {
			return nil, nil
		}
		query += ` AND tenant_id = $2`
		args = append(args, tid)
	}
	query += ` ORDER BY priority ASC, created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanRoutes(rows)
}

func (s *PGChannelAgentRouteStore) ListByTenant(ctx context.Context) ([]store.ChannelAgentRouteData, error) {
	query := `SELECT ` + channelAgentRouteSelectCols + ` FROM channel_agent_routes`
	var args []any
	if !store.IsCrossTenant(ctx) {
		tid := store.TenantIDFromContext(ctx)
		if tid == uuid.Nil {
			return nil, nil
		}
		query += ` WHERE tenant_id = $1`
		args = append(args, tid)
	}
	query += ` ORDER BY channel_instance_id, priority ASC, created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanRoutes(rows)
}

// --- scan / marshal helpers (PG-local) ---

func scanRoute(row *sql.Row) (*store.ChannelAgentRouteData, error) {
	var r store.ChannelAgentRouteData
	var mediaType, intent *string
	var toolAllowRaw []byte
	if err := row.Scan(
		&r.ID, &r.TenantID, &r.ChannelInstanceID, &r.AgentID, &r.Name,
		&r.PeerKind, &mediaType, &r.MentionRequired, &r.Priority, &r.IsEnabled, &toolAllowRaw,
		&intent, &r.TargetKind, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	r.MediaType = mediaType
	r.Intent = intent
	if err := assignToolAllow(&r, toolAllowRaw); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanRoutes(rows *sql.Rows) ([]store.ChannelAgentRouteData, error) {
	defer rows.Close()
	var out []store.ChannelAgentRouteData
	for rows.Next() {
		var r store.ChannelAgentRouteData
		var mediaType, intent *string
		var toolAllowRaw []byte
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.ChannelInstanceID, &r.AgentID, &r.Name,
			&r.PeerKind, &mediaType, &r.MentionRequired, &r.Priority, &r.IsEnabled, &toolAllowRaw,
			&intent, &r.TargetKind, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		r.MediaType = mediaType
		r.Intent = intent
		if err := assignToolAllow(&r, toolAllowRaw); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func marshalToolAllow(allow *[]string) (any, error) {
	if allow == nil {
		return nil, nil
	}
	b, err := json.Marshal(*allow)
	if err != nil {
		return nil, fmt.Errorf("marshal tool_allow: %w", err)
	}
	return b, nil
}

func assignToolAllow(r *store.ChannelAgentRouteData, raw []byte) error {
	if len(raw) == 0 {
		r.ToolAllow = nil
		return nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return fmt.Errorf("unmarshal tool_allow: %w", err)
	}
	r.ToolAllow = &arr
	return nil
}

func normalizeToolAllowForUpdate(v any) (any, error) {
	switch tv := v.(type) {
	case nil:
		return nil, nil
	case *[]string:
		if tv == nil {
			return nil, nil
		}
		return json.Marshal(*tv)
	case []string:
		return json.Marshal(tv)
	case []byte:
		return tv, nil
	case string:
		return []byte(tv), nil
	default:
		return json.Marshal(v)
	}
}

func isValidPeerKind(k string) bool {
	switch k {
	case "direct", "group", "supergroup":
		return true
	}
	return false
}

func isValidMediaType(m string) bool {
	switch m {
	case "text", "voice", "media":
		return true
	}
	return false
}
