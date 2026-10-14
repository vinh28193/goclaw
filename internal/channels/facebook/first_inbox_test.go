package facebook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
)

type recordedReq struct {
	Method string
	Path   string
	Body   string
}

// graphRecorder is a fake Graph API that records every request and answers
// via a per-test responder (default: 200 with a generic body).
type graphRecorder struct {
	mu      sync.Mutex
	reqs    []recordedReq
	respond func(w http.ResponseWriter, r *http.Request, body string)
}

func (g *graphRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	g.mu.Lock()
	g.reqs = append(g.reqs, recordedReq{Method: r.Method, Path: r.URL.Path, Body: string(raw)})
	respond := g.respond
	g.mu.Unlock()
	if respond != nil {
		respond(w, r, string(raw))
		return
	}
	_, _ = w.Write([]byte(`{"id":"reply-1"}`))
}

func (g *graphRecorder) requests() []recordedReq {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]recordedReq(nil), g.reqs...)
}

func (g *graphRecorder) count(path string) int {
	n := 0
	for _, r := range g.requests() {
		if r.Path == path {
			n++
		}
	}
	return n
}

// newFirstInboxChannel builds a channel with comment reply + first inbox enabled,
// pointed at a recording fake Graph server, with fast retry backoff.
func newFirstInboxChannel(t *testing.T, rec *graphRecorder) *Channel {
	t.Helper()
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	swapGraphBase(t, srv.URL)
	saved := graphBackoffBase
	graphBackoffBase = time.Millisecond
	t.Cleanup(func() { graphBackoffBase = saved })

	cfg := facebookInstanceConfig{PageID: "111222333", FirstInboxMessage: "hi"}
	cfg.Features.CommentReply = true
	cfg.Features.FirstInbox = true
	ch, err := New(cfg, facebookCreds{PageAccessToken: "tok", AppSecret: "s", VerifyToken: "v"}, bus.New(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ch
}

func commentOutbound(commentID, senderID string) bus.OutboundMessage {
	return bus.OutboundMessage{
		ChatID:  "post-1:" + senderID,
		Content: "public reply",
		Metadata: map[string]string{
			"fb_mode":             "comment",
			"reply_to_comment_id": commentID,
			"sender_id":           senderID,
		},
	}
}

const privateReplyPath = "/v25.0/111222333/messages"

// respondPrivateReply answers comment replies with 200 and private replies via fn.
func respondPrivateReply(fn func(w http.ResponseWriter)) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path == privateReplyPath {
			fn(w)
			return
		}
		_, _ = w.Write([]byte(`{"id":"reply-1"}`))
	}
}

func graphErr(status int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// TestFirstInbox_UsesPrivateReplyByCommentID inverts the former characterization
// fixture: the DM is a private reply addressed by comment_id, never /me/messages
// with recipient.id = from.id.
func TestFirstInbox_UsesPrivateReplyByCommentID(t *testing.T) {
	rec := &graphRecorder{}
	ch := newFirstInboxChannel(t, rec)

	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	reqs := rec.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d (%+v), want 2", len(reqs), reqs)
	}
	if reqs[0].Method != http.MethodPost || reqs[0].Path != "/v25.0/111_222/comments" {
		t.Errorf("req[0] = %s %s, want POST /v25.0/111_222/comments", reqs[0].Method, reqs[0].Path)
	}
	if reqs[1].Method != http.MethodPost || reqs[1].Path != privateReplyPath {
		t.Errorf("req[1] = %s %s, want POST %s", reqs[1].Method, reqs[1].Path, privateReplyPath)
	}
	if want := `{"message":{"text":"hi"},"recipient":{"comment_id":"111_222"}}`; reqs[1].Body != want {
		t.Errorf("req[1] body = %s, want %s", reqs[1].Body, want)
	}
	if n := rec.count("/v25.0/me/messages"); n != 0 {
		t.Errorf("/me/messages calls = %d, want 0", n)
	}
}

func TestFirstInbox_DedupePerComment(t *testing.T) {
	rec := &graphRecorder{}
	ch := newFirstInboxChannel(t, rec)
	for range 2 { // block reply + final reply on the same comment
		if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	if n := rec.count("/v25.0/111_222/comments"); n != 2 {
		t.Errorf("comment replies = %d, want 2", n)
	}
	if n := rec.count(privateReplyPath); n != 1 {
		t.Errorf("private replies = %d, want 1", n)
	}
}

// Q1 (owner 261014): at most one DM per person per 24h, on top of one per comment.
func TestFirstInbox_DifferentCommentsSameSender(t *testing.T) {
	rec := &graphRecorder{}
	ch := newFirstInboxChannel(t, rec)
	for _, c := range []string{"111_222", "111_333"} {
		if err := ch.Send(t.Context(), commentOutbound(c, "999")); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	if n := rec.count(privateReplyPath); n != 1 {
		t.Errorf("private replies = %d, want 1 (same sender within 24h)", n)
	}
	// A different person still gets theirs.
	if err := ch.Send(t.Context(), commentOutbound("111_444", "888")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := rec.count(privateReplyPath); n != 2 {
		t.Errorf("private replies = %d, want 2 after another sender", n)
	}
}

func TestFirstInbox_SenderGateExpiresAfter24h(t *testing.T) {
	rec := &graphRecorder{}
	ch := newFirstInboxChannel(t, rec)
	ch.firstInboxSenderSeen.Store("999", time.Now().Add(-firstInboxSenderTTL-time.Minute))
	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := rec.count(privateReplyPath); n != 1 {
		t.Errorf("private replies = %d, want 1 (stale sender gate)", n)
	}
}

func TestFirstInbox_AlreadyRepliedIsSuccess(t *testing.T) {
	rec := &graphRecorder{respond: respondPrivateReply(graphErr(400, `{"error":{"code":10900,"message":"Activity already replied to"}}`))}
	ch := newFirstInboxChannel(t, rec)
	before := ch.HealthSnapshot().State

	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := ch.HealthSnapshot().State; got != before {
		t.Errorf("health = %s, want unchanged %s", got, before)
	}
	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := rec.count(privateReplyPath); n != 1 {
		t.Errorf("private replies = %d, want 1 (already replied is final)", n)
	}
}

func TestFirstInbox_RejectedDoesNotFailChannel(t *testing.T) {
	rec := &graphRecorder{respond: respondPrivateReply(graphErr(400, `{"error":{"code":10,"error_subcode":2018278,"message":"outside window"}}`))}
	ch := newFirstInboxChannel(t, rec)
	before := ch.HealthSnapshot().State

	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := ch.HealthSnapshot().State; got != before {
		t.Errorf("health = %s, want unchanged %s", got, before)
	}
	if _, kept := ch.firstInboxSent.Load("111_222"); !kept {
		t.Error("comment key should be kept after a permanent rejection")
	}
	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := rec.count(privateReplyPath); n != 1 {
		t.Errorf("private replies = %d, want 1 (no retry after rejection)", n)
	}
}

func TestFirstInbox_TransientFailureAllowsRetry(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	rec := &graphRecorder{respond: respondPrivateReply(func(w http.ResponseWriter) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"recipient_id":"psid-1","message_id":"m-1"}`))
	})}
	ch := newFirstInboxChannel(t, rec)

	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	first := rec.count(privateReplyPath)
	if first != maxRetries {
		t.Errorf("private reply attempts = %d, want %d (retried 5xx)", first, maxRetries)
	}
	if _, kept := ch.firstInboxSent.Load("111_222"); kept {
		t.Error("comment key should be released after a transient failure")
	}
	fail.Store(false)
	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := rec.count(privateReplyPath); n != first+1 {
		t.Errorf("private replies = %d, want %d (retry on next Send)", n, first+1)
	}
}

func TestFirstInbox_AuthErrorMarksChannel(t *testing.T) {
	rec := &graphRecorder{respond: respondPrivateReply(graphErr(400, `{"error":{"code":190,"message":"token expired"}}`))}
	ch := newFirstInboxChannel(t, rec)

	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h := ch.HealthSnapshot()
	if h.State != channels.ChannelHealthStateFailed || h.FailureKind != channels.ChannelFailureKindAuth {
		t.Errorf("health = %s/%s, want failed/auth", h.State, h.FailureKind)
	}
	if _, kept := ch.firstInboxSent.Load("111_222"); kept {
		t.Error("comment key should be released after auth error")
	}
}

func TestFirstInbox_RateLimitDegrades(t *testing.T) {
	rec := &graphRecorder{respond: respondPrivateReply(graphErr(400, `{"error":{"code":4,"message":"rate limited"}}`))}
	ch := newFirstInboxChannel(t, rec)

	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := ch.HealthSnapshot().State; got != channels.ChannelHealthStateDegraded {
		t.Errorf("health = %s, want degraded", got)
	}
	if _, kept := ch.firstInboxSent.Load("111_222"); kept {
		t.Error("comment key should be released after rate limit")
	}
}

// Q4 (owner 261014): app/Page-level messaging errors degrade the channel so
// operators see them, but never Fail it (public comment replies keep working).
func TestFirstInbox_AppLevelPermissionDegrades(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":200,"message":"pages_messaging not approved"}}`,
		`{"error":{"code":10,"message":"Application does not have permission"}}`,
		`{"error":{"code":10,"error_subcode":1893063,"message":"page blocked"}}`,
	} {
		rec := &graphRecorder{respond: respondPrivateReply(graphErr(400, body))}
		ch := newFirstInboxChannel(t, rec)

		if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
			t.Fatalf("Send: %v", err)
		}
		h := ch.HealthSnapshot()
		if h.State != channels.ChannelHealthStateDegraded || h.FailureKind != channels.ChannelFailureKindAuth || !h.Retryable {
			t.Errorf("%s: health = %s/%s retryable=%v, want degraded/auth retryable", body, h.State, h.FailureKind, h.Retryable)
		}
		if _, kept := ch.firstInboxSent.Load("111_222"); kept {
			t.Errorf("%s: comment key should be released", body)
		}
	}
}

func TestFirstInbox_PublicReplyFailsNoDM(t *testing.T) {
	rec := &graphRecorder{respond: func(w http.ResponseWriter, r *http.Request, _ string) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":100,"message":"bad comment"}}`))
	}}
	ch := newFirstInboxChannel(t, rec)
	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err == nil {
		t.Fatal("expected error from failed public reply")
	}
	if n := rec.count(privateReplyPath); n != 0 {
		t.Errorf("private replies = %d, want 0", n)
	}
}

func TestFirstInbox_MissingCommentIDSkips(t *testing.T) {
	rec := &graphRecorder{}
	ch := newFirstInboxChannel(t, rec)
	msg := commentOutbound("", "999")
	delete(msg.Metadata, "reply_to_comment_id")
	if err := ch.Send(t.Context(), msg); err == nil {
		t.Fatal("expected error for missing reply_to_comment_id")
	}
	if n := len(rec.requests()); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

func TestFirstInbox_DisabledSendsNothing(t *testing.T) {
	rec := &graphRecorder{}
	ch := newFirstInboxChannel(t, rec)
	ch.config.Features.FirstInbox = false
	if err := ch.Send(t.Context(), commentOutbound("111_222", "999")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := rec.count(privateReplyPath); n != 0 {
		t.Errorf("private replies = %d, want 0", n)
	}
}

func TestEvictStale_FirstInboxMaps(t *testing.T) {
	ch := &Channel{}
	now := time.Now()
	ch.firstInboxSent.Store("old", now.Add(-privateReplyWindow-time.Hour))
	ch.firstInboxSent.Store("new", now.Add(-time.Hour))
	ch.firstInboxSenderSeen.Store("old", now.Add(-firstInboxSenderTTL-time.Minute))
	ch.firstInboxSenderSeen.Store("new", now.Add(-time.Hour))

	ch.evictStale(now)

	if _, ok := ch.firstInboxSent.Load("old"); ok {
		t.Error("firstInboxSent: stale comment not evicted")
	}
	if _, ok := ch.firstInboxSent.Load("new"); !ok {
		t.Error("firstInboxSent: fresh comment evicted")
	}
	if _, ok := ch.firstInboxSenderSeen.Load("old"); ok {
		t.Error("firstInboxSenderSeen: stale sender not evicted")
	}
	if _, ok := ch.firstInboxSenderSeen.Load("new"); !ok {
		t.Error("firstInboxSenderSeen: fresh sender evicted")
	}
}

func consumeInboundMeta(t *testing.T, ch *Channel) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	msg, ok := ch.Bus().ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound message published")
	}
	return msg.Metadata
}

func TestHandleCommentEvent_CommentCreatedTimeMetadata(t *testing.T) {
	cfg := facebookInstanceConfig{}
	cfg.Features.CommentReply = true
	ch := newTestChannel(t, "111", cfg)

	ch.handleCommentEvent(WebhookEntry{ID: "111"}, ChangeValue{
		Item: "comment", Verb: "add", CommentID: "1_1", PostID: "1",
		From: FBUser{ID: "999"}, Message: "hello", CreatedTime: 1700000000,
	})
	if got := consumeInboundMeta(t, ch)["comment_created_time"]; got != "1700000000" {
		t.Errorf("comment_created_time = %q, want 1700000000", got)
	}

	ch.handleCommentEvent(WebhookEntry{ID: "111"}, ChangeValue{
		Item: "comment", Verb: "add", CommentID: "1_2", PostID: "1",
		From: FBUser{ID: "999"}, Message: "hello",
	})
	if v, ok := consumeInboundMeta(t, ch)["comment_created_time"]; ok {
		t.Errorf("comment_created_time = %q, want absent when CreatedTime=0", v)
	}
}

func TestFirstInbox_SkipsOlderThan7Days(t *testing.T) {
	rec := &graphRecorder{}
	ch := newFirstInboxChannel(t, rec)
	msg := commentOutbound("111_222", "999")
	msg.Metadata["comment_created_time"] = strconv.FormatInt(time.Now().Add(-8*24*time.Hour).Unix(), 10)

	if err := ch.Send(t.Context(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := rec.count("/v25.0/111_222/comments"); n != 1 {
		t.Errorf("comment replies = %d, want 1 (public reply still sent)", n)
	}
	if n := rec.count(privateReplyPath); n != 0 {
		t.Errorf("private replies = %d, want 0 (outside 7-day window)", n)
	}
}

func TestFirstInbox_RecentCreatedTimeSends(t *testing.T) {
	rec := &graphRecorder{}
	ch := newFirstInboxChannel(t, rec)
	msg := commentOutbound("111_222", "999")
	msg.Metadata["comment_created_time"] = strconv.FormatInt(time.Now().Add(-6*24*time.Hour).Unix(), 10)

	if err := ch.Send(t.Context(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := rec.count(privateReplyPath); n != 1 {
		t.Errorf("private replies = %d, want 1 (inside 7-day window)", n)
	}
}

func TestFirstInbox_UnknownCreatedTimeStillSends(t *testing.T) {
	for _, v := range []string{"", "not-a-number"} {
		rec := &graphRecorder{}
		ch := newFirstInboxChannel(t, rec)
		msg := commentOutbound("111_222", "999")
		if v != "" {
			msg.Metadata["comment_created_time"] = v
		}
		if err := ch.Send(t.Context(), msg); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if n := rec.count(privateReplyPath); n != 1 {
			t.Errorf("created_time %q: private replies = %d, want 1 (let Meta decide)", v, n)
		}
	}
}
