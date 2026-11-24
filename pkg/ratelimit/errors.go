package ratelimit

import "errors"

// ErrDuplicateEndpoint is returned when an endpoint name appears more than once
// in this module's endpoints list.
var ErrDuplicateEndpoint = errors.New("ratelimit: an endpoint name appears twice")
