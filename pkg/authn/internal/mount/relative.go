package mount

import (
	"fmt"
	"strings"
)

// RelativePath returns the path a Mounter expects for a route written absolute
// from the capability's API root: absolute with base removed.
//
// A module declares its operations with paths relative to its Spec's API root,
// and the mounting joins the mounter's base to the path it is given. The two
// forms differ by the capability's base, so the route is derived from the one
// declaration and translated here rather than written twice.
func RelativePath(base, absolute string) (string, error) {
	if base == "" {
		return absolute, nil
	}
	if !strings.HasPrefix(absolute, base+"/") {
		return "", fmt.Errorf("mount: the route %q is not under the capability base %q", absolute, base)
	}
	return strings.TrimPrefix(absolute, base), nil
}
