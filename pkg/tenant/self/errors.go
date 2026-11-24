package self

import "errors"

// ErrInvalidConfig reports a configuration this module cannot run on; the error
// names the offending key.
var ErrInvalidConfig = errors.New("tenant.self: a configuration this module cannot run on")
