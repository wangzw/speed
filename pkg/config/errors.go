package config

import "errors"

// The sentinel errors configuration loading returns during startup. Callers
// tell them apart with errors.Is.
var (
	// ErrMalformedLocator reports a config locator that is not a legal URI, or a
	// shape the selected transport does not serve.
	ErrMalformedLocator = errors.New("config: config locator is malformed")
	// ErrUnknownScheme reports a locator scheme no registered Source answers for.
	ErrUnknownScheme = errors.New("config: no source registered for the locator scheme")
	// ErrUndeterminedFormat reports that the format of the data could not be
	// determined.
	ErrUndeterminedFormat = errors.New("config: format of the primary source could not be determined")
	// ErrUnknownFormat reports a determined format name no registered Format
	// answers for.
	ErrUnknownFormat = errors.New("config: no format registered for the format name")
	// ErrSourceUnavailable reports a Fetch failure.
	ErrSourceUnavailable = errors.New("config: primary source could not be read")
	// ErrMalformedConfig reports data the selected format could not parse, and
	// data that parses but gives one path twice.
	ErrMalformedConfig = errors.New("config: primary source could not be parsed")
	// ErrConfigConflict reports a conflict between two declarations, found while
	// the manifest was collected.
	ErrConfigConflict = errors.New("config: input item declarations conflict")
	// ErrInvalidSchema reports a single declaration that is defective in itself.
	ErrInvalidSchema = errors.New("config: input item declaration is invalid")
	// ErrMalformedCommandLine reports a syntax error the command-line library
	// reported while reading an argument. An unknown subcommand or a bare word a
	// command does not accept is outside the class.
	ErrMalformedCommandLine = errors.New("config: command line is malformed")
	// ErrUnknownKey reports a key the primary source or the command line is not
	// allowed to give. The environment layer does not raise it.
	ErrUnknownKey = errors.New("config: source gave a key it does not accept")
	// ErrTypeMismatch reports a value that cannot be converted to the type of the
	// target field.
	ErrTypeMismatch = errors.New("config: value does not convert to the type of the field")
	// ErrMissingRequired reports a required item no source gave a value for.
	ErrMissingRequired = errors.New("config: required input item has no value")
)
