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

// PGChannelRoutingAffinityStore implements store.ChannelRoutingAffinityStore
// backed by Postgres. All reads filter on expires_at > NOW() so callers never
// see expired rows even if the cleanup cron lags.
type PGChannelRoutingAffinityStore struct {
	db *sql.DB
}

func NewPGChannelRoutingAffinityStore(db *sql.DB) *PGChannelRoutingAffinityStore {
	return &PGChannelRoutingAffinityStore{db: db}
}

const affinitySelectCols = `tenant_id, channel_instance_id, peer_id, agent_id, tool_allow, expires_at, created_at, updated_at`

func (s *PGChannelRoutingAffinityStore) Get(ctx context.Context, channelInstanceID uuid.UUID, peerID string) (*store.ChannelRoutingAffinityData, error) {
	if channelInstanceID == uuid.Nil || peerID == "" {
		return nil, sql.ErrNoRows
	}
	var q string
	var args []any
	if store.IsCrossTenant(ctx) {
		q = `SELECT ` + affinitySelectCols + ` FROM channel_routing_affinity
		     WHERE channel_instance_id = $1 AND peer_id = $2 AND expires_at > NOW()`
		args = []any{channelInstanceID, peerID}
	} else {
		tid := store.TenantIDFromContext(ctx)
		if tid == uuid.Nil {
			return nil, sql.ErrNoRows
		}
		q = `SELECT ` + affinitySelectCols + ` FROM channel_routing_affinity
		     WHERE channel_instance_id = $1 AND peer_id = $2 AND tenant_id = $3 AND expires_at > NOW()`
		args = []any{channelInstanceID, peerID, tid}
	}
	row := s.db.QueryRowContext(ctx, q, args...)
	return scanAffinityRow(row)
}

func (s *PGChannelRoutingAffinityStore) Upsert(ctx context.Context, r *store.ChannelRoutingAffinityData) error {
	if r == nil {
		return errors.New("nil affinity row")
	}
	if r.ChannelInstanceID == uuid.Nil || r.AgentID == uuid.Nil || r.PeerID == "" {
		return errors.New("channel_instance_id, peer_id, agent_id required")
	}
	if r.TenantID == uuid.Nil {
		r.TenantID = store.TenantIDFromContext(ctx)
		if r.TenantID == uuid.Nil {
			return errors.New("tenant_id required (set on row or via ctx)")
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
		toolAllowJSON = buf
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO channel_routing_affinity
		    (tenant_id, channel_instance_id, peer_id, agent_id, tool_allow, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (tenant_id, channel_instance_id, peer_id) DO UPDATE SET
		    agent_id   = EXCLUDED.agent_id,
		    tool_allow = EXCLUDED.tool_allow,
		    expires_at = EXCLUDED.expires_at,
		    updated_at = NOW()
	`, r.TenantID, r.ChannelInstanceID, r.PeerID, r.AgentID, toolAllowJSON, r.ExpiresAt)
	return err
}

func (s *PGChannelRoutingAffinityStore) Delete(ctx context.Context, channelInstanceID uuid.UUID, peerID string) error {
	if channelInstanceID == uuid.Nil || peerID == "" {
		return errors.New("channel_instance_id and peer_id required")
	}
	if store.IsCrossTenant(ctx) {
		_, err := s.db.ExecContext(ctx,
			`DELETE FROM channel_routing_affinity WHERE channel_instance_id = $1 AND peer_id = $2`,
			channelInstanceID, peerID)
		return err
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return errors.New("tenant context required")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_routing_affinity WHERE channel_instance_id = $1 AND peer_id = $2 AND tenant_id = $3`,
		channelInstanceID, peerID, tid)
	return err
}

func (s *PGChannelRoutingAffinityStore) DeletePeerForChannel(ctx context.Context, channelInstanceID uuid.UUID) (int, error) {
	if channelInstanceID == uuid.Nil {
		return 0, errors.New("channel_instance_id required")
	}
	var res sql.Result
	var err error
	if store.IsCrossTenant(ctx) {
		res, err = s.db.ExecContext(ctx,
			`DELETE FROM channel_routing_affinity WHERE channel_instance_id = $1`,
			channelInstanceID)
	} else {
		tid := store.TenantIDFromContext(ctx)
		if tid == uuid.Nil {
			return 0, errors.New("tenant context required")
		}
		res, err = s.db.ExecContext(ctx,
			`DELETE FROM channel_routing_affinity WHERE channel_instance_id = $1 AND tenant_id = $2`,
			channelInstanceID, tid)
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *PGChannelRoutingAffinityStore) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_routing_affinity WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanAffinityRow(row *sql.Row) (*store.ChannelRoutingAffinityData, error) {
	var d store.ChannelRoutingAffinityData
	var toolAllowJSON *[]byte
	err := row.Scan(&d.TenantID, &d.ChannelInstanceID, &d.PeerID, &d.AgentID, &toolAllowJSON, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if toolAllowJSON != nil {
		var arr []string
		if err := json.Unmarshal(*toolAllowJSON, &arr); err != nil {
			return nil, fmt.Errorf("decode tool_allow: %w", err)
		}
		d.ToolAllow = &arr
	}
	return &d, nil
}
