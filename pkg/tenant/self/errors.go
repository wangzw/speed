package self

import "errors"

// ErrInvalidConfig reports a configuration this module cannot run on: an
// endpoint list that is empty, holds an empty name, holds the every-endpoint
// name, or names an endpoint twice. A failure names the key, so a reader finds
// what to write.
var ErrInvalidConfig = errors.New("tenant.self: a configuration this module cannot run on")
