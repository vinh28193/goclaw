//go:build sqlite || sqliteonly

package sqlitestore

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

// SQLiteChannelAgentRouteStore implements store.ChannelAgentRouteStore backed by SQLite.
type SQLiteChannelAgentRouteStore struct {
	db *sql.DB
}

func NewSQLiteChannelAgentRouteStore(db *sql.DB) *SQLiteChannelAgentRouteStore {
	return &SQLiteChannelAgentRouteStore{db: db}
}

const channelAgentRouteSelectCols = `id, tenant_id, channel_instance_id, agent_id, name,
 peer_kind, media_type, mention_required, priority, is_enabled, tool_allow,
 intent, target_kind, created_at, updated_at`

func (s *SQLiteChannelAgentRouteStore) Create(ctx context.Context, r *store.ChannelAgentRouteData) error {
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
		`SELECT tenant_id FROM channel_instances WHERE id = ?`,
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

	toolAllowJSON, err := marshalToolAllowSQLite(r.ToolAllow)
	if err != nil {
		return err
	}

	if r.TargetKind == "" {
		r.TargetKind = store.RouteTargetAgent
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO channel_agent_routes (id, tenant_id, channel_instance_id, agent_id, name,
		 peer_kind, media_type, mention_required, priority, is_enabled, tool_allow,
		 intent, target_kind, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.TenantID, r.ChannelInstanceID, r.AgentID, r.Name,
		r.PeerKind, r.MediaType, boolToInt(r.MentionRequired), r.Priority, boolToInt(r.IsEnabled), toolAllowJSON,
		r.Intent, r.TargetKind, now, now,
	)
	return err
}

func (s *SQLiteChannelAgentRouteStore) Get(ctx context.Context, id uuid.UUID) (*store.ChannelAgentRouteData, error) {
	if store.IsCrossTenant(ctx) {
		row := s.db.QueryRowContext(ctx,
			`SELECT `+channelAgentRouteSelectCols+` FROM channel_agent_routes WHERE id = ?`, id)
		return scanRouteSQLite(row)
	}
	tClause, tArgs, err := scopeClause(ctx)
	if err != nil {
		return nil, err
	}
	args := append([]any{id}, tArgs...)
	row := s.db.QueryRowContext(ctx,
		`SELECT `+channelAgentRouteSelectCols+` FROM channel_agent_routes WHERE id = ?`+tClause, args...)
	return scanRouteSQLite(row)
}

func (s *SQLiteChannelAgentRouteStore) Update(ctx context.Context, id uuid.UUID, updates map[string]any) error {
	if v, ok := updates["peer_kind"]; ok {
		if str, _ := v.(string); !isValidPeerKind(str) {
			return fmt.Errorf("invalid peer_kind: %q", str)
		}
	}
	if v, ok := updates["media_type"]; ok && v != nil {
		switch sv := v.(type) {
		case string:
			if !isValidMediaType(sv) {
				return fmt.Errorf("invalid media_type: %q", sv)
			}
		case *string:
			if sv != nil && !isValidMediaType(*sv) {
				return fmt.Errorf("invalid media_type: %q", *sv)
			}
		}
	}
	if v, ok := updates["tool_allow"]; ok {
		marshalled, err := normalizeToolAllowForUpdateSQLite(v)
		if err != nil {
			return err
		}
		updates["tool_allow"] = marshalled
	}
	if v, ok := updates["mention_required"]; ok {
		if b, isBool := v.(bool); isBool {
			updates["mention_required"] = boolToInt(b)
		}
	}
	if v, ok := updates["is_enabled"]; ok {
		if b, isBool := v.(bool); isBool {
			updates["is_enabled"] = boolToInt(b)
		}
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

func (s *SQLiteChannelAgentRouteStore) Delete(ctx context.Context, id uuid.UUID) error {
	if store.IsCrossTenant(ctx) {
		_, err := s.db.ExecContext(ctx, "DELETE FROM channel_agent_routes WHERE id = ?", id)
		return err
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return errors.New("tenant_id required")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM channel_agent_routes WHERE id = ? AND tenant_id = ?", id, tid)
	return err
}

func (s *SQLiteChannelAgentRouteStore) ListByChannelInstance(ctx context.Context, channelInstanceID uuid.UUID) ([]store.ChannelAgentRouteData, error) {
	query := `SELECT ` + channelAgentRouteSelectCols + ` FROM channel_agent_routes WHERE channel_instance_id = ?`
	args := []any{channelInstanceID}
	if !store.IsCrossTenant(ctx) {
		tid := store.TenantIDFromContext(ctx)
		if tid == uuid.Nil {
			return nil, nil
		}
		query += ` AND tenant_id = ?`
		args = append(args, tid)
	}
	query += ` ORDER BY priority ASC, created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanRoutesSQLite(rows)
}

func (s *SQLiteChannelAgentRouteStore) ListByTenant(ctx context.Context) ([]store.ChannelAgentRouteData, error) {
	query := `SELECT ` + channelAgentRouteSelectCols + ` FROM channel_agent_routes`
	var args []any
	if !store.IsCrossTenant(ctx) {
		tid := store.TenantIDFromContext(ctx)
		if tid == uuid.Nil {
			return nil, nil
		}
		query += ` WHERE tenant_id = ?`
		args = append(args, tid)
	}
	query += ` ORDER BY channel_instance_id, priority ASC, created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanRoutesSQLite(rows)
}

// --- scan / marshal helpers (SQLite-local) ---

func scanRouteSQLite(row *sql.Row) (*store.ChannelAgentRouteData, error) {
	var r store.ChannelAgentRouteData
	var mediaType, intent *string
	var toolAllowRaw *string
	var mentionInt, enabledInt int
	createdAt, updatedAt := scanTimePair()
	if err := row.Scan(
		&r.ID, &r.TenantID, &r.ChannelInstanceID, &r.AgentID, &r.Name,
		&r.PeerKind, &mediaType, &mentionInt, &r.Priority, &enabledInt, &toolAllowRaw,
		&intent, &r.TargetKind, createdAt, updatedAt,
	); err != nil {
		return nil, err
	}
	r.MediaType = mediaType
	r.Intent = intent
	r.MentionRequired = mentionInt != 0
	r.IsEnabled = enabledInt != 0
	r.CreatedAt = createdAt.Time
	r.UpdatedAt = updatedAt.Time
	if err := assignToolAllowSQLite(&r, toolAllowRaw); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanRoutesSQLite(rows *sql.Rows) ([]store.ChannelAgentRouteData, error) {
	defer rows.Close()
	var out []store.ChannelAgentRouteData
	for rows.Next() {
		var r store.ChannelAgentRouteData
		var mediaType, intent *string
		var toolAllowRaw *string
		var mentionInt, enabledInt int
		createdAt, updatedAt := scanTimePair()
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.ChannelInstanceID, &r.AgentID, &r.Name,
			&r.PeerKind, &mediaType, &mentionInt, &r.Priority, &enabledInt, &toolAllowRaw,
			&intent, &r.TargetKind, createdAt, updatedAt,
		); err != nil {
			return nil, err
		}
		r.MediaType = mediaType
		r.Intent = intent
		r.MentionRequired = mentionInt != 0
		r.IsEnabled = enabledInt != 0
		r.CreatedAt = createdAt.Time
		r.UpdatedAt = updatedAt.Time
		if err := assignToolAllowSQLite(&r, toolAllowRaw); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func marshalToolAllowSQLite(allow *[]string) (any, error) {
	if allow == nil {
		return nil, nil
	}
	b, err := json.Marshal(*allow)
	if err != nil {
		return nil, fmt.Errorf("marshal tool_allow: %w", err)
	}
	return string(b), nil
}

func assignToolAllowSQLite(r *store.ChannelAgentRouteData, raw *string) error {
	if raw == nil || *raw == "" {
		r.ToolAllow = nil
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(*raw), &arr); err != nil {
		return fmt.Errorf("unmarshal tool_allow: %w", err)
	}
	r.ToolAllow = &arr
	return nil
}

func normalizeToolAllowForUpdateSQLite(v any) (any, error) {
	switch tv := v.(type) {
	case nil:
		return nil, nil
	case *[]string:
		if tv == nil {
			return nil, nil
		}
		b, err := json.Marshal(*tv)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case []string:
		b, err := json.Marshal(tv)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case string:
		return tv, nil
	case []byte:
		return string(tv), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return string(b), nil
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

