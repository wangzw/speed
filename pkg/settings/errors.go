package settings

import "errors"

var (
	// ErrUnknownKey reports a read or a write of a key no declaration covers; both
	// directions refuse it before touching anything.
	ErrUnknownKey = errors.New("settings: key has no declaration")

	// ErrTypeMismatch reports a value that cannot round-trip under the declared type,
	// on a write or on a stored row that does not decode.
	ErrTypeMismatch = errors.New("settings: value does not match the declared type")

	// ErrInvalidValue reports a written value outside its item's domain, a text over
	// the size limit, or a negative refresh interval.
	ErrInvalidValue = errors.New("settings: value is not acceptable")

	// ErrDeclarationConflict reports two modules declaring the same key, naming the
	// key and both modules.
	ErrDeclarationConflict = errors.New("settings: two declarations claim the same key")

	// ErrInvalidDeclaration reports a declaration defective in itself, with no second
	// party involved: a bad key, mount, type, Items key, domain or default.
	ErrInvalidDeclaration = errors.New("settings: declaration is invalid")

	// ErrTableMissing reports that the snapshot load found the settings table absent.
	// Only that one read failure is classified here.
	ErrTableMissing = errors.New("settings: settings table does not exist")

	// ErrNotLoaded reports a read made before Init loaded the snapshot; the state is
	// unavailable, never a default.
	ErrNotLoaded = errors.New("settings: the snapshot has not been loaded yet")
)
