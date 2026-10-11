package mount

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// Endpoint is the endpoint the capability's routes are mounted on: configured
// when the configuration names one, otherwise the one endpoint the entry point
// declares. With none named and a number of declared endpoints other than one
// there is nothing to pick, and the answer is ErrRoutesUnconfigured.
//
// key is the configuration key configured was read from, for the message. The
// module that mounts routes calls this in its Init, once the entry point has an
// instance to enumerate; the gateway calls it again in Start to judge the same
// endpoint, so the two read the one rule.
func Endpoint(configured string, declared []string, key string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if len(declared) == 1 {
		return declared[0], nil
	}
	if len(declared) == 0 {
		return "", fmt.Errorf("%w: %s is empty and the entry point declares no endpoint",
			authn.ErrRoutesUnconfigured, key)
	}
	return "", fmt.Errorf("%w: %s is empty and the entry point declares %d endpoints (%s). Name the "+
		"one the authentication routes are mounted on", authn.ErrRoutesUnconfigured, key,
		len(declared), strings.Join(declared, ", "))
}
