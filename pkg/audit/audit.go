// Package audit declares the audit capability: the record of who did what,
// when, and to what.
//
// This is the interface package. It registers no module — what registers is the
// implementation subpackage, which delivers this capability exclusively — and
// it depends on the standard library alone.
//
// A module takes this capability up to hand over a fact none of the audit
// module's own capture paths can reach: the paths see statements, published
// events and requests, and a decision carried in a context is none of those.
// The purpose of a cross-tenant operation is the case this exists for, and only
// the call site knows it.
//
// Reading records back is not this capability. It needs an authorization
// decision, which belongs to the permission model, so it lives outside this
// module entirely.
package audit

import (
	"context"
	"errors"
)

// ErrWriteFailed reports that an entry reached no table. It is about the
// module's own state — its connection, its table, its constraints — and not
// about the entry, which was well formed enough to be accepted.
//
// It reaches two kinds of caller. Record returns it, and its caller may act on
// it. A model write returns it too, when the row recording that write could not
// be written: the statement-level record and the write are one unit of work, so
// the write fails with it. A write that fails on its own — a duplicate key, a
// refusal by another plugin — keeps its own error and does not carry this one.
//
// The request and event paths never return it. Those treat a failure as
// something to report and carry on from, because a request or an event must not
// fail on account of the record of it.
var ErrWriteFailed = errors.New("audit: the entry was not written")

// ErrNotAccepting reports that the module is not taking records: it has not
// started, or it has stopped. The capture paths this module drives drop a
// record in that state without a word, because there is nobody to tell; the
// seam says so, because its caller asked.
var ErrNotAccepting = errors.New("audit: the module is not accepting records")

// Audit is the capability the implementation delivers.
//
// It is also the token the request-level capture layer stands for on the entry
// point's middleware chain, which is how another layer states whether it
// belongs inside or outside audit.
type Audit interface {
	// Record takes over one entry.
	//
	// The time, the acting identity, the tenant and the capture path are filled
	// in by the module; e carries the rest. A nil return means the entry was
	// accepted and written.
	//
	// An entry is accepted even when the context carries no identity or no
	// tenant. System-level operations and exempt routes have neither, and those
	// are as much in need of a record as anything else.
	Record(ctx context.Context, e Entry) error
}

// Entry is the part of an audit row the caller supplies.
//
// The fields are free-form strings on purpose. What verb an action is written
// with, and how an object is spelled, are agreed between the call site and
// whoever reads the table later — the module compares none of them and defines
// no vocabulary.
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
	// value is masked on the way to the table; see the implementation package's
	// documentation for the set that is.
	//
	// The keys "on_behalf_of", "impersonation_scope" and "session" belong to the
	// journal. Under an impersonating identity it writes them over whatever the
	// caller put there, and removes "session" when the identity has none; on
	// every other entry, including one with no identity in the context, it
	// removes them from the caller's detail. A caller, including an event
	// payload field that becomes a detail key, must not use them.
	Detail map[string]string
}

// AccountObjectPrefix starts the object of an entry that names an account.
//
// An object is "type + identifier" throughout the table. An account's
// identifier is opaque, and a reader cannot tell it from a session's or an
// invitation's by its shape, so an entry whose object names an account states
// the type: the prefix, then the identifier unchanged.
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
