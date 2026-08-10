package providers

import (
	"testing"
)

func TestBuildRequestBodyForwardsMetadata(t *testing.T) {
	p := &OpenAIProvider{name: "affiliate-brain", providerType: "openai_compat", forwardMetadata: true}
	req := ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Options: map[string]any{
			OptUserID:     "tg-111",
			OptChatID:     "tg-111",
			OptChannel:    "telegram",
			OptPeerKind:   "direct",
			OptSessionKey: "sess-1",
			OptSenderName: "Vinh",
		},
	}
	body := p.buildRequestBody("affiliate-brain", req, false)
	md, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing: %#v", body)
	}
	for k, want := range map[string]string{
		"sender_id": "tg-111", "chat_id": "tg-111", "channel": "telegram",
		"chat_type": "private_chat", "session_key": "sess-1", "sender_name": "Vinh",
	} {
		if md[k] != want {
			t.Errorf("metadata[%q] = %v, want %v", k, md[k], want)
		}
	}
}

func TestBuildRequestBodyGroupSenderBeatsSessionUser(t *testing.T) {
	// Group chats: OptUserID is the aggregated session user
	// ("group:telegram:-123") — the wire sender_id must be the individual
	// sender (OptSenderID), and peer_kind "group" maps to chat_type "group".
	p := &OpenAIProvider{name: "affiliate-brain", providerType: "openai_compat", forwardMetadata: true}
	req := ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Options: map[string]any{
			OptUserID:   "group:telegram:-1003915852870",
			OptSenderID: "894385923",
			OptChatID:   "-1003915852870",
			OptChannel:  "telegram",
			OptPeerKind: "group",
		},
	}
	body := p.buildRequestBody("affiliate-brain", req, false)
	md := body["metadata"].(map[string]any)
	if md["sender_id"] != "894385923" {
		t.Errorf("sender_id = %v, want individual sender 894385923", md["sender_id"])
	}
	if md["chat_type"] != "group" {
		t.Errorf("chat_type = %v, want group", md["chat_type"])
	}
}

func TestBuildRequestBodyNoMetadataWhenFlagOff(t *testing.T) {
	p := &OpenAIProvider{name: "other", providerType: "openai_compat", forwardMetadata: false}
	req := ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Options:  map[string]any{OptUserID: "u1", OptChatID: "c1"},
	}
	body := p.buildRequestBody("gpt-x", req, false)
	if _, exists := body["metadata"]; exists {
		t.Fatalf("metadata must not be sent when flag off")
	}
}
