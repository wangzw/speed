package self

import "errors"

// ErrInvalidConfig reports a configuration this module cannot run on, such as an invalid
// endpoint list.
var ErrInvalidConfig = errors.New("tenant.self: a configuration this module cannot run on")
