package otel

import "errors"

// The failures this module judges for itself. A failure to take up the entry
// module, and an undeclared endpoint name, are not among them: they are core's
// and the entry module's sentinels, wrapped with the input item that asked for
// them and still matchable by errors.Is, so neither gets a second name here.
var (
	// ErrInvalidSampleRatio reports a ratio that is not a finite number within
	// [0,1]. The text names the accepted interval, which is the whole of what a
	// reader needs to fix it.
	ErrInvalidSampleRatio = errors.New("observability.otel: sample-ratio must lie within [0,1]")

	// ErrInvalidBatchParam reports a negative batch parameter. Zero is not this
	// error: it means the upstream default, and the text says so.
	ErrInvalidBatchParam = errors.New("observability.otel: a batch parameter is negative")

	// ErrInvalidDuration reports zero or a negative value where the duration
	// carries meaning rather than switching something off: a batch timeout of
	// zero fails every batch, and an export period of zero is no period at all.
	ErrInvalidDuration = errors.New("observability.otel: a duration must be positive")

	// ErrWildcardBesideEndpointNames reports the endpoint list "*" shares with
	// another entry, a name or a second "*". "*" already covers every declared
	// endpoint, so what is written beside it would either repeat it or say the
	// list means something other than it reads as. The text names the key and
	// the two correct writings.
	ErrWildcardBesideEndpointNames = errors.New(
		`observability.otel.endpoints: "*" stands for every declared endpoint and must be the only entry in the list`)
)
