package stream

import (
	"encoding/json"
	"sync"

	"github.com/vislake/speed/pkg/event"
)

// bufferDepth is how many frames one connection may hold unread before it
// starts dropping its own. It is a fixed value of this module and not a
// configuration item: it bounds a single connection's tolerance, and a
// connection that falls further behind drops its own frames rather than
// stalling the publisher.
const bufferDepth = 64

// frameEventName is the SSE event name every frame carries. The name is fixed
// and part of this module's promise: a connected client parses on it, so
// changing it breaks every client already reading the stream.
const frameEventName = "message"

// frame is one SSE event waiting to be written: the event name and the JSON
// data that follows it.
type frame struct {
	event string
	data  []byte
}

// scope is the (tenant, recipient) pair a connection is subscribed under and a
// published event is matched by.
type scope struct {
	tenant    string
	recipient string
}

// Hub is the per-replica registry of live connections. It holds only this
// replica's connections, in memory; it stores nothing, guarantees no delivery
// and does not cross replicas.
//
// Subscribe, Publish and Close may be called concurrently from different
// goroutines. Adding and removing a connection happen under one mutex; Publish
// takes the matched set of connections out under that same mutex and delivers
// outside it, so delivery never holds a lock a slow reader could stretch.
//
// The zero value is a usable Hub holding no connections.
type Hub struct {
	mu    sync.Mutex
	conns map[scope][]*connection
}

// connection is one established stream. It carries the scope it is subscribed
// under, the message buffer Publish fills, and the close signal the reader
// selects on.
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
//
// It never blocks and never reports an error. An event that names no tenant or
// no recipient cannot be placed in any scope and is dropped; one whose payload
// cannot be read or serialized is dropped too. Each matched connection gets one
// non-blocking send, so a connection whose buffer is full drops its own frame
// and Publish moves on. Dropping is per-connection: one slow client never
// affects another, and never delays the publisher.
//
// Matching is done before any send is attempted: an event is looked up by its
// scope, so a connection outside that scope never takes a send at all.
//
// The frame carries the payload without its tenant identifier: that value is
// internal and is the scope the hub matched on, not something a browser is
// shown. The recipient's account identifier stays, because it is the account
// identifier the API uses everywhere.
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

// Close unregisters c and signals its reader to stop. It never closes the
// message buffer: closing a channel a concurrent Publish may be sending on is
// the very race that would panic a concurrent Hub, and the buffer is reclaimed
// with the connection instead. A send that arrives after the reader has gone
// either lands in the still-existing buffer or is dropped by the non-blocking
// branch, and neither panics.
//
// Close is idempotent and safe on a nil connection.
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

// remove drops c from the index. It is called with the mutex held.
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
