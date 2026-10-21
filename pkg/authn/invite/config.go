package invite

import (
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// The key this module accepts, relative to its namespace, and the full path a
// failure names.
const (
	ttlItem = "ttl"
	ttlKey  = ConfigNamespace + "." + ttlItem
)

// DefaultTTL is the lifetime an invitation has when none is configured. The
// design fixes the key but not the number, so this is this module's declared
// default.
const DefaultTTL = 168 * time.Hour

// The domain of the configured lifetime: below an hour an invitation is a
// nuisance, and past a quarter the value stops meaning anything.
const (
	minTTL = time.Hour
	maxTTL = 2160 * time.Hour
)

// options carries the settings this module owns and their declared defaults.
type options struct {
	// TTL is how long an invitation lives from the moment it is issued, when
	// the issue call states no deadline of its own.
	TTL time.Duration `config:"ttl"`
}

// defaults is the prototype the settings module reads the declared defaults off.
// It never receives a value.
var defaults = options{TTL: DefaultTTL}

// ttlInDomain is the domain of the lifetime setting. A read is judged against it
// as well as a write, since a row written before the domain was declared, or
// written around the store, is served as it is.
func ttlInDomain(d time.Duration) bool { return d >= minTTL && d <= maxTTL }

// configSchema declares this module's config section. The section holds no
// input item: the lifetime that used to live here is a setting now, but the
// section stays declared so that a source still writing one of its keys is
// refused by that key's full path rather than by its parent.
func configSchema() config.Schema {
	return config.Schema{Namespace: ConfigNamespace}
}

// settingsSchema declares this module's runtime parameter. The namespace is this
// module's own name: two modules declaring one path is a startup failure even when
// only one of them runs.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			ttlItem: {
				Description: "how long an invitation lives from the moment it is issued, when the " +
					"issue call states no deadline of its own",
				Domain: settings.DomainOf("durations from "+minTTL.String()+" to "+maxTTL.String(), ttlInDomain),
			},
		},
	}
}
