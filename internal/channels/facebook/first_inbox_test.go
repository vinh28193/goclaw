package facebook

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
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

// TestFirstInbox_Characterization_UsesSendAPIByFromID pins the pre-fix request
// shape: after the public comment reply, the first inbox DM went to
// POST /me/messages with recipient.id = webhook from.id.
func TestFirstInbox_Characterization_UsesSendAPIByFromID(t *testing.T) {
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
	if reqs[1].Method != http.MethodPost || reqs[1].Path != "/v25.0/me/messages" {
		t.Errorf("req[1] = %s %s, want POST /v25.0/me/messages", reqs[1].Method, reqs[1].Path)
	}
	if want := `{"message":{"text":"hi"},"recipient":{"id":"999"}}`; reqs[1].Body != want {
		t.Errorf("req[1] body = %s, want %s", reqs[1].Body, want)
	}
}
