package cors

import "errors"

// ErrInvalidPolicy reports a CORS policy the module refuses: a key no field
// takes, a value outside its domain, or a combination the library would reject.
// Whoever meets it edits this module's endpoints entry, which is a different
// action from writing an endpoint the configuration is missing, so it is a
// sentinel of its own rather than one shared with that failure.
var ErrInvalidPolicy = errors.New("http.cors: the CORS policy is invalid")
