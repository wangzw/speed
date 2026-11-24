package tenantadmin

import "errors"

// ErrInvalidConfig reports a configuration this module cannot run on: the endpoint
// list is required, concrete and duplicate-free, and a refusal names the key.
var ErrInvalidConfig = errors.New("settings.tenantadmin: the configuration cannot be run on")
