package providers

// The affiliate-brain backend (forward_metadata providers) answers only
// non-streaming JSON and rejects stream=true with 400. A channel with DM/group
// streaming or always-on reasoning bubbles calls ChatStream, so every message
// failed with a generic error. ChatStream must fall back to one Chat call.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIProvider_ForwardMetadata_ChatStreamUsesNonStreamRequest(t *testing.T) {
	var gotStream any = "unset"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotStream = body["stream"]
		if body["stream"] == true {
			http.Error(w, `{"error":{"message":"streaming is not supported"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"chào shop"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`))
	}))
	defer srv.Close()

	p := NewOpenAIProvider("affiliate-brain-75", "aff_fake", srv.URL, "affiliate-brain").
		WithForwardMetadata(true)

	var chunks []StreamChunk
	resp, err := p.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	}, func(c StreamChunk) { chunks = append(chunks, c) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if gotStream == true {
		t.Fatalf("request sent stream=true; want a non-stream request")
	}
	if resp.Content != "chào shop" {
		t.Errorf("Content = %q, want %q", resp.Content, "chào shop")
	}
	if len(chunks) != 2 || chunks[0].Content != "chào shop" || !chunks[1].Done {
		t.Errorf("chunks = %+v, want [content, done]", chunks)
	}
}
