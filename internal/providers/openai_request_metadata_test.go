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
			OptPeerKind:   "private_chat",
			OptSessionKey: "sess-1",
		},
	}
	body := p.buildRequestBody("affiliate-brain", req, false)
	md, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing: %#v", body)
	}
	for k, want := range map[string]string{
		"sender_id": "tg-111", "chat_id": "tg-111", "channel": "telegram",
		"chat_type": "private_chat", "session_key": "sess-1",
	} {
		if md[k] != want {
			t.Errorf("metadata[%q] = %v, want %v", k, md[k], want)
		}
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
