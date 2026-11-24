package stream

import (
	"encoding/json"
	"sync"

	"github.com/vislake/speed/pkg/event"
)

// bufferDepth is how many frames one connection may hold unread before it
// starts dropping its own.
const bufferDepth = 64

const frameEventName = "message"

type frame struct {
	event string
	data  []byte
}

type scope struct {
	tenant    string
	recipient string
}

// Hub is the per-replica registry of live connections. It holds only this
// replica's connections, in memory; it stores nothing, guarantees no
// delivery and does not cross replicas.
type Hub struct {
	mu    sync.Mutex
	conns map[scope][]*connection
}

type connection struct {
	scope scope
	ch    chan frame
	done  chan struct{}

	// closeOnce makes Close idempotent: a connection may be closed by the
	// handler that owns it and again by the module's Stop, and closing a channel
	// twice panics.
	closeOnce sync.Once
}

// Subscribe registers a new connection under (tenant, recipient) and returns
// it. The connection's message buffer has bufferDepth slots; the caller reads
// frames out of ch and ends by calling Close.
func (h *Hub) Subscribe(tenant, recipient string) *connection {
	c := &connection{
		scope: scope{tenant: tenant, recipient: recipient},
		ch:    make(chan frame, bufferDepth),
		done:  make(chan struct{}),
	}
	h.mu.Lock()
	if h.conns == nil {
		h.conns = make(map[scope][]*connection)
	}
	h.conns[c.scope] = append(h.conns[c.scope], c)
	h.mu.Unlock()
	return c
}

// Publish delivers evt to every connection whose scope matches the event's
// (tenant_id, recipient_user_id).
func (h *Hub) Publish(evt event.Event) {
	fields, ok := event.PayloadFields(evt.Payload)
	if !ok {
		return
	}
	tenant, ok := event.PayloadString(fields, "tenant_id")
	if !ok {
		return
	}
	recipient, ok := event.PayloadString(fields, "recipient_user_id")
	if !ok {
		return
	}
	delete(fields, "tenant_id")
	data, err := json.Marshal(fields)
	if err != nil {
		return
	}
	f := frame{event: frameEventName, data: data}

	// Take the matched set out under the lock and deliver outside it, so a slow
	// reader's buffer never stretches a lock the registry's own add and remove
	// wait on.
	h.mu.Lock()
	targets := h.conns[scope{tenant: tenant, recipient: recipient}]
	matched := append([]*connection(nil), targets...)
	h.mu.Unlock()

	for _, c := range matched {
		select {
		case c.ch <- f:
		default:
		}
	}
}

// Close unregisters c and signals its reader to stop.
func (h *Hub) Close(c *connection) {
	if c == nil {
		return
	}
	h.mu.Lock()
	h.remove(c)
	h.mu.Unlock()
	c.closeOnce.Do(func() { close(c.done) })
}

// closeAll unregisters every connection and signals each reader to stop. It is
// what the module's Stop runs: after it the Hub is empty, and a late Publish
// finds no connection and does nothing.
func (h *Hub) closeAll() {
	h.mu.Lock()
	all := make([]*connection, 0)
	for _, list := range h.conns {
		all = append(all, list...)
	}
	h.conns = make(map[scope][]*connection)
	h.mu.Unlock()
	for _, c := range all {
		c.closeOnce.Do(func() { close(c.done) })
	}
}

func (h *Hub) remove(c *connection) {
	list := h.conns[c.scope]
	for i, existing := range list {
		if existing == c {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(h.conns, c.scope)
		return
	}
	h.conns[c.scope] = list
}
