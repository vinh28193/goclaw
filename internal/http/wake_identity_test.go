package http

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
)

// baseWakeRunRequest mirrors the placeholder RunRequest handleWake builds
// before applyWakeIdentity runs — same literal values as wake.go's
// loop.Run(ctx, agent.RunRequest{Channel: "wake", ChatID: "api", ...}).
func baseWakeRunRequest() agent.RunRequest {
	return agent.RunRequest{
		SessionKey: "sess-1",
		Message:    "hello",
		Channel:    "wake",
		ChatID:     "api",
		RunID:      "run-1",
		UserID:     "system",
		Stream:     false,
	}
}

func TestApplyWakeIdentity_NoMetadata_LegacyPlaceholdersUntouched(t *testing.T) {
	req := baseWakeRunRequest()
	applyWakeIdentity(&req, nil)

	if req.Channel != "wake" || req.ChatID != "api" {
		t.Errorf("placeholders changed with nil metadata: Channel=%q ChatID=%q", req.Channel, req.ChatID)
	}
	if req.SenderID != "" || req.SenderName != "" || req.PeerKind != "" {
		t.Errorf("identity fields must stay zero-value with nil metadata: SenderID=%q SenderName=%q PeerKind=%q",
			req.SenderID, req.SenderName, req.PeerKind)
	}
}

func TestApplyWakeIdentity_MetadataWithoutSenderID_LegacyPlaceholdersUntouched(t *testing.T) {
	// This is exactly the shape affiliate-backend's digest wake sent BEFORE
	// Task 10b (channel/chat_id present, no sender_id) — must reproduce the
	// pre-fix PERMISSION_DENIED-causing behavior exactly (no accidental
	// upgrade just because channel/chat_id happen to be present).
	req := baseWakeRunRequest()
	meta := map[string]any{
		"intent":            "daily_digest",
		"tenant_id":         float64(75),
		"chat_id":           "894385923",
		"channel":           "telegram",
		"is_system_command": false,
	}
	applyWakeIdentity(&req, meta)

	if req.Channel != "wake" || req.ChatID != "api" {
		t.Errorf("placeholders overridden despite missing sender_id: Channel=%q ChatID=%q", req.Channel, req.ChatID)
	}
	if req.SenderID != "" || req.PeerKind != "" {
		t.Errorf("identity fields must stay empty without sender_id: SenderID=%q PeerKind=%q", req.SenderID, req.PeerKind)
	}
}

func TestApplyWakeIdentity_DigestWakeIdentity_FoldedIn(t *testing.T) {
	// Exact shape of affiliate-backend's Task 10b digest wake metadata
	// (app/chat/tasks/daily_digest.py::_send_digest_async).
	req := baseWakeRunRequest()
	meta := map[string]any{
		"intent":            "daily_digest",
		"tenant_id":         float64(75),
		"chat_id":           "894385923",
		"channel":           "telegram",
		"chat_type":         "private_chat",
		"sender_id":         "894385923",
		"is_system_command": false,
	}
	applyWakeIdentity(&req, meta)

	if req.Channel != "telegram" {
		t.Errorf("Channel = %q, want telegram", req.Channel)
	}
	if req.ChatID != "894385923" {
		t.Errorf("ChatID = %q, want 894385923", req.ChatID)
	}
	if req.SenderID != "894385923" {
		t.Errorf("SenderID = %q, want 894385923", req.SenderID)
	}
	if req.PeerKind != "direct" {
		t.Errorf("PeerKind = %q, want direct (so bridge_identity resolves chat_type=private_chat)", req.PeerKind)
	}
	// Session key / message / run id / user id are handleWake's own concerns —
	// applyWakeIdentity must never touch them.
	if req.SessionKey != "sess-1" || req.Message != "hello" || req.RunID != "run-1" || req.UserID != "system" {
		t.Errorf("non-identity fields mutated: %+v", req)
	}
}

func TestApplyWakeIdentity_ChatTypeGroupOrSupergroup_MapsToGroupPeerKind(t *testing.T) {
	// "supergroup" must map to "group" the same way "group" does: the
	// telegram channel handler's own isGroup check treats them identically
	// (handlers.go: `isGroup := message.Chat.Type == "group" ||
	// message.Chat.Type == "supergroup"`), and there is no separate
	// "supergroup" PeerKind downstream.
	cases := []struct {
		chatType string
	}{
		{chatType: "group"},
		{chatType: "supergroup"},
	}
	for _, tc := range cases {
		t.Run(tc.chatType, func(t *testing.T) {
			req := baseWakeRunRequest()
			applyWakeIdentity(&req, map[string]any{
				"sender_id": "u1",
				"chat_type": tc.chatType,
			})

			if req.PeerKind != "group" {
				t.Errorf("PeerKind = %q, want group (chat_type=%s)", req.PeerKind, tc.chatType)
			}
		})
	}
}

func TestApplyWakeIdentity_SenderIDOnly_PlaceholdersSurviveIndividually(t *testing.T) {
	// sender_id present but channel/chat_id absent: those two placeholders
	// must survive individually (not blanked to "") since the caller didn't
	// say anything about them — only sender_id/sender_name/peer_kind get set.
	req := baseWakeRunRequest()
	applyWakeIdentity(&req, map[string]any{"sender_id": "u1"})

	if req.Channel != "wake" {
		t.Errorf("Channel = %q, want unchanged placeholder wake", req.Channel)
	}
	if req.ChatID != "api" {
		t.Errorf("ChatID = %q, want unchanged placeholder api", req.ChatID)
	}
	if req.SenderID != "u1" {
		t.Errorf("SenderID = %q, want u1", req.SenderID)
	}
	if req.PeerKind != "direct" {
		t.Errorf("PeerKind = %q, want direct (default when chat_type absent)", req.PeerKind)
	}
}

func TestApplyWakeIdentity_SenderNameOptional(t *testing.T) {
	req := baseWakeRunRequest()
	applyWakeIdentity(&req, map[string]any{
		"sender_id":   "u1",
		"sender_name": "Vinh",
	})

	if req.SenderName != "Vinh" {
		t.Errorf("SenderName = %q, want Vinh", req.SenderName)
	}
}

func TestApplyWakeIdentity_EmptySenderIDString_TreatedAsAbsent(t *testing.T) {
	// A wake caller that sends sender_id="" explicitly (vs. omitting the key)
	// must be treated identically to "no identity" — never produce a
	// half-applied RunRequest.
	req := baseWakeRunRequest()
	applyWakeIdentity(&req, map[string]any{
		"sender_id": "",
		"channel":   "telegram",
		"chat_id":   "894385923",
	})

	if req.Channel != "wake" || req.ChatID != "api" {
		t.Errorf("placeholders overridden despite empty sender_id: Channel=%q ChatID=%q", req.Channel, req.ChatID)
	}
}
