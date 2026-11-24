// Package audit declares the audit capability: the record of who did
// what, when, and to what.
package audit

import (
	"context"
	"errors"
)

// ErrWriteFailed reports that an entry reached no table. It is about the
// module's own state — its connection, its table, its constraints —
// and not about the entry, which was well formed enough to be accepted.
var ErrWriteFailed = errors.New("audit: the entry was not written")

// ErrNotAccepting reports that the module is not taking records: it has
// not started, or it has stopped.
var ErrNotAccepting = errors.New("audit: the module is not accepting records")

// Audit is the capability the implementation delivers.
type Audit interface {
	// Record takes over one entry.
	Record(ctx context.Context, e Entry) error
}

// Entry is the part of an audit row the caller supplies.
type Entry struct {
	// Action is what was done, a verb.
	Action string

	// Object is the type of thing it was done to, with its identifier.
	Object string

	// Outcome is how it ended.
	Outcome string

	// Purpose is why it was done. An operation that needs a stated reason —
	// crossing tenants, most of all — gives it here, because the record is the
	// only place it is carried.
	Purpose string

	// Detail is the entry's key-value detail. A key name decides whether its
	// value is masked on the way to the table; see the implementation
	// package's documentation for the set that is.
	Detail map[string]string
}

// AccountObjectPrefix starts the object of an entry that names an
// account.
const AccountObjectPrefix = "account:"

// AccountObject returns the object that names the account id: the account type
// marker followed by the identifier. An empty id yields no object at all --
// an entry that names no account carries an empty object, not a bare marker.
func AccountObject(id string) string {
	if id == "" {
		return ""
	}
	return AccountObjectPrefix + id
}

// Source names the capture path an entry came through.
type Source string

const (
	// SourceStatement is the database plugin: a write that went through a model.
	SourceStatement Source = "statement"
	// SourceEvent is a subscription on the event bus.
	SourceEvent Source = "event"
	// SourceRequest is the entry point's middleware layer.
	SourceRequest Source = "request"
	// SourceSeam is a call to Record.
	SourceSeam Source = "seam"
)
