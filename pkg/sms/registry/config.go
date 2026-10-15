package registry

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms"
)

// The keys this module accepts, relative to its namespace.
const (
	// templatesItem carries the configuration source: the templates the
	// operator wrote into the configuration and had the vendor approve. It is a
	// container leaf, so it takes its value from the primary config source
	// alone.
	templatesItem = "templates"
	// pollItem is how often the background pass runs.
	pollItem = "poll-interval"
)

// defaultPollInterval is how often the background pass runs when the host names
// no interval.
const defaultPollInterval = time.Minute

// configDefaults is what this module is worth without a configuration file. An
// empty configuration template set is legal: a registry whose templates are all
// registered at runtime has none, and the runtime source is where they live.
var configDefaults = moduleConfig{
	PollInterval: defaultPollInterval,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Templates is the configuration source, keyed by the template name a
	// caller sends under: each holds the ordered parameter names the approved
	// template substitutes and the carrier code for each language. It is
	// read-only for the life of the process.
	Templates map[string]sms.TemplateSpec `config:"templates"`
	// PollInterval is how often the background pass reloads the runtime set and
	// reads the vendor's review state for the templates still pending. Zero
	// accepts convergence through change events alone, which means a change this
	// replica never receives stays invisible to it until the process restarts.
	PollInterval time.Duration `config:"poll-interval"`
}

// moduleSettings is the configuration this module runs on: the frozen
// configuration source, the set of its names, and the poll interval.
type moduleSettings struct {
	// declaration is the configuration templates frozen into a declaration, so
	// the registry delegates a configured message's resolution to it and reuses
	// its structural checks. It is the zero Declaration when the configuration
	// declares none.
	declaration sms.Declaration
	// names is the set of configuration template names. It is what Resolve
	// checks first and what the management calls refuse, without walking the
	// declaration.
	names map[string]struct{}
	// poll is the background pass interval; zero turns the ticker off.
	poll time.Duration
}

// resolve turns the declared configuration into what the module uses, refusing
// what cannot be honoured.
//
// The configuration template set is checked for self-consistency with
// sms.NewDeclaration, the same check a carrier makes over its own set: a
// template with no parameter, a repeated parameter name or a language with no
// code is a startup failure naming the template, not a surprise when a message
// is first built. An empty set is not a defect: a registry whose templates all
// arrive at runtime declares none, and it carries the zero declaration.
func (c moduleConfig) resolve() (moduleSettings, error) {
	if c.PollInterval < 0 {
		return moduleSettings{}, fmt.Errorf("%w: %s.%s is %s, and a poll interval cannot be negative. "+
			"Set it to zero to poll only when a template changes",
			sms.ErrInvalidConfig, ConfigNamespace, pollItem, c.PollInterval)
	}
	settings := moduleSettings{names: make(map[string]struct{}, len(c.Templates)), poll: c.PollInterval}
	if len(c.Templates) == 0 {
		return settings, nil
	}
	decl, err := sms.NewDeclaration(c.Templates)
	if err != nil {
		return moduleSettings{}, fmt.Errorf("%s.%s: %w", ConfigNamespace, templatesItem, err)
	}
	for name := range c.Templates {
		settings.names[name] = struct{}{}
	}
	settings.declaration = decl
	return settings, nil
}

// schema declares this module's input items.
//
// The namespace is this module's own name, as it is for every registering
// module: the list is collected over every registered module whether or not it
// runs, so two modules declaring one path is a startup failure even when only
// one of them ever runs.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			templatesItem: {
				// A container leaf: a map takes its origin set from the primary
				// source alone, and it is never re-read while the process runs.
				Origins: config.OriginPrimary,
				Description: "the templates the operator wrote into the configuration and had the " +
					"vendor approve, keyed by the template name the caller sends under; each holds the " +
					"ordered parameter names the approved template substitutes and the carrier code for " +
					"each language. These are read-only for the life of the process",
			},
			pollItem: {
				Description: "how often the runtime template set is reloaded and the vendor's review " +
					"state is read for the templates still pending, or zero to rely on change events alone",
			},
		},
	}
}

// readSettings decodes this module's section. Prepare and New both go through
// it, so the two see one configuration.
func readSettings(reader config.Reader) (moduleSettings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return moduleSettings{}, err
	}
	return cfg.resolve()
}
