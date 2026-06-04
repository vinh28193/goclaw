package mcp

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// buildCtx is a helper that wires all 5 identity sources into a context.
func buildCtx(senderID, senderName, chatID, peerKind, channel string) context.Context {
	ctx := context.Background()
	if senderID != "" {
		ctx = store.WithSenderID(ctx, senderID)
	}
	if senderName != "" {
		ctx = store.WithSenderName(ctx, senderName)
	}
	if chatID != "" {
		ctx = tools.WithToolChatID(ctx, chatID)
	}
	ctx = tools.WithToolPeerKind(ctx, peerKind)
	if channel != "" {
		ctx = tools.WithToolChannel(ctx, channel)
	}
	return ctx
}

func TestInjectSenderIdentity_AllFields(t *testing.T) {
	ctx := buildCtx("uid-123", "Alice", "chat-456", "direct", "telegram")
	args := map[string]any{}
	injectSenderIdentity(ctx, args)

	cases := []struct{ key, want string }{
		{"sender_id", "uid-123"},
		{"sender_name", "Alice"},
		{"chat_id", "chat-456"},
		{"chat_type", "private_chat"}, // peerKind=direct → private_chat
		{"channel", "telegram"},
	}
	for _, c := range cases {
		got, ok := args[c.key].(string)
		if !ok || got != c.want {
			t.Errorf("args[%q] = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestInjectSenderIdentity_PeerKindMapping(t *testing.T) {
	tests := []struct {
		peerKind string
		wantType string
	}{
		{"group", "group"},
		{"direct", "private_chat"},
		{"", "private_chat"},   // empty defaults to private_chat
		{"unknown", "private_chat"}, // any non-group → private_chat
	}
	for _, tt := range tests {
		ctx := buildCtx("id1", "Name", "cid", tt.peerKind, "tg")
		args := map[string]any{}
		injectSenderIdentity(ctx, args)
		got, _ := args["chat_type"].(string)
		if got != tt.wantType {
			t.Errorf("peerKind=%q: chat_type=%q, want %q", tt.peerKind, got, tt.wantType)
		}
	}
}

func TestInjectSenderIdentity_EmptySourceSkipped(t *testing.T) {
	// Build ctx with sender_name empty (not set), everything else populated.
	ctx := context.Background()
	ctx = store.WithSenderID(ctx, "real-id")
	// sender_name NOT set → empty → should be skipped
	ctx = tools.WithToolChatID(ctx, "chat-999")
	ctx = tools.WithToolPeerKind(ctx, "direct")
	ctx = tools.WithToolChannel(ctx, "feishu")

	args := map[string]any{}
	injectSenderIdentity(ctx, args)

	// sender_name must not appear (or be "") — impl skips empty source
	if v, exists := args["sender_name"]; exists && v != "" {
		t.Errorf("sender_name should be absent or empty when source is empty, got %q", v)
	}

	// Other fields must still be set
	if args["sender_id"] != "real-id" {
		t.Errorf("sender_id: want real-id, got %v", args["sender_id"])
	}
	if args["chat_id"] != "chat-999" {
		t.Errorf("chat_id: want chat-999, got %v", args["chat_id"])
	}
	if args["channel"] != "feishu" {
		t.Errorf("channel: want feishu, got %v", args["channel"])
	}
}

func TestInjectSenderIdentity_OverrideSemantics(t *testing.T) {
	// Pre-populate args with LLM-supplied (fake) values.
	args := map[string]any{
		"sender_id":   "LLM_FAKE",
		"sender_name": "LLM_NAME",
		"chat_id":     "LLM_CHAT",
		"chat_type":   "LLM_TYPE",
		"channel":     "LLM_CHANNEL",
	}
	ctx := buildCtx("REAL_ID", "REAL_NAME", "REAL_CHAT", "group", "REAL_CHANNEL")
	injectSenderIdentity(ctx, args)

	// Server-trusted values must have overwritten LLM-supplied values.
	if args["sender_id"] != "REAL_ID" {
		t.Errorf("sender_id not overwritten: got %v", args["sender_id"])
	}
	if args["sender_name"] != "REAL_NAME" {
		t.Errorf("sender_name not overwritten: got %v", args["sender_name"])
	}
	if args["chat_id"] != "REAL_CHAT" {
		t.Errorf("chat_id not overwritten: got %v", args["chat_id"])
	}
	if args["chat_type"] != "group" {
		t.Errorf("chat_type not overwritten: got %v", args["chat_type"])
	}
	if args["channel"] != "REAL_CHANNEL" {
		t.Errorf("channel not overwritten: got %v", args["channel"])
	}
}

func TestInjectSenderIdentity_EmptyCtxEverywhere(t *testing.T) {
	// No identity values in ctx at all (bare background context).
	args := map[string]any{}
	injectSenderIdentity(context.Background(), args)

	// All sources empty → only chat_type injected (defaults to private_chat, never skipped).
	if v, ok := args["chat_type"].(string); !ok || v != "private_chat" {
		t.Errorf("chat_type: want private_chat, got %v", args["chat_type"])
	}

	emptyFields := []string{"sender_id", "sender_name", "chat_id", "channel"}
	for _, k := range emptyFields {
		if v, exists := args[k]; exists && v != "" {
			t.Errorf("field %q should be absent when source empty, got %v", k, v)
		}
	}
}

func TestInjectSenderIdentity_EmptySourceDeletesPreexisting(t *testing.T) {
	// If ctx has NO sender_id but args already has an LLM-supplied value,
	// the pre-delete step must remove it so the LLM value is never preserved.
	args := map[string]any{"sender_id": "LLM_FAKE"}
	ctx := context.Background() // no sender_id in ctx
	ctx = store.WithSenderName(ctx, "Someone")
	ctx = tools.WithToolChatID(ctx, "cid")
	ctx = tools.WithToolPeerKind(ctx, "direct")
	ctx = tools.WithToolChannel(ctx, "zalo")

	injectSenderIdentity(ctx, args)

	// sender_id source is empty → field must be ABSENT (not LLM-supplied carryover).
	_, ok := args["sender_id"]
	if ok {
		t.Errorf("sender_id must be absent when ctx source is empty, got %v", args["sender_id"])
	}
}
