package agent

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// Agent is the core abstraction for an AI agent execution loop.
// Implemented by *Loop; extracted as an interface for testability and composability.
type Agent interface {
	ID() string
	UUID() uuid.UUID
	OtherConfig() json.RawMessage
	Run(ctx context.Context, req RunRequest) (*RunResult, error)
	IsRunning() bool
	Model() string
	ProviderName() string
	Provider() providers.Provider

	// CallTool dispatches a registered tool by name outside the LLM loop.
	// Used by gateway-level fallback paths when the LLM is unavailable.
	// Returns (nil, false) when the tool isn't registered.
	CallTool(ctx context.Context, name string, args map[string]any) (*tools.Result, bool)
}
