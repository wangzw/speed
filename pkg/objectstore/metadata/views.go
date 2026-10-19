package metadata

import (
	"time"

	"github.com/vislake/speed/pkg/objectstore"
)

// ObjectstoreObject is one object as the routes show it. It is built field by
// field from the root package's Object and never handed through as it stands, so
// a field added to Object later does not reach a client by being there.
//
// It carries no internal key: the key is this module's private bookkeeping and
// reaches no API response. Every instant is written in UTC.
//
// The type is exported and named as it is because the generated fragment
// publishes it under this name; its description tags are the contract's text.
type ObjectstoreObject struct {
	_ struct{} `description:"One object as the routes expose it: the values observed by the last upload plus the row's timestamps. It never carries the internal storage key, and every instant is UTC."`

	ID        string     `json:"id" required:"true" description:"The object's identifier, minted at declaration; every other object route takes it in the path."`
	Name      string     `json:"name" required:"true" description:"The declared label, normalized once when the object was created; empty when none was declared or it normalized to nothing"`
	State     string     `json:"state" required:"true" enum:"pending,uploaded,completed,deleted" description:"The lifecycle state: pending after declaration, uploaded once bytes are stored, completed after the completion call, deleted once tombstoned. A deleted object reads as 404 on every route except delete."`
	Size      int64      `json:"size" required:"true" description:"The observed byte count recorded by the last upload; zero until the first upload lands."`
	MIME      string     `json:"mime" required:"true" description:"The content type recorded by the last upload, taken from the declared content_type; empty until the first upload lands."`
	Checksum  string     `json:"checksum_sha256" required:"true" description:"The SHA-256 of the stored bytes as 64 lowercase hex digits, computed during the last upload; empty until the first upload lands."`
	ExpiresAt *time.Time `json:"expires_at,omitempty" description:"When a completed object will be swept into deleted, written from object-ttl at completion; absent when no lifetime is configured, so the object does not expire."`
	CreatedAt time.Time  `json:"created_at" required:"true" description:"When the object was declared, as an RFC 3339 instant in UTC."`
	UpdatedAt time.Time  `json:"updated_at" required:"true" description:"When the row last changed, whether by declaration, upload, completion or deletion, as an RFC 3339 instant in UTC."`
}

// objectViewOf builds the view of one object.
func objectViewOf(o objectstore.Object) ObjectstoreObject {
	return ObjectstoreObject{
		ID:        o.ID,
		Name:      o.Name,
		State:     string(o.State),
		Size:      o.Size,
		MIME:      o.MIME,
		Checksum:  o.Checksum,
		ExpiresAt: utcPtr(o.ExpiresAt),
		CreatedAt: o.CreatedAt.UTC(),
		UpdatedAt: o.UpdatedAt.UTC(),
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
