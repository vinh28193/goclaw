package mcp

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// TestInjectSenderIdentity_WakeDigestIdentity closes the loop for Track B
// Task 10b: proves that the RunRequest fields internal/http.applyWakeIdentity
// derives from digest wake metadata — SenderID/Channel/ChatID/PeerKind, the
// exact values affiliate-backend's daily_digest wake sends — flow through
// the SAME context wiring the real agent loop uses (loop_context.go's
// injectContext for SenderID/SenderName; ExecuteWithContext's
// WithToolChannel/WithToolChatID/WithToolPeerKind for the rest) into
// injectSenderIdentity's output. This is what makes the digest agent's
// get_daily_digest / send_chat_message calls carry a non-empty sender_id
// instead of PERMISSION_DENIED.
//
// http.applyWakeIdentity itself is exercised directly (table tests) in
// internal/http/wake_identity_test.go; this test pins the OTHER half of the
// contract — that once RunRequest carries those fields, the existing,
// unmodified MCP identity-injection pipe honors them for a wake run exactly
// as it would for a live channel message.
func TestInjectSenderIdentity_WakeDigestIdentity(t *testing.T) {
	// Mirrors what applyWakeIdentity(&req, meta) sets on RunRequest for
	// affiliate-backend's digest wake metadata (sender_id=894385923,
	// channel=telegram, chat_id=894385923, chat_type=private_chat →
	// PeerKind="direct").
	const senderID = "894385923"
	const channel = "telegram"
	const chatID = "894385923"
	const peerKind = "direct"

	// Reproduce injectContext (loop_context.go:62-69) — only sets when non-empty.
	ctx := context.Background()
	if senderID != "" {
		ctx = store.WithSenderID(ctx, senderID)
	}
	// Reproduce ExecuteWithContext (registry.go) — channel/chatID/peerKind from RunRequest.
	ctx = tools.WithToolChannel(ctx, channel)
	ctx = tools.WithToolChatID(ctx, chatID)
	ctx = tools.WithToolPeerKind(ctx, peerKind)

	args := map[string]any{}
	injectSenderIdentity(ctx, args)

	if got := args["sender_id"]; got != senderID {
		t.Errorf("sender_id = %v, want %q — this is the exact field require_owner reads; "+
			"its absence was the Task 10 PERMISSION_DENIED root cause", got, senderID)
	}
	if got := args["channel"]; got != channel {
		t.Errorf("channel = %v, want %q", got, channel)
	}
	if got := args["chat_id"]; got != chatID {
		t.Errorf("chat_id = %v, want %q", got, chatID)
	}
	if got := args["chat_type"]; got != "private_chat" {
		t.Errorf("chat_type = %v, want private_chat (peerKind=direct → private_chat)", got)
	}
}

// TestInjectSenderIdentity_PlainWakeUnchanged pins the legacy (pre-Task-10b)
// wake shape: no SenderID ever injected into ctx (applyWakeIdentity is a
// no-op without metadata's sender_id) → sender_id absent from args, same
// PERMISSION_DENIED-causing shape as before this fix, for any wake caller
// that doesn't opt in to structured identity.
func TestInjectSenderIdentity_PlainWakeUnchanged(t *testing.T) {
	ctx := context.Background() // no SenderID set — applyWakeIdentity never ran
	ctx = tools.WithToolChannel(ctx, "wake")
	ctx = tools.WithToolChatID(ctx, "api")
	// PeerKind left unset (Go zero value ""), matching handleWake's untouched RunRequest.

	args := map[string]any{}
	injectSenderIdentity(ctx, args)

	if _, ok := args["sender_id"]; ok {
		t.Errorf("sender_id must be absent for a plain wake with no identity metadata, got %v", args["sender_id"])
	}
	if got := args["chat_type"]; got != "private_chat" {
		t.Errorf("chat_type = %v, want private_chat (empty peerKind defaults there, not group)", got)
	}
}
