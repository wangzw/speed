package inbox

import (
	"time"

	"github.com/vislake/speed/pkg/notification"
)

// messageView is one message as the routes show it. It is built field by field
// from the root package's Message and never handed through as it stands, so a
// field added to Message later does not reach a client by being there. It
// carries no bookkeeping column — no tenant, recipient, dedupe key, expiry or
// update time — and every instant is written in UTC.
type messageView struct {
	ID        string         `json:"id"`
	TypeKey   string         `json:"type_key"`
	Group     string         `json:"group"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Params    map[string]any `json:"params"`
	Link      string         `json:"link"`
	ReadAt    *time.Time     `json:"read_at"`
	CreatedAt time.Time      `json:"created_at"`
}

// messageViewOf builds the view of one message.
func messageViewOf(m notification.Message) messageView {
	return messageView{
		ID:        m.ID,
		TypeKey:   m.TypeKey,
		Group:     m.Group,
		Title:     m.Title,
		Body:      m.Body,
		Params:    m.Params,
		Link:      m.Link,
		ReadAt:    utcPtr(m.ReadAt),
		CreatedAt: m.CreatedAt.UTC(),
	}
}

// utcPtr writes an optional instant in UTC.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	at := t.UTC()
	return &at
}
