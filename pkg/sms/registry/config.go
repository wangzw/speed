package registry

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
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
	// sendingEnabledItem is the one runtime switch this module declares: a
	// false value stops every resolve without a restart.
	sendingEnabledItem = "sending-enabled"
)

// sendingEnabledPath is the full path of the sending switch, the shape a
// settings read and an operator's write both name it by.
var sendingEnabledPath = ConfigNamespace + "." + sendingEnabledItem

// defaultPollInterval is how often the background pass runs when the host names
// no interval.
const defaultPollInterval = time.Minute

// configDefaults is what this module is worth without a configuration file. An
// empty configuration template set is legal: a registry whose templates are all
// registered at runtime has none, and the runtime source is where they live.
var configDefaults = moduleConfig{
	PollInterval: defaultPollInterval,
}

// settingDefaults carries the declared default of the sending switch. The
// settings module reads the defaults off this prototype and it never receives a
// value.
type settingDefaults struct {
	SendingEnabled bool `config:"sending-enabled"`
}

// settingDefaultValues is what the switch answers when no row says otherwise,
// including when no settings store is assembled and when a read fails.
var settingDefaultValues = settingDefaults{SendingEnabled: true}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Templates is the configuration source, keyed by the template name a
	// caller sends under: each holds the kind it is for, the ordered parameter
	// names the approved template substitutes, the carrier code for each
	// language, the locally rendered body for each language and the vendor
	// registration identifiers it carries. It is read-only for the life of the
	// process.
	Templates map[string]sms.TemplateSpec `config:"templates"`
	// PollInterval is how often the background pass reloads the runtime set and
	// reads the vendor's review state for the templates still pending. Zero
	// accepts convergence through change events alone, which means a change this
	// replica never receives stays invisible to it until the process restarts.
	PollInterval time.Duration `config:"poll-interval"`
}

// moduleSettings is the configuration this module runs on: the frozen
// configuration source, the set of its names and specs, and the poll interval.
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
	// specs holds the configuration specs as written, used to report a
	// configured record's bodies and attributes. The declaration holds the
	// validated, normalized shape; this holds the values List reports.
	specs map[string]sms.TemplateSpec
	// poll is the background pass interval; zero turns the ticker off.
	poll time.Duration
}

// resolve turns the declared configuration into what the module uses, refusing
// what cannot be honoured.
//
// The configuration template set is checked for self-consistency with
// sms.NewDeclaration, the same check a carrier makes over its own set: a
// repeated parameter name, a language with neither a code nor a body, or a
// placeholder naming an undeclared parameter is a startup failure naming the
// template, not a surprise when a message is first built. An empty set is not a
// defect: a registry whose templates all arrive at runtime declares none, and it
// carries the zero declaration. Whether the set fits the mounted carrier's
// CarrierModel is checked in New, where the provider is reachable.
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
	settings.specs = make(map[string]sms.TemplateSpec, len(c.Templates))
	for name, spec := range c.Templates {
		settings.names[name] = struct{}{}
		settings.specs[name] = spec
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
					"kind it is for, the ordered parameter names the approved template substitutes, the " +
					"carrier code and the locally rendered body for each language, and the vendor " +
					"registration identifiers it carries. These are read-only for the life of the process",
			},
			pollItem: {
				Description: "how often the runtime template set is reloaded and the vendor's review " +
					"state is read for the templates still pending, or zero to rely on change events alone",
			},
		},
	}
}

// settingsSchema declares this module's runtime switch.
//
// The one item is the whole of what an operator can change without a restart:
// the poll interval sets a startup ticker and the template set is configuration,
// so both still need one. The item hangs from the module's namespace as the
// schema's namespace, which keys it by the full path an operator writes:
// sms.registry.sending-enabled.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &settingDefaultValues}},
		Items: map[string]settings.Item{
			sendingEnabledItem: {
				Description: "whether this registry may resolve a message for sending; false answers " +
					"every resolve with the not-sendable sentinel until it is turned back, without a " +
					"restart",
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

// settingsReader reads the sending switch. The store is nil when the assembly
// has none, and every read then answers the declared default, which is a legal
// assembly.
type settingsReader struct {
	store  settings.Store
	logger *slog.Logger

	// mu guards reported, which holds for the switch the fault last written, so
	// that a setting read on every resolve is reported once and not on each.
	mu       sync.Mutex
	reported map[string]string
}

func newSettingsReader(store settings.Store, logger *slog.Logger) *settingsReader {
	if logger == nil {
		logger = slog.Default()
	}
	return &settingsReader{store: store, logger: logger, reported: make(map[string]string)}
}

// sendingEnabled reports whether the registry may resolve a message for sending.
//
// It reads on every call, which is what lets an operator stop sending without a
// restart. A read that fails answers the declared default, true: this switch
// exists to stop sending on purpose, and a settings outage must not take the
// whole capability down with it. The fault is reported at Error once per
// distinct fault rather than on every resolve.
func (r *settingsReader) sendingEnabled() bool {
	if r == nil || r.store == nil {
		return settingDefaultValues.SendingEnabled
	}
	enabled, err := settings.Get[bool](r.store, settings.Global(), sendingEnabledPath)
	if err != nil {
		r.report("the switch cannot be read, so its declared default is used", err.Error())
		return settingDefaultValues.SendingEnabled
	}
	r.report("", "")
	return enabled
}

// report writes the Error line of the switch read in a way other than as it
// says. The same fault is written once and again only when it changes or has
// been cleared in between; an empty problem clears it.
func (r *settingsReader) report(problem, detail string) {
	r.mu.Lock()
	last, reported := r.reported[sendingEnabledPath]
	switch {
	case problem == "":
		delete(r.reported, sendingEnabledPath)
	case !reported || last != detail:
		r.reported[sendingEnabledPath] = detail
	default:
		problem = ""
	}
	r.mu.Unlock()
	if problem != "" {
		r.logger.Error(ModuleName+": "+problem, slog.String("setting", sendingEnabledPath))
	}
}

// normalizedKind is the kind a template carries: a template that names none is a
// notification, which is the default the root declaration applies too.
func normalizedKind(kind sms.Kind) sms.Kind {
	if kind == "" {
		return sms.KindNotification
	}
	return kind
}

// validateAgainstModel holds one template to the mounted carrier's declaration.
//
// It is the carrier-shape check the design puts in three places — New over the
// configuration set, loadSnapshot over the runtime rows and the write calls —
// so a template this carrier cannot carry fails at startup, at load or at the
// write that brought it, never at the first send. Each failure is
// ErrInvalidConfig naming the template and, where it applies, the language, the
// attribute key or the body length.
//
// requireCodes is false for a submission that has not reached the vendor yet: an
// Offer carries a body and no code, and the code the vendor answers with is
// re-checked under the same rules before anything is stored.
func validateAgainstModel(name string, spec sms.TemplateSpec, model sms.CarrierModel, requireCodes bool) error {
	if !model.Content.TemplateCode && len(spec.Codes) > 0 {
		return fmt.Errorf("%w: the template %q declares vendor codes and this carrier does not send by "+
			"code", sms.ErrInvalidConfig, name)
	}
	for _, locale := range sortedKeys(spec.Codes) {
		if spec.Codes[locale] == "" {
			return fmt.Errorf("%w: the template %q has no vendor code for the language %q",
				sms.ErrInvalidConfig, name, locale)
		}
	}
	if requireCodes {
		for _, locale := range effectiveLocales(spec.Codes, spec.Bodies) {
			if spec.Codes[locale] == "" {
				return fmt.Errorf("%w: the template %q has no vendor code for the language %q, and this "+
					"carrier reviews every template per code", sms.ErrInvalidConfig, name, locale)
			}
		}
	}
	accepted := make(map[string]struct{}, len(model.AttributeKeys))
	for _, key := range model.AttributeKeys {
		accepted[key] = struct{}{}
	}
	for _, key := range sortedKeys(spec.Attributes) {
		if _, ok := accepted[key]; !ok {
			return fmt.Errorf("%w: the template %q carries the attribute %q, which this carrier does "+
				"not accept", sms.ErrInvalidConfig, name, key)
		}
	}
	if model.MaxBodyRunes > 0 {
		for _, locale := range sortedKeys(spec.Bodies) {
			if runes := utf8.RuneCountInString(spec.Bodies[locale]); runes > model.MaxBodyRunes {
				return fmt.Errorf("%w: the body of the template %q for the language %q is %d runes, over "+
					"this carrier's bound of %d", sms.ErrInvalidConfig, name, locale, runes, model.MaxBodyRunes)
			}
		}
	}
	return nil
}
