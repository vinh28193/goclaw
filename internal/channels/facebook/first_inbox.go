package facebook

import (
	"context"
	"log/slog"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
)

const (
	// privateReplyWindow is Meta's limit for sending a private reply after the
	// comment was created; also the TTL of the per-comment dedupe key.
	privateReplyWindow = 7 * 24 * time.Hour
	// firstInboxSenderTTL caps first-inbox DMs to one per person per window
	// (in-memory soft gate; Meta's per-comment limit is the durable one).
	firstInboxSenderTTL = 24 * time.Hour

	defaultFirstInboxMsg = "Cảm ơn bạn đã comment! Mình có thể hỗ trợ thêm qua tin nhắn riêng."
)

// sendFirstInbox sends a Messenger private reply to the author of commentID
// after the public comment reply (best-effort, never returns an error).
//
// Dedupe: one per comment (firstInboxSent, also enforced by Meta: a second
// attempt returns 10900) and one per sender per 24h (firstInboxSenderSeen).
// Per-recipient rejections never touch channel health; app/Page-level
// messaging errors degrade it; auth and rate limits go through handleAPIError.
func (ch *Channel) sendFirstInbox(ctx context.Context, commentID, senderID string, commentCreated int64) {
	now := time.Now()
	if commentCreated > 0 && now.Sub(time.Unix(commentCreated, 0)) > privateReplyWindow {
		slog.Info("facebook: first inbox skipped: comment older than 7 days",
			"comment_id", commentID, "created_time", commentCreated)
		return
	}
	if _, sent := ch.firstInboxSent.LoadOrStore(commentID, now); sent {
		return
	}
	if senderID != "" && !ch.claimFirstInboxSender(senderID, now) {
		slog.Info("facebook: first inbox skipped: sender already messaged within 24h",
			"comment_id", commentID, "sender_id", senderID)
		return
	}

	message := ch.config.FirstInboxMessage
	if message == "" {
		message = defaultFirstInboxMsg
	}

	// Pre-arm echo suppression for the commenter (works when from.id == PSID);
	// the real PSID is only known from the response.
	if senderID != "" {
		ch.botSentAt.Store(senderID, now)
	}

	res, err := ch.graphClient.PrivateReply(ctx, commentID, message)
	if err == nil {
		if res.RecipientID != "" {
			ch.botSentAt.Store(res.RecipientID, time.Now())
		}
		slog.Info("facebook: first inbox private reply sent",
			"comment_id", commentID, "sender_id", senderID, "recipient_id", res.RecipientID)
		return
	}

	if IsAlreadyReplied(err) {
		slog.Info("facebook: first inbox already sent for comment", "comment_id", commentID)
		return
	}

	// Every path below delivered nothing: release the sender gate and the echo stamp.
	if senderID != "" {
		ch.firstInboxSenderSeen.CompareAndDelete(senderID, now)
		ch.botSentAt.CompareAndDelete(senderID, now)
	}

	switch {
	case IsPrivateReplyRejected(err):
		// Permanent for this comment: keep the comment key, health untouched.
		slog.Info("facebook: first inbox rejected for comment",
			"comment_id", commentID, "sender_id", senderID, "err", err)
		return
	case IsPrivateReplyAppLevel(err):
		ch.MarkDegraded("private reply not permitted", err.Error(), channels.ChannelFailureKindAuth, true)
	case IsAuthError(err), IsRateLimitError(err):
		ch.handleAPIError(err)
	default:
		slog.Warn("facebook: first inbox send failed",
			"comment_id", commentID, "sender_id", senderID, "err", err)
	}
	ch.firstInboxSent.Delete(commentID) // allow retry on a later Send
}

// claimFirstInboxSender marks senderID as messaged at now unless it already was
// within firstInboxSenderTTL. Returns false when the sender is still gated.
func (ch *Channel) claimFirstInboxSender(senderID string, now time.Time) bool {
	ch.firstInboxMu.Lock()
	defer ch.firstInboxMu.Unlock()
	if v, ok := ch.firstInboxSenderSeen.Load(senderID); ok {
		if t, ok := v.(time.Time); ok && now.Sub(t) < firstInboxSenderTTL {
			return false
		}
	}
	ch.firstInboxSenderSeen.Store(senderID, now)
	return true
}
