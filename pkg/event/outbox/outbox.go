// Package outbox declares the capability that ties an event's publication to
// a database write: an event staged in the transaction that makes the write is
// published once that transaction commits, and never when it rolls back.
//
// The event bus publishes apart from any transaction. A publisher that
// publishes after its write has committed loses the event when the bus refuses
// it, with the write standing and nothing left to repeat the publication; one
// that publishes before committing announces a write that may yet roll back.
// Staging closes that gap: the event is written into the caller's transaction,
// in a table of the delivered database, and a relay hands it to the bus after
// the commit, again and again until the bus accepts it.
//
// This is the interface package. It registers no module; the relay subpackage
// does, and delivers this capability exclusively. It imports GORM, because the
// caller's transaction is a GORM session, and the event package, because what is
// staged is an event — which is why it is a package of its own rather than part
// of the event package, whose root every publisher imports and which stays free
// of GORM.
package outbox

import (
	"errors"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/event"
)

// ErrNoTransaction is returned by Stage for a session that is not in a
// transaction: the handle itself, or a session derived from it outside
// Transaction or Begin.
//
// A staged row written outside a transaction commits on its own, at once, so
// the event would be published whether or not the write it announces ever
// happens — the failure this capability exists to remove, and the one a caller
// makes by handing Stage the handle it opened the transaction on rather than
// the transaction.
var ErrNoTransaction = errors.New("outbox: the session is not in a transaction")

// ErrOtherDatabase is returned by Stage for a transaction on a connection pool
// other than the one the database capability delivered: a connection built
// with the database module's Open, a handle a host opened itself, or another
// assembly's handle.
//
// The relay reads the table of the delivered database alone. A row staged
// anywhere else is either refused there, where no migration built the table,
// or kept in a table no relay of this assembly reads and never published.
var ErrOtherDatabase = errors.New("outbox: the transaction is not on the delivered database")

// Outbox is the capability the relay subpackage delivers exclusively. Declare
// the dependency as core.Requirement{Token: (*outbox.Outbox)(nil)} and take it
// up with core.Resolve[outbox.Outbox](reg).
//
// The promise is about the bus accepting the event, which is where the bus's
// own promise begins: an event whose transaction committed is published, at
// least once, by a relay of an assembly on the same database; one whose
// transaction rolled back is never published. Publication follows the commit
// rather than happening inside it, so a consumer hears of the write after the
// write is visible, and within the time the relay takes to reach the row —
// promptly on the replica that staged it, however steadily that replica goes
// on staging, and within the relay's polling interval on any other.
//
// At least once is the most a relay can promise: a relay that loses its claim
// on a row, or cannot remove a row the bus has accepted, leaves it to be
// published again. The event carries the same identifier each time, and a
// consumer that must act on an event once de-duplicates by it, as it does for
// every other redelivery the bus allows.
type Outbox interface {
	// Stage records evt in the transaction tx is in, to be published on the
	// bus once that transaction commits.
	//
	// tx is the session a GORM transaction hands its closure, or one Begin
	// returned, on the handle the database capability delivered; anything else
	// is refused with ErrNoTransaction or ErrOtherDatabase before anything is
	// written. The row is written on tx itself, under tx's context, so it
	// commits or rolls back with the caller's own statements and takes no
	// connection of its own.
	//
	// An empty ID is assigned here, in the form event.NewID mints, and the
	// staged event carries it to every subscriber; a caller that has to record
	// the identifier beside its write sets it first. The payload is serialized
	// here, and one that cannot be serialized is refused with the error
	// Publish would have given. A subscriber receives the payload in the shape
	// a transport delivers — the decoded structure, numbers kept as their
	// decimal text — on every bus, the in-process one included.
	//
	// A failure to write the row is returned with the database's error in its
	// chain, and on PostgreSQL it leaves the transaction unable to commit; the
	// caller rolls back, and the write and its announcement both do not happen.
	Stage(tx *gorm.DB, evt event.Event) error
}
