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

// SQLiteChannelRoutingAffinityStore mirrors the Postgres impl on SQLite for
// desktop (Lite) builds. SQLite has no NOW() function so we pass time values
// from Go and compare against ISO-8601 text columns.
type SQLiteChannelRoutingAffinityStore struct {
	db *sql.DB
}

func NewSQLiteChannelRoutingAffinityStore(db *sql.DB) *SQLiteChannelRoutingAffinityStore {
	return &SQLiteChannelRoutingAffinityStore{db: db}
}

const affinitySelectCols = `tenant_id, channel_instance_id, peer_id, agent_id, tool_allow, expires_at, created_at, updated_at`

func (s *SQLiteChannelRoutingAffinityStore) Get(ctx context.Context, channelInstanceID uuid.UUID, peerID string) (*store.ChannelRoutingAffinityData, error) {
	if channelInstanceID == uuid.Nil || peerID == "" {
		return nil, sql.ErrNoRows
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	var q string
	var args []any
	if store.IsCrossTenant(ctx) {
		q = `SELECT ` + affinitySelectCols + ` FROM channel_routing_affinity
		     WHERE channel_instance_id = ? AND peer_id = ? AND expires_at > ?`
		args = []any{channelInstanceID.String(), peerID, now}
	} else {
		tid := store.TenantIDFromContext(ctx)
		if tid == uuid.Nil {
			return nil, sql.ErrNoRows
		}
		q = `SELECT ` + affinitySelectCols + ` FROM channel_routing_affinity
		     WHERE channel_instance_id = ? AND peer_id = ? AND tenant_id = ? AND expires_at > ?`
		args = []any{channelInstanceID.String(), peerID, tid.String(), now}
	}
	row := s.db.QueryRowContext(ctx, q, args...)
	return scanAffinityRow(row)
}

func (s *SQLiteChannelRoutingAffinityStore) Upsert(ctx context.Context, r *store.ChannelRoutingAffinityData) error {
	if r == nil {
		return errors.New("nil affinity row")
	}
	if r.ChannelInstanceID == uuid.Nil || r.AgentID == uuid.Nil || r.PeerID == "" {
		return errors.New("channel_instance_id, peer_id, agent_id required")
	}
	if r.TenantID == uuid.Nil {
		r.TenantID = store.TenantIDFromContext(ctx)
		if r.TenantID == uuid.Nil {
			return errors.New("tenant_id required")
		}
	}
	if r.ExpiresAt.IsZero() {
		return errors.New("expires_at required")
	}
	var toolAllowJSON any
	if r.ToolAllow != nil {
		buf, err := json.Marshal(*r.ToolAllow)
		if err != nil {
			return fmt.Errorf("marshal tool_allow: %w", err)
		}
		toolAllowJSON = string(buf)
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	expiresStr := r.ExpiresAt.UTC().Format("2006-01-02T15:04:05.000Z")
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO channel_routing_affinity
		    (tenant_id, channel_instance_id, peer_id, agent_id, tool_allow, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, channel_instance_id, peer_id) DO UPDATE SET
		    agent_id   = excluded.agent_id,
		    tool_allow = excluded.tool_allow,
		    expires_at = excluded.expires_at,
		    updated_at = excluded.updated_at
	`, r.TenantID.String(), r.ChannelInstanceID.String(), r.PeerID, r.AgentID.String(),
		toolAllowJSON, expiresStr, now, now)
	return err
}

func (s *SQLiteChannelRoutingAffinityStore) Delete(ctx context.Context, channelInstanceID uuid.UUID, peerID string) error {
	if channelInstanceID == uuid.Nil || peerID == "" {
		return errors.New("channel_instance_id and peer_id required")
	}
	if store.IsCrossTenant(ctx) {
		_, err := s.db.ExecContext(ctx,
			`DELETE FROM channel_routing_affinity WHERE channel_instance_id = ? AND peer_id = ?`,
			channelInstanceID.String(), peerID)
		return err
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return errors.New("tenant context required")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_routing_affinity WHERE channel_instance_id = ? AND peer_id = ? AND tenant_id = ?`,
		channelInstanceID.String(), peerID, tid.String())
	return err
}

func (s *SQLiteChannelRoutingAffinityStore) DeletePeerForChannel(ctx context.Context, channelInstanceID uuid.UUID) (int, error) {
	if channelInstanceID == uuid.Nil {
		return 0, errors.New("channel_instance_id required")
	}
	var res sql.Result
	var err error
	if store.IsCrossTenant(ctx) {
		res, err = s.db.ExecContext(ctx,
			`DELETE FROM channel_routing_affinity WHERE channel_instance_id = ?`,
			channelInstanceID.String())
	} else {
		tid := store.TenantIDFromContext(ctx)
		if tid == uuid.Nil {
			return 0, errors.New("tenant context required")
		}
		res, err = s.db.ExecContext(ctx,
			`DELETE FROM channel_routing_affinity WHERE channel_instance_id = ? AND tenant_id = ?`,
			channelInstanceID.String(), tid.String())
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteChannelRoutingAffinityStore) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	nowStr := now.UTC().Format("2006-01-02T15:04:05.000Z")
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_routing_affinity WHERE expires_at <= ?`, nowStr)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanAffinityRow(row *sql.Row) (*store.ChannelRoutingAffinityData, error) {
	var d store.ChannelRoutingAffinityData
	var tenantIDStr, channelIDStr, agentIDStr string
	var toolAllowText sql.NullString
	var expiresStr, createdStr, updatedStr string
	if err := row.Scan(&tenantIDStr, &channelIDStr, &d.PeerID, &agentIDStr, &toolAllowText, &expiresStr, &createdStr, &updatedStr); err != nil {
		return nil, err
	}
	if id, err := uuid.Parse(tenantIDStr); err == nil {
		d.TenantID = id
	}
	if id, err := uuid.Parse(channelIDStr); err == nil {
		d.ChannelInstanceID = id
	}
	if id, err := uuid.Parse(agentIDStr); err == nil {
		d.AgentID = id
	}
	if toolAllowText.Valid {
		var arr []string
		if err := json.Unmarshal([]byte(toolAllowText.String), &arr); err != nil {
			return nil, fmt.Errorf("decode tool_allow: %w", err)
		}
		d.ToolAllow = &arr
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", expiresStr); err == nil {
		d.ExpiresAt = t
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", createdStr); err == nil {
		d.CreatedAt = t
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", updatedStr); err == nil {
		d.UpdatedAt = t
	}
	return &d, nil
}
