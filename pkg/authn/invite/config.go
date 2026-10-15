package invite

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
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

// ttlDefaults is what this module is worth without a configuration file: the
// declared lifetime.
var ttlDefaults = moduleConfig{TTL: DefaultTTL}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// TTL is how long an invitation lives from the moment it is issued, when
	// the issue call states no deadline of its own.
	TTL time.Duration `config:"ttl"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	ttl time.Duration
}

// resolve turns decoded configuration into what the module runs on, refusing
// what it cannot run on. A refusal names the key.
func (c moduleConfig) resolve() (resolved, error) {
	if c.TTL < minTTL || c.TTL > maxTTL {
		return resolved{}, fmt.Errorf("%w: %s is %s, and the lifetime is from %s to %s",
			authn.ErrInvalidConfig, ttlKey, c.TTL, minTTL, maxTTL)
	}
	return resolved{ttl: c.TTL}, nil
}

// schema declares this module's input items. The namespace is this module's own
// name: two modules declaring one path is a startup failure even when only one
// of them runs. The lifetime is a deployment-wide period and takes the primary
// configuration source, like the other periods of the capability.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &ttlDefaults}},
		Items: map[string]config.Item{
			ttlItem: {
				Origins: config.OriginPrimary,
				Description: "how long an invitation lives from the moment it is issued, when the " +
					"issue call states no deadline of its own",
			},
		},
	}
}

// readConfig decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one configuration.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
