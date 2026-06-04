package mcp

import (
	"context"

	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// injectSenderIdentity sets 5 identity fields into args from ctx.
// Server-trusted identity OVERRIDES any LLM-supplied values for the 5 reserved keys;
// empty source values cause the field to be ABSENT, never a LLM-supplied carryover.
//
// All 5 reserved keys are deleted from args BEFORE injection, so a LLM-supplied value
// is never preserved — even when the server context has no value for that field.
// MCP servers should accept unknown args (e.g. **kwargs in FastMCP); strict servers
// must whitelist these 5 keys in their input_schema.
//
// Fields injected (exact key names):
//
//	sender_id    - store.SenderIDFromContext(ctx)
//	sender_name  - store.SenderNameFromContext(ctx)
//	chat_id      - tools.ToolChatIDFromCtx(ctx)
//	chat_type    - "group" when peer kind is "group", otherwise "private_chat"
//	channel      - tools.ToolChannelFromCtx(ctx)
func injectSenderIdentity(ctx context.Context, args map[string]any) {
	// Pre-delete all 5 reserved keys so LLM-supplied values can never survive injection.
	// Empty source → field absent (not carried over from LLM).
	for _, k := range []string{"sender_id", "sender_name", "chat_id", "chat_type", "channel"} {
		delete(args, k)
	}

	set := func(k, v string) {
		if v != "" {
			args[k] = v
		}
	}

	set("sender_id", store.SenderIDFromContext(ctx))
	set("sender_name", store.SenderNameFromContext(ctx))
	set("chat_id", tools.ToolChatIDFromCtx(ctx))

	chatType := "private_chat"
	if tools.ToolPeerKindFromCtx(ctx) == "group" {
		chatType = "group"
	}
	set("chat_type", chatType) // chat_type always set (never empty)

	set("channel", tools.ToolChannelFromCtx(ctx))
}
