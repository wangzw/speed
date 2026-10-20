package config

import "errors"

// The sentinel errors configuration loading returns. Every one of them happens
// during startup and aborts it; callers tell the classes apart with errors.Is,
// and the error text carries what is needed to locate the cause and states the
// fixing action.
var (
	// ErrMalformedLocator reports a config locator that is not a legal URI,
	// or a shape the selected transport does not serve, such as a remote
	// host handed to the file source. Both leave the host the same thing to
	// do: fix the locator it gave.
	ErrMalformedLocator = errors.New("config: config locator is malformed")
	// ErrUnknownScheme reports a locator scheme no registered Source answers
	// for.
	ErrUnknownScheme = errors.New("config: no source registered for the locator scheme")
	// ErrUndeterminedFormat reports that the format of the data could not be
	// determined: an unrecognised file extension, or a remote locator
	// without a format parameter.
	ErrUndeterminedFormat = errors.New("config: format of the primary source could not be determined")
	// ErrUnknownFormat reports a determined format name no registered Format
	// answers for.
	ErrUnknownFormat = errors.New("config: no format registered for the format name")
	// ErrSourceUnavailable reports a Fetch failure: the file is missing, the
	// remote is unreachable, the permissions are insufficient.
	ErrSourceUnavailable = errors.New("config: primary source could not be read")
	// ErrMalformedConfig reports data the selected format could not parse,
	// and data that parses but gives one path twice, as a key repeated in one
	// mapping or as the same path written both flat and nested: only one of
	// the two values could take effect.
	ErrMalformedConfig = errors.New("config: primary source could not be parsed")
	// ErrConfigConflict reports a conflict between two declarations, found
	// while the manifest was collected.
	ErrConfigConflict = errors.New("config: input item declarations conflict")
	// ErrInvalidSchema reports a single declaration that is defective in
	// itself, with no second party involved.
	ErrInvalidSchema = errors.New("config: input item declaration is invalid")
	// ErrMalformedCommandLine classifies a syntax error the command-line
	// library reported while reading an argument: one that ends without the
	// value it takes, or one written in a syntax the library refuses. The
	// syntax is the library's, so the forms are not enumerated here; what
	// this class says is that the command line is wrong in itself, as
	// against a name the syntax admits and no item declares.
	//
	// A word that is no argument at all is outside the class. Which words a
	// command accepts is the library's own finding, raised past the seam
	// this module classifies at: an unknown subcommand, and a bare word on
	// a command that takes none, reach the host as the library reported
	// them.
	ErrMalformedCommandLine = errors.New("config: command line is malformed")
	// ErrUnknownKey reports a key the primary source or the command line is
	// not allowed to give: the manifest does not have it, or it has it and
	// the item does not accept that origin. The command-line side judges by
	// the participants of the selected command, the primary source by the
	// whole manifest, since one source is shared by every command. The
	// environment layer does not raise it.
	ErrUnknownKey = errors.New("config: source gave a key it does not accept")
	// ErrTypeMismatch reports a value that cannot be converted to the type
	// of the target field.
	ErrTypeMismatch = errors.New("config: value does not convert to the type of the field")
	// ErrMissingRequired reports a required item none of the primary source,
	// the environment, the command line and the host defaults document gave a
	// value for.
	ErrMissingRequired = errors.New("config: required input item has no value")
)
