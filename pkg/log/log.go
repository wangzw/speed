// Package log carries structured logging: the capability modules take up, the configured handler chain, redaction, context storage and the outbound HTTP transport.
// Its calling surface is the standard library's *slog.Logger, and it defines no logging API of its own.
package log

import "log/slog"

// Logger is the capability this module delivers.
// It is taken up through the registry, and it is not the logger object: a *slog.Logger is a
// concrete type and cannot designate a capability.
type Logger interface {
	// Named returns a logger carrying an attribute that identifies the calling module.
	// The handler chain behind it is shared; a module takes one in New and holds it for the logs it
	// writes without a request context.
	Named(name string) *slog.Logger
	// Redaction returns the registration interface for redaction rules.
	Redaction() Redaction
}

// Redaction registers redaction rules.
// Rules are append-only: there is no way to remove one or to switch the layer off, so a
// registration that is too wide is corrected by changing the registering code, not at run time.
type Redaction interface {
	// AddKeys registers sensitive key names.
	// A key path is compared segment by segment, so a registered name matches a group opened by
	// WithGroup as well as a leaf attribute.
	AddKeys(keys ...string)
	// AddPattern registers a value-shape rule under a name.
	// The matcher runs on every resolved string value and error text of every record, so its cost
	// lands on the whole process's logging path.
	AddPattern(name string, match Matcher)
}

// Matcher reports the spans of a string value that need masking, and returns nothing when the
// value carries none.
type Matcher func(value string) []Span

// Span is a half-open range of a value, masked from Start up to but not including End.
type Span struct {
	// Start and End are the first byte offset of the span and the offset just
	// past its end.
	Start, End int
}
