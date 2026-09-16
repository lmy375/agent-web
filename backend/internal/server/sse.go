package server

import (
	"net/http"
	"time"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

const keepaliveInterval = 15 * time.Second

// streamEvents is the SSE body: the hub's increments with keepalives, until the
// client leaves or a fatal error ends the stream. Nothing is replayed -- a
// reconnecting client opens this first, then fetches the thread detail and the
// transcript, and deduplicates by id.
func streamEvents(w http.ResponseWriter, r *http.Request, hub *chat.Hub, threadID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		protocol.WriteError(w, protocol.Errorf(protocol.CodeInternal, "streaming unsupported"))
		return
	}
	events, unsubscribe := hub.Subscribe(threadID)
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Nginx and friends buffer text/event-stream into uselessness otherwise.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// A proxy in front of this will often hold the headers back until the body
	// starts, and a client that never sees them never fires `open` -- which is
	// the signal the client resyncs on. One comment frame settles it.
	if _, err := w.Write([]byte(": open\n\n")); err != nil {
		return
	}
	flusher.Flush()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case event, open := <-events:
			if !open {
				return
			}
			if _, err := w.Write(protocol.SSEFrame(event)); err != nil {
				return
			}
			flusher.Flush()
			if failure, ok := event.(protocol.ErrorEvent); ok && failure.Fatal {
				return
			}
		}
	}
}
