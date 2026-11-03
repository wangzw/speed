package tenantadmin

import "errors"

// ErrInvalidConfig reports a configuration this module cannot run on. The one
// such shape is the endpoint list: it is required, holds concrete endpoint names
// only, and names no endpoint twice. The failure names the key and the item, so
// the fixing action is visible.
//
// It is this module's own sentinel rather than the settings capability's: a
// configuration defect is this module's to name, and the settings module states
// no sentinel for a configuration one of its consumers cannot run on.
var ErrInvalidConfig = errors.New("settings.tenantadmin: the configuration cannot be run on")
