package inbox

import (
	"time"

	"github.com/vislake/speed/pkg/notification"
)

// NotificationMessage is one message as the routes show it. It is built field by
// field from the root package's Message and never handed through as it stands,
// so a field added to Message later does not reach a client by being there. It
// carries no bookkeeping column — no tenant, recipient, dedupe key, expiry or
// update time — and every instant is written in UTC.
type NotificationMessage struct {
	_ struct{} `description:"One in-app message as the inbox shows it. The title and body arrive already rendered, and the bookkeeping columns — tenant, recipient, dedupe key, expiry and update time — are not part of this view."`

	ID        string         `json:"id" required:"true" description:"The message identifier; it is the value the per-message mark-read route takes."`
	TypeKey   string         `json:"type_key" required:"true" description:"The notification type key the message was dispatched under, shaped like <module>.<entity>.<action>."`
	Group     string         `json:"group" required:"true" description:"The group the message was filed under; the listing filters on it. Empty when the message was filed under none."`
	Title     string         `json:"title" required:"true" description:"The rendered title, in the language the delivery pipeline rendered it in."`
	Body      string         `json:"body" required:"true" description:"The rendered body, in the language the delivery pipeline rendered it in."`
	Params    map[string]any `json:"params" required:"true" description:"The type's parameters after the allow-list filter, as an opaque JSON object the client renders into the message; null when the message carries none."`
	Link      string         `json:"link" required:"true" description:"The optional navigation target; the empty string when the message carries none."`
	ReadAt    *time.Time     `json:"read_at" description:"The first-read instant as an RFC 3339 instant in UTC; null while the message is unread, and never rewritten once set."`
	CreatedAt time.Time      `json:"created_at" required:"true" description:"When the message was written, as an RFC 3339 instant in UTC."`
}

// messageViewOf builds the view of one message.
func messageViewOf(m notification.Message) NotificationMessage {
	return NotificationMessage{
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
