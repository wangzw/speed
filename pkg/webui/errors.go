package webui

import "errors"

// ErrNilAssets reports a constructor call handed no asset filesystem, which is a
// fault in the host's own code.
var ErrNilAssets = errors.New("webui: the asset filesystem is nil")
