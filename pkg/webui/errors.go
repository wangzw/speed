package webui

import "errors"

// ErrNilAssets reports a constructor call that was handed no asset filesystem.
// The module has nothing to serve without one, and the fault is in the host's
// own code — it either left the argument out or built it from a value that was
// nil — so the startup ends with this sentinel rather than a module that
// answers 404 to every request.
var ErrNilAssets = errors.New("webui: the asset filesystem is nil")
