package otel

import "errors"

// The failures this module judges for itself. Everything else it hands back
// wrapped: a missing reading face is core's sentinel, an undeclared endpoint
// name is the entry module's, and neither gets a second name here.
var (
	// ErrEmptyServiceName reports a service name that a source gave as the
	// empty string. The item is required, which catches a value no source
	// supplied; an empty value is a different input and needs its own answer.
	ErrEmptyServiceName = errors.New("observability.otel: service-name is set but empty")

	// ErrInvalidSampleRatio reports a ratio outside [0,1]. The text names the
	// accepted interval, which is the whole of what a reader needs to fix it.
	ErrInvalidSampleRatio = errors.New("observability.otel: sample-ratio must lie within [0,1]")

	// ErrInvalidBatchParam reports a negative batch parameter. Zero is not this
	// error: it means the upstream default, and the text says so.
	ErrInvalidBatchParam = errors.New("observability.otel: a batch parameter is negative")

	// ErrInvalidDuration reports zero or a negative value where the duration
	// carries meaning rather than switching something off: a batch timeout of
	// zero fails every batch, and an export period of zero is no period at all.
	ErrInvalidDuration = errors.New("observability.otel: a duration must be positive")
)
