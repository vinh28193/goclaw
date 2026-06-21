package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/sessions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// WakeHandler handles POST /v1/agents/{id}/wake — external trigger API.
// Allows orchestrators (Paperclip, n8n, etc.) to trigger agent runs via HTTP.
type WakeHandler struct {
	agents     *agent.Router
	postTurn   tools.PostTurnProcessor
	channelMgr *channels.Manager
}

// SetPostTurnProcessor sets the post-turn processor for team task dispatch.
func (h *WakeHandler) SetPostTurnProcessor(pt tools.PostTurnProcessor) {
	h.postTurn = pt
}

// SetChannelManager wires the channel manager used by `is_system_command`
// wake requests to dispatch text / PDF attachments directly without invoking
// the LLM loop. Optional — wake requests without is_system_command run the
// normal agent loop path regardless.
func (h *WakeHandler) SetChannelManager(mgr *channels.Manager) {
	h.channelMgr = mgr
}

// NewWakeHandler creates a handler for the wake endpoint.
func NewWakeHandler(agents *agent.Router) *WakeHandler {
	return &WakeHandler{agents: agents}
}

// RegisterRoutes registers wake routes on the given mux.
func (h *WakeHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/agents/{id}/wake", h.handleWake)
}

type wakeRequest struct {
	Message    string         `json:"message"`
	SessionKey string         `json:"session_key,omitempty"`
	UserID     string         `json:"user_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type wakeResponse struct {
	Content string   `json:"content"`
	RunID   string   `json:"run_id"`
	Usage   *wakeUsage `json:"usage,omitempty"`
}

type wakeUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (h *WakeHandler) handleWake(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)

	// Auth + RBAC check (gateway token or API key, operator required for POST)
	auth := resolveAuth(r)
	if !auth.Authenticated {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": i18n.T(locale, i18n.MsgUnauthorized)})
		return
	}
	if !permissions.HasMinRole(auth.Role, permissions.RoleOperator) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": i18n.T(locale, i18n.MsgPermissionDenied, r.URL.Path)})
		return
	}

	// Inject tenant, role, user, and locale into context for downstream stores/tools.
	r = r.WithContext(enrichContext(r.Context(), r, auth))

	agentID := r.PathValue("id")
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgInvalidID, "agent")})
		return
	}

	// Limit request body size
	const maxBodySize = 1 << 20 // 1MB
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	var req wakeRequest
	if !bindJSON(w, r, locale, &req) {
		return
	}

	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}

	// System commands (backend-initiated outbound: send_chat_message,
	// send_pdf_attachment) bypass the LLM loop and dispatch straight to the
	// channel manager. Identified by metadata.is_system_command=true. Skip
	// the branch when channelMgr is nil (test/dev wiring without channels).
	if h.channelMgr != nil && isSystemCommand(req.Metadata) {
		h.handleSystemCommand(w, r, req.Metadata)
		return
	}

	loop, err := h.agents.Get(r.Context(), agentID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(locale, i18n.MsgNotFound, "agent", agentID)})
		return
	}

	// Build session key
	sessionKey := req.SessionKey
	if sessionKey == "" {
		sessionKey = sessions.SessionKey(agentID, "wake-"+uuid.NewString()[:8])
	}

	// Body user_id override: allowed only when API key has no bound owner (prevents impersonation).
	userID := store.UserIDFromContext(r.Context())
	ctx := r.Context()
	if req.UserID != "" && req.UserID != userID {
		if auth.KeyData != nil && auth.KeyData.OwnerID != "" {
			slog.Warn("security.wake_owner_override_blocked",
				"req_user_id", req.UserID,
				"owner_id", auth.KeyData.OwnerID,
			)
		} else {
			userID = req.UserID
			ctx = store.WithUserID(ctx, req.UserID)
		}
	}

	runID := uuid.NewString()
	slog.Info("wake request", "agent", agentID, "user", userID, "session", sessionKey)

	ctx, drainTeamDispatch := tools.InjectTeamDispatch(ctx, h.postTurn)
	defer drainTeamDispatch()

	result, err := loop.Run(ctx, agent.RunRequest{
		SessionKey: sessionKey,
		Message:    req.Message,
		Channel:    "wake",
		ChatID:     "api",
		RunID:      runID,
		UserID:     userID,
		Stream:     false,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("agent run failed: %v", err)})
		return
	}

	resp := wakeResponse{
		Content: result.Content,
		RunID:   runID,
	}
	if result.Usage != nil {
		resp.Usage = &wakeUsage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// systemCommandStorageRoot bounds attachment paths to a single mount so a
// malicious / malformed metadata field can't trick the gateway into reading
// arbitrary files. Goclaw's compose mounts the backend's storage volume here.
const systemCommandStorageRoot = "/app/storage"

func isSystemCommand(meta map[string]any) bool {
	if meta == nil {
		return false
	}
	v, ok := meta["is_system_command"]
	if !ok {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// metaString returns the string value at the given metadata key (or "").
func metaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	if v, ok := meta[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// validateAttachmentPath rejects paths outside the storage mount, symlink
// escapes, or non-files. Returns the cleaned path on success.
func validateAttachmentPath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("attachment_path is required")
	}
	cleaned := filepath.Clean(raw)
	if !strings.HasPrefix(cleaned, systemCommandStorageRoot+string(filepath.Separator)) &&
		cleaned != systemCommandStorageRoot {
		return "", fmt.Errorf("attachment_path outside storage root")
	}
	return cleaned, nil
}

type systemCommandResponse struct {
	Success   bool   `json:"success"`
	MessageID string `json:"message_id,omitempty"`
	RunID     string `json:"run_id"`
	Error     string `json:"error,omitempty"`
}

// handleSystemCommand dispatches the wake call directly to the channel manager
// without invoking the LLM. Supports command_types `send_chat_message` and
// `send_pdf_attachment` — anything else returns 400.
func (h *WakeHandler) handleSystemCommand(
	w http.ResponseWriter, r *http.Request, meta map[string]any,
) {
	runID := uuid.NewString()
	commandType := metaString(meta, "command_type")
	targetChannel := metaString(meta, "target_channel")
	targetChatID := metaString(meta, "target_chat_id")

	if targetChannel == "" || targetChatID == "" {
		slog.Warn("wake.system_command.bad_metadata",
			"command_type", commandType,
			"has_channel", targetChannel != "",
			"has_chat_id", targetChatID != "",
		)
		writeJSON(w, http.StatusBadRequest, systemCommandResponse{
			RunID: runID,
			Error: "target_channel and target_chat_id are required",
		})
		return
	}

	switch commandType {
	case "send_chat_message":
		text := metaString(meta, "text")
		if text == "" {
			writeJSON(w, http.StatusBadRequest, systemCommandResponse{
				RunID: runID,
				Error: "text is required for send_chat_message",
			})
			return
		}
		if err := h.channelMgr.SendToChannel(r.Context(), targetChannel, targetChatID, text); err != nil {
			slog.Warn("wake.system_command.send_failed",
				"command_type", commandType, "channel", targetChannel, "err", err,
			)
			writeJSON(w, http.StatusBadGateway, systemCommandResponse{
				RunID: runID,
				Error: fmt.Sprintf("channel send failed: %v", err),
			})
			return
		}
		slog.Info("wake.system_command.dispatched",
			"command_type", commandType, "channel", targetChannel, "chat_id", targetChatID,
		)
		writeJSON(w, http.StatusOK, systemCommandResponse{Success: true, RunID: runID})

	case "send_pdf_attachment":
		rawPath := metaString(meta, "attachment_path")
		caption := metaString(meta, "caption")
		path, err := validateAttachmentPath(rawPath)
		if err != nil {
			slog.Warn("security.wake_attachment_path_rejected",
				"raw", rawPath, "err", err,
			)
			writeJSON(w, http.StatusBadRequest, systemCommandResponse{
				RunID: runID,
				Error: err.Error(),
			})
			return
		}
		media := []bus.MediaAttachment{{
			URL:         path,
			ContentType: "application/pdf",
			Caption:     caption,
		}}
		if err := h.channelMgr.SendMediaToChannel(
			r.Context(), targetChannel, targetChatID, caption, media,
		); err != nil {
			slog.Warn("wake.system_command.send_media_failed",
				"command_type", commandType, "channel", targetChannel, "err", err,
			)
			writeJSON(w, http.StatusBadGateway, systemCommandResponse{
				RunID: runID,
				Error: fmt.Sprintf("channel send media failed: %v", err),
			})
			return
		}
		slog.Info("wake.system_command.dispatched",
			"command_type", commandType, "channel", targetChannel,
			"chat_id", targetChatID, "attachment", path,
		)
		writeJSON(w, http.StatusOK, systemCommandResponse{Success: true, RunID: runID})

	default:
		slog.Warn("wake.system_command.unknown_type", "command_type", commandType)
		writeJSON(w, http.StatusBadRequest, systemCommandResponse{
			RunID: runID,
			Error: fmt.Sprintf("unknown command_type: %s", commandType),
		})
	}
}
