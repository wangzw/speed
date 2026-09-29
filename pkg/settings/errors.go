package settings

import "errors"

// The sentinel errors this module raises for the failures it judges itself.
// The startup ones abort the startup; the read-path and write-path ones reach
// the caller of Get, Set or Delete. Callers tell the classes apart with
// errors.Is, and every wrap uses %w.
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

	// ErrInvalidValue reports a value that is disqualified: a written value
	// outside the domain its item's declaration gives, a written text over the
	// size limit, or a negative refresh interval in configuration.
	//
	// The message names the key and the reason: for a value outside its
	// domain, the rule the declaration states. The fixing action is to write a
	// value the rule admits; nothing was written.
	ErrInvalidValue = errors.New("settings: value is not acceptable")

	// ErrDeclarationConflict reports two modules declaring the same key. The
	// message names the key and both declaring modules; the fixing action is
	// for one of them to change its key or its namespace.
	ErrDeclarationConflict = errors.New("settings: two declarations claim the same key")

	// ErrInvalidDeclaration reports a module's declaration defective in
	// itself: no second party is involved, and what this one module declared
	// is enough to judge it unusable. A key name outside the character set, a
	// mount that is not a struct pointer, a type cycle in the expansion, an
	// Items key matching no leaf, a declared type outside the closed type set,
	// a domain over another type than the item's or with no rule or no
	// predicate, a default its own domain does not admit, and one module
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

	// ErrNotLoaded reports a read made before this module's Init first loaded
	// the snapshot. Until then nothing has been read from the table, and a
	// read would answer with the declared default whatever the table holds.
	//
	// It is a fact about the assembly rather than about the data: the reading
	// module was initialised ahead of this one, or the drive stopped short of
	// Init. The message names the key; the fixing action is for the reading
	// module to declare Requires (*Store)(nil), which initialises it after
	// this one, or for the drive to run to Init at least.
	ErrNotLoaded = errors.New("settings: the snapshot has not been loaded yet")
)
