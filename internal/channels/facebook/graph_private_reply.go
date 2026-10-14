package facebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// PrivateReplyResult is the (loosely parsed) Send API response of a private reply.
// Meta does not document the shape for private replies; both fields may be empty.
type PrivateReplyResult struct {
	RecipientID string `json:"recipient_id"` // PSID of the commenter, when returned
	MessageID   string `json:"message_id"`
}

// PrivateReply sends a Messenger private reply to the author of a comment:
// POST /{page-id}/messages with recipient.comment_id. Meta allows one private
// reply per comment, within 7 days of the comment's creation.
// See https://developers.facebook.com/docs/messenger-platform/discovery/private-replies/
func (g *GraphClient) PrivateReply(ctx context.Context, commentID, text string) (PrivateReplyResult, error) {
	var res PrivateReplyResult
	if err := validateFBID(commentID); err != nil {
		return res, fmt.Errorf("facebook: private reply: %w", err)
	}
	if err := validateFBID(g.pageID); err != nil {
		return res, fmt.Errorf("facebook: private reply: %w", err)
	}
	body := map[string]any{
		"recipient": map[string]string{"comment_id": commentID},
		"message":   map[string]string{"text": text},
	}
	data, err := g.doRequest(ctx, http.MethodPost, "/"+g.pageID+"/messages", body)
	if err != nil {
		return res, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &res); err != nil {
			// Delivered; only the response shape is unexpected. Not an error.
			slog.Debug("facebook: private reply response not parsed", "err", err)
		}
	}
	return res, nil
}

// Graph error codes relevant to private replies.
const (
	codeAlreadyReplied    = 10900 // "Activity already replied to" (third-party documented)
	codePrivacyBlocked    = 10903 // commenter privacy / commenter is a Page (third-party documented)
	codeRecipientNotAvail = 551
)

// IsAlreadyReplied reports whether Meta says the comment already has a private reply.
// Treated as success: the commenter already got their one message.
func IsAlreadyReplied(err error) bool {
	var ge *graphAPIError
	return errors.As(err, &ge) && ge.code == codeAlreadyReplied
}

// IsPrivateReplyRejected reports per-comment / per-recipient rejections that are
// permanent for this comment and say nothing about the channel's health.
func IsPrivateReplyRejected(err error) bool {
	var ge *graphAPIError
	if !errors.As(err, &ge) {
		return false
	}
	switch ge.code {
	case codePrivacyBlocked, codeRecipientNotAvail:
		return true
	case 100:
		return ge.subcode == 2534025
	case 10:
		return ge.subcode == 2018278 || ge.subcode == 2534022 || ge.subcode == 2018108
	case 200:
		return ge.subcode == 1545041
	}
	return false
}

// IsPrivateReplyAppLevel reports app/Page-level messaging permission problems
// (pages_messaging not approved, app lacks permission, Page temporarily blocked
// from messaging). These affect every private reply, so operators must see them.
func IsPrivateReplyAppLevel(err error) bool {
	var ge *graphAPIError
	if !errors.As(err, &ge) {
		return false
	}
	switch ge.code {
	case 200:
		return ge.subcode != 1545041
	case 10:
		return ge.subcode == 0 || ge.subcode == 1404170 || ge.subcode == 1893063
	}
	return false
}
