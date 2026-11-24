// Package outbox ties an event's publication to a database write: an event
// staged in a transaction is published once that transaction commits, and never
// when it rolls back.
package outbox

import (
	"errors"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/event"
)

// ErrNoTransaction is returned by Stage for a session that is not in a
// transaction: the handle itself, or a session derived from it outside
// Transaction or Begin.
var ErrNoTransaction = errors.New("outbox: the session is not in a transaction")

// ErrOtherDatabase is returned by Stage for a transaction on a connection pool
// other than the one the database capability delivered.
var ErrOtherDatabase = errors.New("outbox: the transaction is not on the delivered database")

// Outbox is the capability the relay subpackage delivers exclusively.
type Outbox interface {
	// Stage records evt in the transaction tx is in, to be published on the
	// bus once that transaction commits.
	Stage(tx *gorm.DB, evt event.Event) error
}
