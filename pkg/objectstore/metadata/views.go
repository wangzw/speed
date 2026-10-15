package metadata

import (
	"time"

	"github.com/vislake/speed/pkg/objectstore"
)

// objectView is one object as the routes show it. It is built field by field
// from the root package's Object and never handed through as it stands, so a
// field added to Object later does not reach a client by being there.
//
// It carries no internal key: the key is this module's private bookkeeping and
// reaches no API response. Every instant is written in UTC.
type objectView struct {
	ID        string     `json:"id"`
	State     string     `json:"state"`
	Size      int64      `json:"size"`
	MIME      string     `json:"mime"`
	Checksum  string     `json:"checksum_sha256"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// objectViewOf builds the view of one object.
func objectViewOf(o objectstore.Object) objectView {
	return objectView{
		ID:        o.ID,
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
