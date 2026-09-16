package chat

import (
	"sync"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// subscriberQueue bounds one subscriber's backlog. A client this far behind is
// dropped with one frame saying so rather than buffered without bound; its
// reconnect resyncs through the thread detail and the transcript.
const subscriberQueue = 4096

// GlobalStream is the pseudo thread id of the directory stream, which carries
// only thread_updated and thread_deleted for every thread at once. The
// reference protocol has no such stream because its client is a desktop app
// with its own thread list; a browser sidebar would otherwise have to poll.
const GlobalStream = "*"

// Hub fans one event out to the subscribers of its thread and to the directory
// stream. Fan-out belongs to the shell, not to a backend, so all three kinds
// share this one.
type Hub struct {
	mu     sync.Mutex
	byID   map[string]map[*subscription]struct{}
	global map[*subscription]struct{}
}

type subscription struct {
	events chan protocol.ServerEvent
	closed bool
}

func NewHub() *Hub {
	return &Hub{byID: map[string]map[*subscription]struct{}{}, global: map[*subscription]struct{}{}}
}

func isDirectoryEvent(e protocol.ServerEvent) bool {
	switch protocol.EventType(e) {
	case "thread_updated", "thread_deleted":
		return true
	}
	return false
}

func (h *Hub) Publish(e protocol.ServerEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.byID[protocol.EventThreadID(e)] {
		h.deliverLocked(sub, e)
	}
	if isDirectoryEvent(e) {
		for sub := range h.global {
			h.deliverLocked(sub, e)
		}
	}
}

// deliverLocked hands the event over, or ends a stream that fell behind. The
// overflow frame replaces the event that would not fit, so the client always
// learns why its stream stopped.
func (h *Hub) deliverLocked(sub *subscription, e protocol.ServerEvent) {
	if sub.closed {
		return
	}
	select {
	case sub.events <- e:
	default:
		sub.events <- protocol.StreamError(protocol.EventThreadID(e), protocol.ErrStreamOverflow,
			"client fell behind the event buffer; reconnect", true)
		close(sub.events)
		sub.closed = true
	}
}

// Subscribe returns the stream for one thread, or for the directory when
// threadID is GlobalStream, and the function that ends it. Subscribing starts
// nothing: current state is GET /threads/{id} and history is GET /messages.
func (h *Hub) Subscribe(threadID string) (<-chan protocol.ServerEvent, func()) {
	// One slot over the bound so the overflow frame always fits.
	sub := &subscription{events: make(chan protocol.ServerEvent, subscriberQueue+1)}
	h.mu.Lock()
	if threadID == GlobalStream {
		h.global[sub] = struct{}{}
	} else {
		if h.byID[threadID] == nil {
			h.byID[threadID] = map[*subscription]struct{}{}
		}
		h.byID[threadID][sub] = struct{}{}
	}
	h.mu.Unlock()

	return sub.events, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if threadID == GlobalStream {
			delete(h.global, sub)
		} else if subs := h.byID[threadID]; subs != nil {
			delete(subs, sub)
			if len(subs) == 0 {
				delete(h.byID, threadID)
			}
		}
		if !sub.closed {
			close(sub.events)
			sub.closed = true
		}
	}
}
