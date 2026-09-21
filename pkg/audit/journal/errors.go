package journal

import "errors"

// ErrUnmatchablePattern reports that a request pattern this module was
// configured with is not one its matcher reads.
//
// It is this module's own sentinel rather than one carried over from the entry
// point, and that is the point of it: the engine and the matcher are two
// parsers, and a pattern the engine accepted when a route was registered is not
// known to be one this matcher accepts until the matcher is built. An engine
// with a wider grammar than the standard library's makes disagreement the
// ordinary case rather than the odd one, so it is a startup failure here rather
// than a panic at a registration call.
var ErrUnmatchablePattern = errors.New("audit.journal: a configured pattern could not be read")
