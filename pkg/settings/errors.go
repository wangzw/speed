package settings

import "errors"

// The sentinel errors this module raises for the failures it judges itself.
// The startup ones abort the startup; the write-path ones reach the caller of
// Set or Delete. Callers tell the classes apart with errors.Is, and every wrap
// uses %w.
//
// A failure that came from the database or the event bus is not here. Those
// implementations define their own errors and this module does not invent
// names for them; it passes them through unchanged.
var (
	// ErrUnknownKey reports a read or a write of a key no declaration covers.
	// Both directions refuse it, and both refuse it before touching anything:
	// refusing in one place would stop the writer's own mistake but not the
	// case where a declaration was removed while rows for it remain.
	//
	// The fixing action is to name the key in an error message with the key
	// itself, so the caller can add a declaration or correct the key.
	ErrUnknownKey = errors.New("settings: key has no declaration")

	// ErrTypeMismatch reports a value that cannot round-trip under the
	// declared type: encoding the value being written failed, or a value
	// already in the database does not decode under the declaration.
	//
	// The message names the key and the declared type. The fixing action is to
	// write a legal value, or to reconcile the declaration with the old value.
	ErrTypeMismatch = errors.New("settings: value does not match the declared type")

	// ErrInvalidValue reports a value that is disqualified: a written text
	// over the size limit, or a negative refresh interval in configuration.
	//
	// The message names the key and the reason.
	ErrInvalidValue = errors.New("settings: value is not acceptable")

	// ErrDeclarationConflict reports two modules declaring the same key. The
	// message names the key and both declaring modules; the fixing action is
	// for one of them to change its key or its namespace.
	ErrDeclarationConflict = errors.New("settings: two declarations claim the same key")

	// ErrInvalidDeclaration reports a module's declaration defective in
	// itself: no second party is involved, and what this one module declared
	// is enough to judge it unusable. A key name outside the character set, a mount that is not a
	// struct pointer, a type cycle in the expansion, an Items key matching no
	// leaf, a declared type outside the closed type set, and one module
	// reaching one key twice — from two mounts of a schema or from two of its
	// schemas — are all of this class.
	//
	// The message names the declaring module and the defect.
	ErrInvalidDeclaration = errors.New("settings: declaration is invalid")

	// ErrTableMissing reports that loading the snapshot in Init found the
	// settings table absent.
	//
	// It covers that one read failure alone. A connection failure or
	// insufficient permissions are named by their own implementations and
	// passed through: folding them in here would point the fixing action at
	// the wrong cause.
	//
	// The message names the table and the command that applies migrations; the
	// fixing action is to write this module into that command's declaration
	// set.
	ErrTableMissing = errors.New("settings: settings table does not exist")
)
