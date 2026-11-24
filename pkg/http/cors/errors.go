package cors

import "errors"

// ErrInvalidPolicy reports a CORS policy the module refuses: a key no field takes, a
// value outside its domain, or a combination the library would reject.
var ErrInvalidPolicy = errors.New("http.cors: the CORS policy is invalid")
