package cmd

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// fallbackStubAgent records CallTool so tests can tell whether the offline
// URL fallback reached the MCP tools. Embedding the interface leaves every
// other method nil; the fallback only calls Provider and CallTool.
type fallbackStubAgent struct {
	agent.Agent
	provider providers.Provider
	calls    []string
}

func (s *fallbackStubAgent) Provider() providers.Provider { return s.provider }

func (s *fallbackStubAgent) CallTool(_ context.Context, name string, _ map[string]any) (*tools.Result, bool) {
	s.calls = append(s.calls, name)
	return nil, false
}

// When the brain runs in the backend (forward_metadata provider), a goclaw-side
// timeout often means the backend turn already finished: running the MCP tools
// again would create a second shortlink. The backend owns retry and fallback.
func TestComposeOfflineURLReply_SkipsBackendBrainAgents(t *testing.T) {
	stub := &fallbackStubAgent{
		provider: providers.NewOpenAIProvider("affiliate-brain-75", "aff_x", "http://be", "affiliate-brain").
			WithForwardMetadata(true),
	}
	if got := composeOfflineURLReply(context.Background(), stub, "https://shopee.vn/p"); got != "" {
		t.Fatalf("reply = %q, want empty", got)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("MCP tools called %v, want none", stub.calls)
	}
}

func TestComposeOfflineURLReply_StillRunsForGoclawBrainAgents(t *testing.T) {
	stub := &fallbackStubAgent{
		provider: providers.NewOpenAIProvider("openrouter", "k", "http://llm", "m"),
	}
	_ = composeOfflineURLReply(context.Background(), stub, "https://shopee.vn/p")
	if len(stub.calls) == 0 {
		t.Fatalf("expected the fallback to call detect_url")
	}
}
