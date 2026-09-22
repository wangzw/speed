package ratelimit

import "errors"

// ErrDuplicateEndpoint is returned when an endpoint name appears more than
// once in this module's endpoints list.
//
// The remedy is to correct the name, which is not the remedy for a missing
// input item: this module therefore carries its own sentinel instead of
// folding the failure into the configuration module's.
var ErrDuplicateEndpoint = errors.New("ratelimit: an endpoint name appears twice")
